package remixlab

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"

	"video-production-console/internal/agentruntime/openaicompat"
)

var (
	ErrAgentModel       = errors.New("agent model is required")
	ErrAgentUpstream    = errors.New("agent upstream failed")
	ErrEmptyAgentReply  = errors.New("agent returned an empty reply")
	ErrProposalNotFound = errors.New("proposal was not found")
	ErrUnknownProposal  = errors.New("unknown proposal type")
)

var secretLike = regexp.MustCompile(`(?i)(sk-[A-Za-z0-9_-]{6,}|Bearer\s+\S+)`)

type AgentSettingsView struct {
	Model            string `json:"model"`
	BaseURL          string `json:"base_url"`
	ReasoningEffort  string `json:"reasoning_effort"`
	APIKeyConfigured bool   `json:"api_key_configured"`
}

type AgentSettingsInput struct {
	Model           string
	BaseURL         string
	ReasoningEffort string
	APIKey          string
	ClearAPIKey     bool
}

type AgentProposal struct {
	ID      string          `json:"id"`
	Type    string          `json:"type"`
	Summary string          `json:"summary"`
	Payload json.RawMessage `json:"payload"`
}

type AgentChatResult struct {
	Reply     string          `json:"reply"`
	Proposals []AgentProposal `json:"proposals"`
}

// AgentChatTimeout is how long the console keeps waiting for a non-streaming
// agent reply after the browser has already gone away.
const AgentChatTimeout = 12 * time.Minute

type AgentLastView struct {
	At           time.Time       `json:"at"`
	Message      string          `json:"message"`
	ExperimentID string          `json:"experiment_id,omitempty"`
	Reply        string          `json:"reply,omitempty"`
	Proposals    []AgentProposal `json:"proposals,omitempty"`
	Error        string          `json:"error,omitempty"`
}

type AgentConfirmResult struct {
	Type   string `json:"type"`
	Result any    `json:"result,omitempty"`
}

// AgentHistoryTurn is one side of a stored conversation exchange.
type AgentHistoryTurn struct {
	Role      string          `json:"role"`
	Text      string          `json:"text"`
	Proposals []AgentProposal `json:"proposals,omitempty"`
	At        time.Time       `json:"at"`
}

type agentHistoryFile struct {
	Turns []AgentHistoryTurn `json:"turns"`
}

const (
	// maxStoredAgentTurns caps agent_history.json so it cannot grow forever.
	maxStoredAgentTurns = 40
	// maxRequestAgentTurns is how many stored turns are replayed to the model.
	maxRequestAgentTurns = 12
	// maxRequestTurnRunes clips a single replayed turn (pasted articles stay in
	// the stored history but are trimmed for the request).
	maxRequestTurnRunes = 3000
)

type agentFile struct {
	Model            string `json:"model"`
	BaseURL          string `json:"base_url"`
	ReasoningEffort  string `json:"reasoning_effort"`
	APIKeyCiphertext string `json:"api_key_ciphertext"`
}

type agentPendingFile struct {
	Proposals []AgentProposal `json:"proposals"`
}

func (s *Service) agentSettingsPath() string {
	return filepath.Join(filepath.Clean(s.dataRoot), "remix_lab", "agent.json")
}

func (s *Service) agentPendingPath() string {
	return filepath.Join(filepath.Clean(s.dataRoot), "remix_lab", "agent_pending.json")
}

func (s *Service) agentLastPath() string {
	return filepath.Join(filepath.Clean(s.dataRoot), "remix_lab", "agent_last.json")
}

func (s *Service) agentHistoryPath() string {
	return filepath.Join(filepath.Clean(s.dataRoot), "remix_lab", "agent_history.json")
}

func (s *Service) GetAgentHistory() ([]AgentHistoryTurn, error) {
	raw, err := os.ReadFile(s.agentHistoryPath())
	if err != nil {
		if os.IsNotExist(err) {
			return []AgentHistoryTurn{}, nil
		}
		return nil, fmt.Errorf("read agent history: %w", err)
	}
	var file agentHistoryFile
	if err := json.Unmarshal(raw, &file); err != nil {
		return nil, fmt.Errorf("decode agent history: %w", err)
	}
	if file.Turns == nil {
		file.Turns = []AgentHistoryTurn{}
	}
	return file.Turns, nil
}

func (s *Service) ClearAgentHistory() error {
	if err := os.Remove(s.agentHistoryPath()); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("clear agent history: %w", err)
	}
	if err := os.Remove(s.agentLastPath()); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("clear agent last: %w", err)
	}
	return nil
}

func (s *Service) appendAgentHistory(turns ...AgentHistoryTurn) {
	existing, err := s.GetAgentHistory()
	if err != nil {
		slog.Default().Error("remix lab agent history read failed", "error", err)
		existing = []AgentHistoryTurn{}
	}
	now := time.Now()
	for i := range turns {
		if turns[i].At.IsZero() {
			turns[i].At = now
		}
	}
	existing = append(existing, turns...)
	if len(existing) > maxStoredAgentTurns {
		existing = existing[len(existing)-maxStoredAgentTurns:]
	}
	if err := s.saveJSONFile(s.agentHistoryPath(), agentHistoryFile{Turns: existing}); err != nil {
		slog.Default().Error("remix lab agent history save failed", "error", err)
	}
}

// historyMessages replays recent exchanges so the model keeps conversation
// context. Assistant turns are replayed as reply text plus draft summaries.
func historyMessages(turns []AgentHistoryTurn) []openaicompat.Message {
	if len(turns) > maxRequestAgentTurns {
		turns = turns[len(turns)-maxRequestAgentTurns:]
	}
	out := make([]openaicompat.Message, 0, len(turns))
	for _, turn := range turns {
		role := "user"
		if turn.Role == "assistant" {
			role = "assistant"
		}
		text := clipRunes(strings.TrimSpace(turn.Text), maxRequestTurnRunes)
		if role == "assistant" && len(turn.Proposals) > 0 {
			var summaries []string
			for _, p := range turn.Proposals {
				if summary := strings.TrimSpace(p.Summary); summary != "" {
					summaries = append(summaries, summary)
				}
			}
			if len(summaries) > 0 {
				text += "\n（这轮给过的草案：" + strings.Join(summaries, "；") + "）"
			}
		}
		if text == "" {
			continue
		}
		out = append(out, openaicompat.Message{Role: role, Content: text})
	}
	return out
}

func (s *Service) GetAgentSettings() (AgentSettingsView, error) {
	file, err := s.loadAgentFile()
	if err != nil {
		return AgentSettingsView{}, err
	}
	return AgentSettingsView{
		Model:            file.Model,
		BaseURL:          file.BaseURL,
		ReasoningEffort:  file.ReasoningEffort,
		APIKeyConfigured: strings.TrimSpace(file.APIKeyCiphertext) != "",
	}, nil
}

func (s *Service) PutAgentSettings(in AgentSettingsInput) (AgentSettingsView, error) {
	model := strings.TrimSpace(in.Model)
	if model == "" {
		return AgentSettingsView{}, ErrAgentModel
	}
	file, err := s.loadAgentFile()
	if err != nil {
		return AgentSettingsView{}, err
	}
	file.Model = model
	file.BaseURL = strings.TrimSpace(in.BaseURL)
	file.ReasoningEffort = strings.TrimSpace(in.ReasoningEffort)
	switch {
	case in.ClearAPIKey:
		file.APIKeyCiphertext = ""
	case strings.TrimSpace(in.APIKey) != "":
		cipher, err := s.protector.Protect([]byte(strings.TrimSpace(in.APIKey)))
		if err != nil {
			return AgentSettingsView{}, err
		}
		file.APIKeyCiphertext = base64.StdEncoding.EncodeToString(cipher)
	}
	if err := s.saveJSONFile(s.agentSettingsPath(), file); err != nil {
		return AgentSettingsView{}, err
	}
	return s.GetAgentSettings()
}

func (s *Service) GetAgentLast() (AgentLastView, bool, error) {
	raw, err := os.ReadFile(s.agentLastPath())
	if err != nil {
		if os.IsNotExist(err) {
			return AgentLastView{}, false, nil
		}
		return AgentLastView{}, false, fmt.Errorf("read agent last: %w", err)
	}
	var view AgentLastView
	if err := json.Unmarshal(raw, &view); err != nil {
		return AgentLastView{}, false, fmt.Errorf("decode agent last: %w", err)
	}
	if cleaned, ok := repairWrappedAgentLast(view); ok {
		s.rememberAgentLast(cleaned)
		if len(cleaned.Proposals) > 0 {
			if err := s.saveJSONFile(s.agentPendingPath(), agentPendingFile{Proposals: cleaned.Proposals}); err != nil {
				slog.Default().Error("remix lab agent pending save failed", "error", err)
			}
		}
		return cleaned, true, nil
	}
	return view, true, nil
}

func repairWrappedAgentLast(view AgentLastView) (AgentLastView, bool) {
	if !looksLikeWrappedAgentJSON(view.Reply) {
		return view, false
	}
	parsed, err := parseAgentReply(view.Reply)
	if err != nil || strings.TrimSpace(parsed.Reply) == "" {
		return view, false
	}
	for i := range parsed.Proposals {
		if strings.TrimSpace(parsed.Proposals[i].ID) == "" {
			parsed.Proposals[i].ID = uuid.NewString()
		}
		if !validProposalType(parsed.Proposals[i].Type) {
			parsed.Proposals[i].Type = ""
		}
	}
	kept := parsed.Proposals[:0]
	for _, p := range parsed.Proposals {
		if p.Type == "" {
			continue
		}
		kept = append(kept, p)
	}
	view.Reply = parsed.Reply
	view.Proposals = kept
	view.Error = ""
	return view, true
}

func looksLikeWrappedAgentJSON(raw string) bool {
	trimmed := strings.TrimSpace(raw)
	return strings.Contains(trimmed, `"reply"`) && strings.Contains(trimmed, `"proposals"`)
}

func (s *Service) rememberAgentLast(view AgentLastView) {
	view.At = time.Now()
	if view.Proposals == nil {
		view.Proposals = []AgentProposal{}
	}
	if err := s.saveJSONFile(s.agentLastPath(), view); err != nil {
		slog.Default().Error("remix lab agent last save failed", "error", err)
	}
}

func (s *Service) AgentChat(ctx context.Context, message, experimentID string) (AgentChatResult, error) {
	message = strings.TrimSpace(message)
	if message == "" {
		return AgentChatResult{}, fmt.Errorf("message is required")
	}
	file, err := s.loadAgentFile()
	if err != nil {
		return AgentChatResult{}, err
	}
	if strings.TrimSpace(file.Model) == "" {
		return AgentChatResult{}, ErrAgentModel
	}
	rt, err := s.runtime.Runtime(ctx)
	if err != nil {
		return AgentChatResult{}, err
	}
	baseURL := strings.TrimSpace(file.BaseURL)
	if baseURL == "" {
		baseURL = strings.TrimSpace(rt.RemixBaseURL)
	}
	apiKey, err := s.resolveAgentKey(file, rt)
	if err != nil {
		return AgentChatResult{}, err
	}
	user, err := s.buildAgentUser(ctx, message, experimentID)
	if err != nil {
		return AgentChatResult{}, err
	}
	history, err := s.GetAgentHistory()
	if err != nil {
		slog.Default().Error("remix lab agent history read failed", "error", err)
		history = nil
	}
	messages := make([]openaicompat.Message, 0, len(history)+2)
	messages = append(messages, openaicompat.Message{Role: "system", Content: agentSystemPrompt()})
	messages = append(messages, historyMessages(history)...)
	messages = append(messages, openaicompat.Message{Role: "user", Content: user})
	req := openaicompat.ChatRequest{
		Model:           file.Model,
		ReasoningEffort: file.ReasoningEffort,
		Stream:          true,
		Messages:        messages,
	}
	resp, err := s.callAgentChat(baseURL, apiKey, req)
	if err != nil {
		slog.Default().Error("remix lab agent chat upstream failed", "error", err)
		wrapped := fmt.Errorf("%w: %s", ErrAgentUpstream, sanitizeAgentError(err))
		s.rememberAgentLast(AgentLastView{Message: message, ExperimentID: experimentID, Error: sanitizeAgentError(wrapped)})
		return AgentChatResult{}, wrapped
	}
	if len(resp.Choices) == 0 || strings.TrimSpace(resp.Choices[0].Message.Content) == "" {
		s.rememberAgentLast(AgentLastView{Message: message, ExperimentID: experimentID, Error: sanitizeAgentError(ErrEmptyAgentReply)})
		return AgentChatResult{}, ErrEmptyAgentReply
	}
	parsed, err := parseAgentReply(resp.Choices[0].Message.Content)
	if err != nil {
		out := AgentChatResult{Reply: strings.TrimSpace(resp.Choices[0].Message.Content), Proposals: []AgentProposal{}}
		s.rememberAgentLast(AgentLastView{Message: message, ExperimentID: experimentID, Reply: out.Reply, Proposals: out.Proposals})
		s.appendAgentHistory(
			AgentHistoryTurn{Role: "user", Text: message},
			AgentHistoryTurn{Role: "assistant", Text: out.Reply},
		)
		return out, nil
	}
	for i := range parsed.Proposals {
		if strings.TrimSpace(parsed.Proposals[i].ID) == "" {
			parsed.Proposals[i].ID = uuid.NewString()
		}
		if !validProposalType(parsed.Proposals[i].Type) {
			parsed.Proposals[i].Type = ""
		}
	}
	kept := parsed.Proposals[:0]
	for _, p := range parsed.Proposals {
		if p.Type == "" {
			continue
		}
		kept = append(kept, p)
	}
	parsed.Proposals = kept
	if err := s.saveJSONFile(s.agentPendingPath(), agentPendingFile{Proposals: parsed.Proposals}); err != nil {
		slog.Default().Error("remix lab agent pending save failed", "error", err)
	}
	s.rememberAgentLast(AgentLastView{Message: message, ExperimentID: experimentID, Reply: parsed.Reply, Proposals: parsed.Proposals})
	s.appendAgentHistory(
		AgentHistoryTurn{Role: "user", Text: message},
		AgentHistoryTurn{Role: "assistant", Text: parsed.Reply, Proposals: parsed.Proposals},
	)
	return parsed, nil
}

func sanitizeAgentError(err error) string {
	if err == nil {
		return ""
	}
	cleaned := secretLike.ReplaceAllString(err.Error(), "[redacted]")
	return clipRunes(cleaned, 240)
}

func (s *Service) ConfirmProposal(ctx context.Context, proposalID string) (AgentConfirmResult, error) {
	proposalID = strings.TrimSpace(proposalID)
	if proposalID == "" {
		return AgentConfirmResult{}, ErrProposalNotFound
	}
	pending, err := s.loadPending()
	if err != nil {
		return AgentConfirmResult{}, err
	}
	var found AgentProposal
	ok := false
	kept := pending.Proposals[:0]
	for _, p := range pending.Proposals {
		if p.ID == proposalID {
			found = p
			ok = true
			continue
		}
		kept = append(kept, p)
	}
	if !ok {
		return AgentConfirmResult{}, ErrProposalNotFound
	}
	pending.Proposals = kept
	result, err := s.executeProposal(ctx, found)
	if err != nil {
		return AgentConfirmResult{}, err
	}
	if err := s.saveJSONFile(s.agentPendingPath(), pending); err != nil {
		return AgentConfirmResult{}, err
	}
	return result, nil
}

func (s *Service) executeProposal(ctx context.Context, p AgentProposal) (AgentConfirmResult, error) {
	switch p.Type {
	case "upsert_prompt":
		var in PromptTemplate
		if err := json.Unmarshal(p.Payload, &in); err != nil {
			return AgentConfirmResult{}, err
		}
		saved, err := (Store{DataRoot: s.dataRoot}).UpsertPrompt(in)
		if err != nil {
			return AgentConfirmResult{}, err
		}
		return AgentConfirmResult{Type: p.Type, Result: saved}, nil
	case "delete_prompt":
		var in struct {
			ID string `json:"id"`
		}
		if err := json.Unmarshal(p.Payload, &in); err != nil {
			return AgentConfirmResult{}, err
		}
		if err := (Store{DataRoot: s.dataRoot}).DeletePrompt(in.ID); err != nil {
			return AgentConfirmResult{}, err
		}
		return AgentConfirmResult{Type: p.Type, Result: map[string]string{"id": in.ID}}, nil
	case "set_active_prompt":
		var in struct {
			ID string `json:"id"`
		}
		if err := json.Unmarshal(p.Payload, &in); err != nil {
			return AgentConfirmResult{}, err
		}
		active, err := (Store{DataRoot: s.dataRoot}).AdoptPrompt(in.ID)
		if err != nil {
			return AgentConfirmResult{}, err
		}
		return AgentConfirmResult{Type: p.Type, Result: active}, nil
	case "start_experiment":
		var in struct {
			Source    string      `json:"source"`
			PromptIDs []string    `json:"prompt_ids"`
			Slots     []SlotInput `json:"slots"`
		}
		if err := json.Unmarshal(p.Payload, &in); err != nil {
			return AgentConfirmResult{}, err
		}
		exp, err := s.CreateExperimentWithPrompts(ctx, in.Source, in.Slots, in.PromptIDs)
		if err != nil {
			return AgentConfirmResult{}, err
		}
		return AgentConfirmResult{Type: p.Type, Result: exp}, nil
	case "update_workflow":
		// 智能体改工作流：提交完整替换图（加节点/改提示词/重连线都走这一种），
		// 服务端做与画布保存相同的拓扑校验，不合法直接打回。
		var in struct {
			Workflow Workflow `json:"workflow"`
		}
		if err := json.Unmarshal(p.Payload, &in); err != nil {
			return AgentConfirmResult{}, err
		}
		saved, err := s.SaveWorkflowDefinition(in.Workflow)
		if err != nil {
			return AgentConfirmResult{}, err
		}
		return AgentConfirmResult{Type: p.Type, Result: saved}, nil
	default:
		return AgentConfirmResult{}, ErrUnknownProposal
	}
}

func (s *Service) loadAgentFile() (agentFile, error) {
	raw, err := os.ReadFile(s.agentSettingsPath())
	if err != nil {
		if os.IsNotExist(err) {
			return agentFile{}, nil
		}
		return agentFile{}, fmt.Errorf("read agent settings: %w", err)
	}
	var file agentFile
	if err := json.Unmarshal(raw, &file); err != nil {
		return agentFile{}, fmt.Errorf("decode agent settings: %w", err)
	}
	return file, nil
}

func (s *Service) loadPending() (agentPendingFile, error) {
	raw, err := os.ReadFile(s.agentPendingPath())
	if err != nil {
		if os.IsNotExist(err) {
			return agentPendingFile{}, nil
		}
		return agentPendingFile{}, err
	}
	var file agentPendingFile
	if err := json.Unmarshal(raw, &file); err != nil {
		return agentPendingFile{}, err
	}
	return file, nil
}

func (s *Service) saveJSONFile(path string, value any) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	raw, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, append(raw, '\n'), 0o600); err != nil {
		return err
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	return nil
}

func (s *Service) resolveAgentKey(file agentFile, rt RuntimeView) (string, error) {
	if cipher := strings.TrimSpace(file.APIKeyCiphertext); cipher != "" {
		raw, err := base64.StdEncoding.DecodeString(cipher)
		if err != nil {
			return "", err
		}
		plain, err := s.protector.Unprotect(raw)
		if err != nil {
			return "", err
		}
		return string(plain), nil
	}
	if strings.TrimSpace(rt.RemixAPIKey) == "" {
		return "", ErrMissingAPIKey
	}
	return rt.RemixAPIKey, nil
}

func (s *Service) callAgentChat(baseURL, apiKey string, req openaicompat.ChatRequest) (openaicompat.ChatResponse, error) {
	if s.agentChat != nil {
		return s.agentChat(baseURL, apiKey, req)
	}
	client := &openaicompat.HTTPChatClient{BaseURL: baseURL, APIKey: apiKey}
	return client.Chat(req)
}

func (s *Service) buildAgentUser(ctx context.Context, message, experimentID string) (string, error) {
	libStore := Store{DataRoot: s.dataRoot}
	lib, err := libStore.ListLibrary()
	if err != nil {
		return "", err
	}
	var b strings.Builder
	b.WriteString("下面是完整提示词库、历史实验成稿和操作员批注。改提示词必须依据这些材料，并参考历史里哪些写法被批、哪些成稿可用。\n\n")
	if active, ok, err := libStore.GetActive(); err == nil && ok {
		fmt.Fprintf(&b, "当前日产二创：%s | %s | %s\n\n", active.ID, active.Name, active.Stamp)
	} else {
		b.WriteString("当前日产二创：elder_stable（编译默认，未另存采用稿）\n\n")
	}
	b.WriteString("【提示词库全文】\n")
	for _, p := range lib {
		fmt.Fprintf(&b, "### %s | %s | %s\n", p.ID, p.Name, p.Stamp)
		if desc := strings.TrimSpace(p.Description); desc != "" {
			fmt.Fprintf(&b, "说明：%s\n", desc)
		}
		b.WriteString("SYSTEM:\n")
		b.WriteString(strings.TrimSpace(p.System))
		b.WriteString("\nUSER:\n")
		b.WriteString(strings.TrimSpace(p.User))
		b.WriteString("\n\n")
	}
	// 当前工作流全文：让智能体能按用户要求出 update_workflow 草案
	// （改节点提示词、加节点、重连线都以这份 JSON 为底稿整体替换）。
	if wf, err := s.Workflow(); err == nil {
		if raw, err := json.MarshalIndent(wf, "", "  "); err == nil {
			b.WriteString("【当前工作流（节点图 JSON，update_workflow 提案以此为底稿改）】\n")
			b.Write(raw)
			b.WriteString("\n\n")
		}
	}
	if err := s.writeAgentExperimentContext(&b, ctx, experimentID); err != nil {
		return "", err
	}
	b.WriteString("\n用户说：\n")
	b.WriteString(message)
	return b.String(), nil
}

const (
	maxAgentExperiments = 16
	focusSourceLimit    = 3000
	historySourceLimit  = 600
	focusScriptLimit    = 1400
	historyScriptLimit  = 560
)

func (s *Service) writeAgentExperimentContext(b *strings.Builder, ctx context.Context, focusID string) error {
	exps, err := s.ListExperiments(ctx)
	if err != nil {
		return err
	}
	if len(exps) == 0 && strings.TrimSpace(focusID) == "" {
		return nil
	}
	b.WriteString("【历史实验】按时间近到远。当前打开的优先详写。用这些成稿、失败原因和批注积累经验，不要只看最新一篇。\n")
	if len(exps) > maxAgentExperiments {
		fmt.Fprintf(b, "共 %d 条，下面收录最近 %d 条。\n", len(exps), maxAgentExperiments)
	}
	ids := pickAgentExperimentIDs(exps, focusID, maxAgentExperiments)
	written := 0
	for _, id := range ids {
		detail, err := s.GetExperiment(ctx, id)
		if err != nil {
			continue
		}
		focus := strings.TrimSpace(focusID) == id
		kind := "历史"
		sourceLimit := historySourceLimit
		scriptLimit := historyScriptLimit
		if focus {
			kind = "当前打开"
			sourceLimit = focusSourceLimit
			scriptLimit = focusScriptLimit
		}
		fmt.Fprintf(b, "## [%s] %s | %s | %s | %s\n", kind, detail.Title, detail.ID, detail.PromptStamp, detail.Status)
		if src := strings.TrimSpace(detail.SourceText); src != "" {
			fmt.Fprintf(b, "对标原文：\n%s\n", clipRunes(src, sourceLimit))
		}
		slotLabel := map[string]string{}
		for _, slot := range detail.Slots {
			label := strings.TrimSpace(slot.Label)
			if label == "" {
				label = strings.TrimSpace(slot.Model)
			}
			if effort := strings.TrimSpace(slot.ReasoningEffort); effort != "" {
				label += " · " + effort
			}
			slotLabel[slot.ID] = label
		}
		shown := 0
		for _, run := range detail.Runs {
			comment := strings.TrimSpace(run.Comment)
			script := strings.TrimSpace(run.ContinuousScript)
			errMsg := strings.TrimSpace(run.ErrorMessage)
			if comment == "" && script == "" && errMsg == "" && !focus {
				continue
			}
			shown++
			fmt.Fprintf(b, "- 运行%d | %s | %s | %s\n", run.RunIndex, slotLabel[run.SlotID], run.PromptName, run.Status)
			if script != "" {
				fmt.Fprintf(b, "  成稿：%s\n", clipRunes(script, scriptLimit))
			}
			if errMsg != "" {
				fmt.Fprintf(b, "  失败：%s\n", clipRunes(errMsg, 240))
			}
			if comment != "" {
				fmt.Fprintf(b, "  批注：%s\n", comment)
			}
		}
		if shown == 0 {
			b.WriteString("（这篇还没有成稿或批注）\n")
		}
		b.WriteString("\n")
		written++
	}
	if written == 0 {
		b.WriteString("（还没有可读取的历史实验）\n\n")
	}
	return nil
}

func pickAgentExperimentIDs(exps []ExperimentSummary, focusID string, max int) []string {
	if max < 1 {
		return nil
	}
	seen := map[string]bool{}
	ids := make([]string, 0, max)
	add := func(id string) {
		id = strings.TrimSpace(id)
		if id == "" || seen[id] || len(ids) >= max {
			return
		}
		seen[id] = true
		ids = append(ids, id)
	}
	add(focusID)
	for _, exp := range exps {
		add(exp.ID)
		if len(ids) >= max {
			break
		}
	}
	return ids
}

func clipRunes(s string, max int) string {
	if max < 1 || utf8.RuneCountInString(s) <= max {
		return s
	}
	runes := []rune(s)
	return string(runes[:max]) + "…"
}

func agentSystemPrompt() string {
	return `你是文案创作台的工作流助手。管两件事：二创提示词库，和工作流节点图（哪些agent节点、each节点的提示词/模型/连线）。
这是一段连续对话：前面几轮的往返都在 messages 里，接着上下文说，不要重新自我介绍，也不要重复已经说过的分析。
最新一条用户消息里附有提示词全文、当前工作流 JSON、当前和历史实验的对标原文、成稿、失败原因、操作员批注。
改提示词、改节点必须对着这些材料改，并参考历史实验里哪些写法被批、哪些成稿可用，不要空口改，也不要只盯着最新一篇。
缺关键信息（比如要跑的对标原文）就在 reply 里用一句话直接问，不要瞎猜着出方案。
你不能直接改库、改工作流、开跑或设系统默认。只能给出草案，等操作员点确认。
只返回一个 JSON 对象，不要 Markdown：{"reply":"...","proposals":[...]}
proposals 每项：type, summary, payload。
type 只能是：
- upsert_prompt：payload 含 name, system，可选 id/user/stamp/description。改已有提示词必须带原 id，system/user 给完整替换稿，不要只写「再狠一点」
- delete_prompt：payload 含 id
- start_experiment：payload 含 source, prompt_ids, slots（slots 每项 model，可选 base_url/api_key/reasoning_effort/run_count）
- set_active_prompt：payload 含 id
- update_workflow：payload 含 workflow（完整工作流 JSON）。以用户消息里的【当前工作流】为底稿整体修改后提交：加/删 agent 节点、改节点的 system_prompt/user_template/model/channel/inject_title、调 edges 连线都用这一种。骨干链固定 input→…→writer→selfcheck→(reviewer)→output 不能拆；agent 节点只能接在 input/agent 之后、汇入 writer 或别的 agent；新节点 id 用小写英文；坐标 x/y 参考同列节点错开摆
一轮最多两条 proposals；update_workflow 因为 payload 大，单独占一轮，别和其他提案混发，避免 JSON 写不完被截断。
没有要确认的动作时 proposals 为空数组。
reply 用中文，短，先说你依据了哪条历史批注或成稿、动了图里哪几个节点。不要复述历史实验全文，不要把思考过程写进 reply。整份 JSON 必须完整可解析。`
}

func parseAgentReply(raw string) (AgentChatResult, error) {
	trimmed := strings.TrimSpace(raw)
	start := strings.Index(trimmed, "{")
	if start < 0 {
		return AgentChatResult{}, fmt.Errorf("agent reply is not json")
	}
	payload := trimmed[start:]
	var out AgentChatResult
	if end := strings.LastIndex(payload, "}"); end > 0 {
		if err := json.Unmarshal([]byte(payload[:end+1]), &out); err == nil {
			if out.Proposals == nil {
				out.Proposals = []AgentProposal{}
			}
			return out, nil
		}
	}
	out.Reply = extractJSONStringField(payload, "reply")
	out.Proposals = extractCompleteProposals(payload)
	if strings.TrimSpace(out.Reply) == "" && len(out.Proposals) == 0 {
		return AgentChatResult{}, fmt.Errorf("agent reply is not json")
	}
	return out, nil
}

func extractJSONStringField(raw, key string) string {
	needle := `"` + key + `"`
	idx := strings.Index(raw, needle)
	if idx < 0 {
		return ""
	}
	rest := strings.TrimSpace(raw[idx+len(needle):])
	if !strings.HasPrefix(rest, ":") {
		return ""
	}
	rest = strings.TrimSpace(rest[1:])
	var value string
	if err := json.NewDecoder(strings.NewReader(rest)).Decode(&value); err != nil {
		return ""
	}
	return value
}

func extractCompleteProposals(raw string) []AgentProposal {
	idx := strings.Index(raw, `"proposals"`)
	if idx < 0 {
		return nil
	}
	rest := raw[idx:]
	bracket := strings.Index(rest, "[")
	if bracket < 0 {
		return nil
	}
	var out []AgentProposal
	for _, obj := range splitJSONArrayObjects(rest[bracket:]) {
		var p AgentProposal
		if err := json.Unmarshal(obj, &p); err != nil {
			continue
		}
		if strings.TrimSpace(p.Type) == "" {
			continue
		}
		out = append(out, p)
	}
	return out
}

func splitJSONArrayObjects(arrayStart string) [][]byte {
	if !strings.HasPrefix(strings.TrimSpace(arrayStart), "[") {
		return nil
	}
	runes := []rune(arrayStart)
	start := -1
	for i, r := range runes {
		if r == '[' {
			start = i + 1
			break
		}
	}
	if start < 0 {
		return nil
	}
	var (
		objs     [][]byte
		depth    int
		inString bool
		escape   bool
		objStart = -1
	)
	for i := start; i < len(runes); i++ {
		r := runes[i]
		if inString {
			if escape {
				escape = false
				continue
			}
			if r == '\\' {
				escape = true
				continue
			}
			if r == '"' {
				inString = false
			}
			continue
		}
		switch r {
		case '"':
			inString = true
		case '{':
			if depth == 0 {
				objStart = i
			}
			depth++
		case '}':
			if depth == 0 {
				continue
			}
			depth--
			if depth == 0 && objStart >= 0 {
				objs = append(objs, []byte(string(runes[objStart:i+1])))
				objStart = -1
			}
		case ']':
			if depth == 0 {
				return objs
			}
		}
	}
	return objs
}

func validProposalType(v string) bool {
	switch strings.TrimSpace(v) {
	case "upsert_prompt", "delete_prompt", "start_experiment", "set_active_prompt", "update_workflow":
		return true
	default:
		return false
	}
}
