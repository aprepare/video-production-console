package remixproducer

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"mime/multipart"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	"video-production-console/internal/store"
)

// 生产驱动器：二创定稿确认（或全自动放行）后，通过本机回环 HTTP 调用控制台
// 自己的 API，把现有混剪链路按步串起来：建项目 → 传发布包 → 口播稿任务 →
// （可选字幕关键词）→ 配音 → 混剪任务 → 剪映草稿注册。每步状态入库，控制台
// 重启后 ResumeAll 续跑。混剪系统本身一行不动——驱动器只当指挥官。

const (
	StepConfirm   = "confirm"
	StepProject   = "project"
	StepSpoken    = "spoken"
	StepCaptions  = "captions"
	StepNarration = "narration"
	StepMontage   = "montage"
	StepDone      = "done"
)

// ProductionSteps 是生产段的固定顺序（画布展示与状态推导共用）。
var ProductionSteps = []string{StepProject, StepSpoken, StepCaptions, StepNarration, StepMontage}

// 各步的默认任务提示词：画布上生产节点抽屉展示的就是这些；工作流的
// production 配置可以按需覆盖。
const (
	DefaultSpokenPrompt   = "按一句一行、每行不超过九个字，把当前连续文案切成口播稿。"
	DefaultCaptionsPrompt = "为口播稿每一行挑出值得放大强调的警示词和数字。"
	DefaultMontagePrompt  = "执行风景混剪，产出可编辑的剪映草稿。"
)

// Config 是生产段在工作流里的可编辑配置（存在工作流 JSON 的 production 字段，
// 随实验快照冻结）。
type Config struct {
	CaptionsDisabled bool     `json:"captions_disabled,omitempty"`
	SpokenPrompt     string   `json:"spoken_prompt,omitempty"`
	CaptionsPrompt   string   `json:"captions_prompt,omitempty"`
	MontagePrompt    string   `json:"montage_prompt,omitempty"`
	SpokenModel      string   `json:"spoken_model,omitempty"`
	SpokenEffort     string   `json:"spoken_effort,omitempty"`
	MontageModel     string   `json:"montage_model,omitempty"`
	MontageEffort    string   `json:"montage_effort,omitempty"`
	NarrationVoiceID string   `json:"narration_voice_id,omitempty"`
	NarrationModel   string   `json:"narration_model,omitempty"`
	NarrationEmotion string   `json:"narration_emotion,omitempty"`
	NarrationSpeed   *float64 `json:"narration_speed,omitempty"`
	NarrationVolume  *float64 `json:"narration_volume,omitempty"`
	NarrationPitch   *int     `json:"narration_pitch,omitempty"`
}

// ParseConfig 从实验的工作流快照 JSON 里取生产段配置。没有 production 字段
// 时默认关掉字幕关键词（和画布「默认不显示关键词节点」对齐）；显式写出的
// 对象按字段原样生效。
func ParseConfig(workflowJSON string) Config {
	trimmed := strings.TrimSpace(workflowJSON)
	if trimmed == "" {
		return Config{CaptionsDisabled: true}
	}
	var raw map[string]json.RawMessage
	if err := json.Unmarshal([]byte(trimmed), &raw); err != nil {
		return Config{CaptionsDisabled: true}
	}
	prodRaw, ok := raw["production"]
	if !ok || len(bytes.TrimSpace(prodRaw)) == 0 || string(bytes.TrimSpace(prodRaw)) == "null" {
		return Config{CaptionsDisabled: true}
	}
	var cfg Config
	_ = json.Unmarshal(prodRaw, &cfg)
	return cfg
}

func withTaskModel(body map[string]string, model, effort string) map[string]string {
	if m := strings.TrimSpace(model); m != "" {
		body["model"] = m
	}
	if e := strings.TrimSpace(effort); e != "" {
		body["reasoning_effort"] = e
	}
	return body
}

func (c Config) narrationOverride() map[string]any {
	out := map[string]any{}
	if v := strings.TrimSpace(c.NarrationVoiceID); v != "" {
		out["voice_id"] = v
	}
	if v := strings.TrimSpace(c.NarrationModel); v != "" {
		out["model"] = v
	}
	if v := strings.TrimSpace(c.NarrationEmotion); v != "" {
		out["emotion"] = v
	}
	if c.NarrationSpeed != nil {
		out["speed"] = *c.NarrationSpeed
	}
	if c.NarrationVolume != nil {
		out["volume"] = *c.NarrationVolume
	}
	if c.NarrationPitch != nil {
		out["pitch"] = *c.NarrationPitch
	}
	return out
}

func (c Config) EffectiveSpokenPrompt() string {
	if p := strings.TrimSpace(c.SpokenPrompt); p != "" {
		return p
	}
	return DefaultSpokenPrompt
}

func (c Config) EffectiveCaptionsPrompt() string {
	if p := strings.TrimSpace(c.CaptionsPrompt); p != "" {
		return p
	}
	return DefaultCaptionsPrompt
}

func (c Config) EffectiveMontagePrompt() string {
	if p := strings.TrimSpace(c.MontagePrompt); p != "" {
		return p
	}
	return DefaultMontagePrompt
}

const (
	taskPollInterval = 3 * time.Second
	spokenTimeout    = 20 * time.Minute
	montageTimeout   = 45 * time.Minute
	narrationTimeout = 8 * time.Minute
)

type Driver struct {
	repo   *store.RemixLabRepository
	base   string
	token  string
	client *http.Client
	logger *slog.Logger

	mu     sync.Mutex
	active map[string]bool
}

// New 构造驱动器。listenAddr 是控制台监听地址（如 127.0.0.1:2030 或 :2030），
// 回环调用时通配 host 一律换成 127.0.0.1。
func New(repo *store.RemixLabRepository, listenAddr, token string) *Driver {
	return &Driver{
		repo:   repo,
		base:   "http://" + loopbackHostPort(listenAddr),
		token:  token,
		client: &http.Client{Timeout: 90 * time.Second},
		logger: slog.Default().With("component", "remixproducer"),
		active: map[string]bool{},
	}
}

func loopbackHostPort(listenAddr string) string {
	host, port, err := net.SplitHostPort(strings.TrimSpace(listenAddr))
	if err != nil {
		return "127.0.0.1:2030"
	}
	if host == "" || host == "0.0.0.0" || host == "::" {
		host = "127.0.0.1"
	}
	return net.JoinHostPort(host, port)
}

// Launch 异步驱动一条生产（幂等：同一 run 已在驱动中则忽略）。
func (d *Driver) Launch(runID string) {
	d.mu.Lock()
	if d.active[runID] {
		d.mu.Unlock()
		return
	}
	d.active[runID] = true
	d.mu.Unlock()
	go func() {
		defer func() {
			d.mu.Lock()
			delete(d.active, runID)
			d.mu.Unlock()
		}()
		d.run(runID)
	}()
}

// ResumeAll 重启后把 status=running 的生产接着跑（任务系统本身持久，续跑
// 只是重新轮询/重发当前步）。
func (d *Driver) ResumeAll() {
	ctx := context.Background()
	records, err := d.repo.ListRunningProductions(ctx)
	if err != nil {
		d.logger.Error("resume productions list failed", "error", err)
		return
	}
	for _, rec := range records {
		d.logger.Info("resume production", "run_id", rec.RunID, "step", rec.Step)
		d.Launch(rec.RunID)
	}
}

func (d *Driver) run(runID string) {
	ctx := context.Background()
	rec, err := d.repo.GetProduction(ctx, runID)
	if err != nil {
		d.logger.Error("production load failed", "run_id", runID, "error", err)
		return
	}
	if rec.Status != "running" {
		return
	}
	// 生产段配置随实验的工作流快照冻结：跳字幕、换提示词按当时的图执行。
	cfg := Config{}
	if exp, _, _, expErr := d.repo.GetExperiment(ctx, rec.ExperimentID); expErr == nil {
		cfg = ParseConfig(exp.WorkflowJSON)
	}
	save := func() {
		rec.UpdatedAt = time.Now().UTC()
		if err := d.repo.UpdateProduction(ctx, rec); err != nil {
			d.logger.Error("production save failed", "run_id", runID, "error", err)
		}
	}
	fail := func(step string, cause error) {
		rec.Status = "failed"
		rec.Error = fmt.Sprintf("%s：%s", stepLabel(step), truncate(cause.Error(), 500))
		save()
		d.logger.Error("production failed", "run_id", runID, "step", step, "error", cause)
	}

	for {
		switch rec.Step {
		case "", StepConfirm:
			rec.Step = StepProject
			save()
		case StepProject:
			if err := d.stepProject(ctx, &rec); err != nil {
				fail(StepProject, err)
				return
			}
			rec.Step = StepSpoken
			save()
		case StepSpoken:
			if err := d.stepSpoken(ctx, &rec, cfg); err != nil {
				fail(StepSpoken, err)
				return
			}
			rec.Step = StepCaptions
			save()
		case StepCaptions:
			// 字幕关键词是可选增强：工作流里删掉了就整步跳过；失败也不拦
			// 流程，混剪回落本地词表。
			if !cfg.CaptionsDisabled {
				d.stepCaptions(ctx, &rec, cfg)
			}
			rec.Step = StepNarration
			save()
		case StepNarration:
			if err := d.stepNarration(ctx, &rec, cfg); err != nil {
				fail(StepNarration, err)
				return
			}
			rec.Step = StepMontage
			save()
		case StepMontage:
			if err := d.stepMontage(ctx, &rec, cfg); err != nil {
				fail(StepMontage, err)
				return
			}
			rec.Step = StepDone
			rec.Status = "completed"
			rec.Error = ""
			save()
			d.logger.Info("production completed", "run_id", runID, "project_id", rec.ProjectID)
			return
		case StepDone:
			rec.Status = "completed"
			save()
			return
		default:
			fail(rec.Step, fmt.Errorf("未知的生产步骤 %q", rec.Step))
			return
		}
	}
}

func stepLabel(step string) string {
	switch step {
	case StepProject:
		return "建项目导入失败"
	case StepSpoken:
		return "口播稿失败"
	case StepCaptions:
		return "字幕关键词失败"
	case StepNarration:
		return "配音失败"
	case StepMontage:
		return "混剪失败"
	default:
		return "生产失败"
	}
}

// —— 各步实现 ——

func (d *Driver) stepProject(ctx context.Context, rec *store.RemixLabProductionRecord) error {
	if strings.TrimSpace(rec.ProjectID) != "" {
		return nil
	}
	run, err := d.repo.GetRun(ctx, rec.RunID)
	if err != nil {
		return err
	}
	exp, _, _, err := d.repo.GetExperiment(ctx, rec.ExperimentID)
	if err != nil {
		return err
	}
	title := projectTitle(run.PackageJSON, exp.Title)

	var project struct {
		ID string `json:"id"`
	}
	if err := d.doJSON(ctx, http.MethodPost, "/api/projects",
		map[string]string{"account_id": rec.AccountID, "title": title}, &project); err != nil {
		return err
	}
	if strings.TrimSpace(project.ID) == "" {
		return fmt.Errorf("项目创建返回缺少 id")
	}

	payload := strings.TrimSpace(run.PackageJSON)
	if payload == "" {
		encoded, encodeErr := json.Marshal(map[string]string{"continuous_script": run.ContinuousScript})
		if encodeErr != nil {
			return encodeErr
		}
		payload = string(encoded)
	}
	if err := d.uploadScript(ctx, project.ID, payload); err != nil {
		return err
	}
	rec.ProjectID = project.ID
	return nil
}

func projectTitle(packageJSON, fallback string) string {
	var pkg struct {
		ShortTitles []string `json:"short_titles"`
		Titles      []string `json:"titles"`
	}
	_ = json.Unmarshal([]byte(packageJSON), &pkg)
	title := ""
	if len(pkg.ShortTitles) > 0 {
		title = strings.TrimSpace(pkg.ShortTitles[0])
	}
	if title == "" && len(pkg.Titles) > 0 {
		title = strings.TrimSpace(pkg.Titles[0])
	}
	if title == "" {
		title = strings.TrimSpace(fallback)
	}
	runes := []rune(title)
	if len(runes) > 40 {
		title = string(runes[:40])
	}
	return title
}

func (d *Driver) stepSpoken(ctx context.Context, rec *store.RemixLabProductionRecord, cfg Config) error {
	if rec.SpokenTaskID != "" {
		status, _, errMsg, err := d.taskStatus(ctx, rec.SpokenTaskID)
		if err == nil {
			switch status {
			case "completed":
				return nil
			case "failed", "canceled", "cancelled", "interrupted":
				rec.SpokenTaskID = "" // 上一轮挂了，重新起任务
			default:
				return d.waitTask(ctx, rec.SpokenTaskID, spokenTimeout)
			}
			_ = errMsg
		}
	}
	taskID, err := d.createTask(ctx, rec.ProjectID, withTaskModel(map[string]string{
		"account_id": rec.AccountID,
		"type":       "remix",
		"action":     "remix.spoken_lines",
		"prompt":     cfg.EffectiveSpokenPrompt(),
	}, cfg.SpokenModel, cfg.SpokenEffort))
	if err != nil {
		return err
	}
	rec.SpokenTaskID = taskID
	rec.UpdatedAt = time.Now().UTC()
	_ = d.repo.UpdateProduction(ctx, *rec)
	return d.waitTask(ctx, taskID, spokenTimeout)
}

func (d *Driver) stepCaptions(ctx context.Context, rec *store.RemixLabProductionRecord, cfg Config) {
	if d.keywordsHidden(ctx) {
		return
	}
	if rec.CaptionTaskID != "" {
		if status, _, _, err := d.taskStatus(ctx, rec.CaptionTaskID); err == nil && status == "completed" {
			return
		}
	}
	taskID, err := d.createTask(ctx, rec.ProjectID, map[string]string{
		"account_id": rec.AccountID,
		"type":       "remix",
		"action":     "remix.caption_keywords",
		"prompt":     cfg.EffectiveCaptionsPrompt(),
	})
	if err != nil {
		d.logger.Warn("caption task start failed, fallback to local keywords", "run_id", rec.RunID, "error", err)
		return
	}
	rec.CaptionTaskID = taskID
	rec.UpdatedAt = time.Now().UTC()
	_ = d.repo.UpdateProduction(ctx, *rec)
	if err := d.waitTask(ctx, taskID, spokenTimeout); err != nil {
		d.logger.Warn("caption task failed, montage falls back to local keywords", "run_id", rec.RunID, "error", err)
	}
}

func (d *Driver) keywordsHidden(ctx context.Context) bool {
	var view struct {
		Public struct {
			MontageStyle struct {
				KeywordsHidden bool `json:"keywords_hidden"`
			} `json:"montage_style"`
		} `json:"public"`
	}
	if err := d.doJSON(ctx, http.MethodGet, "/api/settings", nil, &view); err != nil {
		return false
	}
	return view.Public.MontageStyle.KeywordsHidden
}

func (d *Driver) stepNarration(ctx context.Context, rec *store.RemixLabProductionRecord, cfg Config) error {
	ready, err := d.narrationReady(ctx, rec.ProjectID)
	if err == nil && ready {
		return nil
	}
	var payload io.Reader
	if ov := cfg.narrationOverride(); len(ov) > 0 {
		raw, err := json.Marshal(ov)
		if err != nil {
			return err
		}
		payload = bytes.NewReader(raw)
	}
	client := &http.Client{Timeout: narrationTimeout}
	req, err := d.newRequest(ctx, http.MethodPost, "/api/projects/"+rec.ProjectID+"/narration", payload)
	if err != nil {
		return err
	}
	if payload != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
	switch {
	case resp.StatusCode == http.StatusCreated:
		return nil
	case resp.StatusCode == http.StatusConflict && strings.Contains(string(raw), "narration_in_progress"):
		// 别处已在配音（比如重试撞上没退场的旧请求）：轮询等它出结果。
		deadline := time.Now().Add(narrationTimeout)
		for time.Now().Before(deadline) {
			time.Sleep(5 * time.Second)
			if ready, err := d.narrationReady(ctx, rec.ProjectID); err == nil && ready {
				return nil
			}
		}
		return fmt.Errorf("等待进行中的配音超时")
	default:
		return fmt.Errorf("配音接口 %d：%s", resp.StatusCode, apiMessage(raw))
	}
}

func (d *Driver) narrationReady(ctx context.Context, projectID string) (bool, error) {
	var detail struct {
		Assets map[string]struct {
			State string `json:"state"`
		} `json:"assets"`
	}
	if err := d.doJSON(ctx, http.MethodGet, "/api/projects/"+projectID, nil, &detail); err != nil {
		return false, err
	}
	narration, ok := detail.Assets["narration"]
	srt, ok2 := detail.Assets["subtitle_srt"]
	return ok && ok2 && narration.State == "ready" && srt.State == "ready", nil
}

func (d *Driver) stepMontage(ctx context.Context, rec *store.RemixLabProductionRecord, cfg Config) error {
	if rec.MontageTaskID != "" {
		status, _, _, err := d.taskStatus(ctx, rec.MontageTaskID)
		if err == nil {
			switch status {
			case "completed":
				return nil
			case "failed", "canceled", "cancelled", "interrupted":
				rec.MontageTaskID = ""
			default:
				return d.waitTask(ctx, rec.MontageTaskID, montageTimeout)
			}
		}
	}
	taskID, err := d.createTask(ctx, rec.ProjectID, withTaskModel(map[string]string{
		"account_id": rec.AccountID,
		"type":       "montage",
		"prompt":     cfg.EffectiveMontagePrompt(),
	}, cfg.MontageModel, cfg.MontageEffort))
	if err != nil {
		return err
	}
	rec.MontageTaskID = taskID
	rec.UpdatedAt = time.Now().UTC()
	_ = d.repo.UpdateProduction(ctx, *rec)
	// montage.execute 任务 completed 即代表剪映草稿注册成功（注册失败任务会是 failed）。
	return d.waitTask(ctx, taskID, montageTimeout)
}

// —— 回环 HTTP 基础设施 ——

func (d *Driver) newRequest(ctx context.Context, method, path string, body io.Reader) (*http.Request, error) {
	req, err := http.NewRequestWithContext(ctx, method, d.base+path, body)
	if err != nil {
		return nil, err
	}
	req.Header.Set("X-Internal-Token", d.token)
	return req, nil
}

func (d *Driver) doJSON(ctx context.Context, method, path string, payload any, out any) error {
	var body io.Reader
	if payload != nil {
		raw, err := json.Marshal(payload)
		if err != nil {
			return err
		}
		body = bytes.NewReader(raw)
	}
	req, err := d.newRequest(ctx, method, path, body)
	if err != nil {
		return err
	}
	if payload != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := d.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("%s %s 返回 %d：%s", method, path, resp.StatusCode, apiMessage(raw))
	}
	if out != nil {
		if err := json.Unmarshal(raw, out); err != nil {
			return fmt.Errorf("解析 %s 响应失败: %w", path, err)
		}
	}
	return nil
}

func (d *Driver) uploadScript(ctx context.Context, projectID, payload string) error {
	var buf bytes.Buffer
	writer := multipart.NewWriter(&buf)
	part, err := writer.CreateFormFile("file", "continuous-script.txt")
	if err != nil {
		return err
	}
	if _, err := part.Write([]byte(payload)); err != nil {
		return err
	}
	if err := writer.Close(); err != nil {
		return err
	}
	req, err := d.newRequest(ctx, http.MethodPost, "/api/projects/"+projectID+"/assets/continuous_script", &buf)
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", writer.FormDataContentType())
	resp, err := d.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("文案写入项目失败 %d：%s", resp.StatusCode, apiMessage(raw))
	}
	return nil
}

// createTask 起任务；已有同类活跃任务时后端返回 200+现有任务，一并接受。
func (d *Driver) createTask(ctx context.Context, projectID string, body map[string]string) (string, error) {
	var task struct {
		ID string `json:"id"`
	}
	if err := d.doJSON(ctx, http.MethodPost, "/api/projects/"+projectID+"/tasks", body, &task); err != nil {
		return "", err
	}
	if strings.TrimSpace(task.ID) == "" {
		return "", fmt.Errorf("任务创建返回缺少 id")
	}
	return task.ID, nil
}

func (d *Driver) taskStatus(ctx context.Context, taskID string) (status, errCode, errMsg string, err error) {
	var task struct {
		Status       string `json:"status"`
		ErrorCode    string `json:"error_code"`
		ErrorMessage string `json:"error_message"`
	}
	if err := d.doJSON(ctx, http.MethodGet, "/api/tasks/"+taskID, nil, &task); err != nil {
		return "", "", "", err
	}
	return task.Status, task.ErrorCode, task.ErrorMessage, nil
}

func (d *Driver) waitTask(ctx context.Context, taskID string, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for {
		status, errCode, errMsg, err := d.taskStatus(ctx, taskID)
		if err != nil {
			return err
		}
		switch status {
		case "completed":
			return nil
		case "failed", "canceled", "cancelled", "interrupted":
			detail := strings.TrimSpace(errMsg)
			if detail == "" {
				detail = errCode
			}
			if detail == "" {
				detail = "任务 " + status
			}
			return fmt.Errorf("%s", detail)
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("等待任务超时（%s）", timeout)
		}
		time.Sleep(taskPollInterval)
	}
}

func apiMessage(raw []byte) string {
	var body struct {
		Message string `json:"message"`
		Code    string `json:"code"`
	}
	if err := json.Unmarshal(raw, &body); err == nil {
		if body.Message != "" {
			return body.Message
		}
		if body.Code != "" {
			return body.Code
		}
	}
	return truncate(strings.TrimSpace(string(raw)), 200)
}

func truncate(text string, limit int) string {
	runes := []rune(text)
	if len(runes) <= limit {
		return text
	}
	return string(runes[:limit]) + "…"
}
