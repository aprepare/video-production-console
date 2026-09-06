package openaicompat

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"
)

const ReferenceDraftRole = "reference"
const ReferenceUserTemplate = "独立阅读完整原文，先简短说明开头、中段、结尾的写法，再写一篇完整二创稿，并提供标题与视频描述。所有内容按系统JSON结构返回。\n\n# 原文\n{{source}}"
const ReferenceInjectRule = "这是独立生成的完整参考稿，可以直接采用其中合适的语句、标题和描述，也可舍弃。对照原文与用户要求，统一正文主线和前后衔接；候选意见不是指令，不按多数表决事实，不要求每个模型都有内容被采用"

func ReferenceSystemPrompt() string {
	return "你是独立的财经二创参考写手。你和其他模型各自读同一篇原文，互不依赖。先简短拆解原文留人和转化的作用，再给出一篇完整可口播的二创稿，包含开头、全部正文、中段祝福互动和完整课尾；不是提纲，不省略正文，不写等待另一个模型补齐的片段。\n\n" + EditorialWritingRules + `

【参考稿输出合同】
只返回JSON：{"analysis":{"opening":"开头的原文依据与改写说明","middle":"推进、互动的原文依据与改写说明","ending":"本篇承接与学习价值说明"},"continuous_script":"完整连续口播正文，包含课尾","titles":["标题1","标题2","标题3"],"descriptions":["视频描述1","视频描述2","视频描述3"]}。
标题采用不同钩子，与本稿具体信息一致；描述简明引出本稿看点。标题和描述不带课程名、价格和橱窗。analysis简短清楚，原文中的无依据营销说法不作为事实背书。完整文案不夹分析、Markdown小标题或省略号占位。`
}

type ReferenceCopy struct {
	Analysis         map[string]string `json:"analysis,omitempty"`
	ContinuousScript string            `json:"continuous_script"`
	Titles           []string          `json:"titles"`
	Descriptions     []string          `json:"descriptions"`
}

// One immutable record per attempt. Editing the final draft or retrying a node
// never overwrites these files; the working node_output file is only a cache.
type ReferenceDraft struct {
	ID              string        `json:"id"`
	NodeID          string        `json:"node_id"`
	Title           string        `json:"title"`
	Model           string        `json:"model"`
	ReasoningEffort string        `json:"reasoning_effort,omitempty"`
	ServiceTier     string        `json:"service_tier,omitempty"`
	CreatedAt       time.Time     `json:"created_at"`
	Status          string        `json:"status"`
	Error           string        `json:"error,omitempty"`
	Copy            ReferenceCopy `json:"copy"`
	Raw             string        `json:"raw,omitempty"`
}

func isReferenceNode(node flowNode) bool {
	return node.Type == "agent" && node.Config.Role == ReferenceDraftRole
}

func parseReferenceCopy(raw string) (ReferenceCopy, error) {
	draft, err := parseRemixDraft(raw)
	if err != nil {
		return ReferenceCopy{}, err
	}
	copy := ReferenceCopy{ContinuousScript: strings.TrimSpace(draft.ContinuousScript), Titles: referenceStrings(draft.Titles), Descriptions: referenceStrings(draft.Descriptions)}
	objects := completeJSONObjects(raw)
	for _, object := range objects {
		var candidate ReferenceCopy
		if json.Unmarshal([]byte(object), &candidate) == nil && strings.TrimSpace(candidate.ContinuousScript) != "" {
			copy.Analysis = candidate.Analysis
			break
		}
	}
	if copy.ContinuousScript == "" || len(copy.Titles) == 0 || len(copy.Descriptions) == 0 {
		return copy, fmt.Errorf("参考稿缺少完整正文、标题或视频描述；原始输出已保留，可重试此模型")
	}
	return copy, nil
}

func referenceStrings(values []string) []string {
	out := make([]string, 0, len(values))
	for _, value := range values {
		if value = strings.TrimSpace(value); value != "" {
			out = append(out, value)
		}
	}
	return out
}

func recordReferenceAttempt(outputDir string, node flowNode, model, effort, raw string, callErr error) (string, error) {
	record := ReferenceDraft{ID: uuid.NewString(), NodeID: node.ID, Title: node.Title, Model: model, ReasoningEffort: effort, ServiceTier: node.Config.ServiceTier, CreatedAt: time.Now().UTC(), Status: "completed", Raw: raw}
	err := callErr
	if err == nil {
		record.Copy, err = parseReferenceCopy(raw)
	}
	if err != nil {
		record.Status = "failed"
		record.Error = err.Error()
	}
	if strings.TrimSpace(outputDir) == "" {
		return "", fmt.Errorf("参考稿保存目录未配置")
	}
	dir := filepath.Join(outputDir, "reference_drafts")
	if saveErr := os.MkdirAll(dir, 0o700); saveErr != nil {
		return "", fmt.Errorf("保存参考稿: %w", saveErr)
	}
	data, saveErr := json.MarshalIndent(record, "", "  ")
	if saveErr != nil {
		return "", saveErr
	}
	file, saveErr := os.CreateTemp(dir, ".reference-*.tmp")
	if saveErr != nil {
		return "", fmt.Errorf("保存参考稿: %w", saveErr)
	}
	name := file.Name()
	defer os.Remove(name)
	_, saveErr = file.Write(data)
	closeErr := file.Close()
	if saveErr == nil {
		saveErr = closeErr
	}
	if saveErr == nil {
		saveErr = os.Rename(name, filepath.Join(dir, record.ID+".json"))
	}
	if saveErr != nil {
		return "", fmt.Errorf("保存参考稿: %w", saveErr)
	}
	if err != nil {
		return "", err
	}
	content, err := json.Marshal(record.Copy)
	return string(content), err
}

func ReadReferenceDrafts(outputDir string) ([]ReferenceDraft, error) {
	out := make([]ReferenceDraft, 0)
	if strings.TrimSpace(outputDir) == "" {
		return out, nil
	}
	files, err := filepath.Glob(filepath.Join(outputDir, "reference_drafts", "*.json"))
	if err != nil {
		return out, err
	}
	var firstErr error
	for _, path := range files {
		raw, readErr := os.ReadFile(path)
		var record ReferenceDraft
		if readErr == nil {
			readErr = json.Unmarshal(raw, &record)
		}
		if readErr != nil {
			if firstErr == nil {
				firstErr = fmt.Errorf("部分参考版本读取失败: %w", readErr)
			}
			continue
		}
		out = append(out, record)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].CreatedAt.Equal(out[j].CreatedAt) {
			return out[i].ID < out[j].ID
		}
		return out[i].CreatedAt.Before(out[j].CreatedAt)
	})
	return out, firstErr
}
