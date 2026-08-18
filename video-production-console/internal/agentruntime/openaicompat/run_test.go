package openaicompat

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestWriterPromptForbidsLineByLineParaphrase(t *testing.T) {
	system := buildWriterPrompt("# skill", PromptStyleRewrite)
	for _, want := range []string{"禁止逐段同义改写", "机器以原文为准", "财富觉醒方法论", "本金乘利率", "原稿的推进顺序不能倒", "#干货分享", "3到4个", "必须从这篇口播长出来"} {
		if !strings.Contains(system, want) {
			t.Fatalf("system missing %q", want)
		}
	}
	for _, forbid := range []string{"第三次换锚", "一百七十万亿", "第三个锚故意不说完", "方便面被外卖抢走", "河的上游"} {
		if strings.Contains(system, forbid) {
			t.Fatalf("rewrite prompt must not hardcode plot %q", forbid)
		}
	}
	user := buildWriterUser(manifestLite{}, "法拍房快堆到四十万套", PromptStyleRewrite)
	if !strings.Contains(user, "不当逐句模板") || !strings.Contains(user, "先从原文锁机器") || !strings.Contains(user, "标题和短标题也必须跟这篇新口播走") || strings.Contains(user, "只换说法和加料，不换题") {
		t.Fatalf("user=%q", user)
	}
}

func TestWriterPromptSkillExcerptDoesNotReinjectFixedPlot(t *testing.T) {
	skillPath := filepath.Join(os.Getenv("USERPROFILE"), ".codex", "skills", "finance-viral-remix", "SKILL.md")
	raw, err := os.ReadFile(skillPath)
	if err != nil {
		t.Skip(err)
	}
	system := buildWriterPrompt(string(raw), PromptStyleRewrite)
	if !strings.Contains(system, "# 补充约束") {
		t.Fatalf("expected skill excerpt in system prompt")
	}
	for _, forbid := range []string{"第三次换锚", "一百七十万亿", "第三个锚", "方便面被外卖抢走", "河的上游", "先发财换锚"} {
		if strings.Contains(system, forbid) {
			t.Fatalf("skill excerpt reintroduced plot %q", forbid)
		}
	}
}

func TestWashPromptKeepsSourceAndOnlyCutsPhrasing(t *testing.T) {
	system := buildWriterPrompt("# skill\n禁止照抄金句", PromptStyleWash)
	for _, want := range []string{"按洗稿来", "切成适合口播的短段", "轻微换词", "原稿的推进顺序不能倒", "本金乘利率", "#干货分享"} {
		if !strings.Contains(system, want) {
			t.Fatalf("wash system missing %q", want)
		}
	}
	for _, forbid := range []string{"禁止逐段同义改写", "禁止照抄", "补充约束", "不当逐句模板", "第三次换锚", "一百七十万亿", "先发财换锚"} {
		if strings.Contains(system, forbid) {
			t.Fatalf("wash system has %q: %s", forbid, system)
		}
	}
	user := buildWriterUser(manifestLite{}, "法拍房快堆到四十万套", PromptStyleWash)
	if !strings.Contains(user, "不要另写一篇") || strings.Contains(user, "不当逐句模板") {
		t.Fatalf("wash user=%q", user)
	}
}

func TestNormalizePromptStyle(t *testing.T) {
	got, err := NormalizePromptStyle("")
	if err != nil || got != PromptStyleRewrite {
		t.Fatalf("empty=%q err=%v", got, err)
	}
	got, err = NormalizePromptStyle("WASH")
	if err != nil || got != PromptStyleWash {
		t.Fatalf("wash=%q err=%v", got, err)
	}
	if _, err := NormalizePromptStyle("paraphrase"); err == nil {
		t.Fatal("expected invalid style")
	}
}

func TestRunWritesEnvelopeFromModelText(t *testing.T) {
	root := t.TempDir()
	skillRoot := filepath.Join(root, "skill")
	_ = os.MkdirAll(skillRoot, 0o755)
	_ = os.WriteFile(filepath.Join(skillRoot, "SKILL.md"), []byte("# skill"), 0o644)
	sourcePath := filepath.Join(root, "source.txt")
	_ = os.WriteFile(sourcePath, []byte("又一批人要发财了。人民币要第三次换锚。"), 0o644)

	outputDir := filepath.Join(root, "output")
	manifestPath := filepath.Join(root, "task_manifest.json")
	manifest := map[string]any{
		"task_id": "task-openai-1", "action": "remix.standard", "skill": "finance-viral-remix",
		"output_dir": outputDir,
		"inputs":     []any{map[string]any{"type": "source_script", "role": "primary_source", "path": sourcePath}},
	}
	raw, _ := json.Marshal(manifest)
	_ = os.WriteFile(manifestPath, raw, 0o644)
	last := filepath.Join(root, "output-last-message.json")

	client := &textClient{content: remixJSON}
	if err := Run(Options{
		ManifestPath:      manifestPath,
		SkillRoot:         skillRoot,
		OutputLastMessage: last,
		Model:             "test-model",
		BaseURL:           "http://example.invalid/v1",
		APIKey:            "test-key",
		Client:            client,
	}); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !client.streamed {
		t.Fatal("remix request must stream and omit tools")
	}
	body, err := os.ReadFile(last)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(body), `"completed"`) || !strings.Contains(string(body), "task-openai-1") {
		t.Fatalf("envelope=%s", body)
	}
	if _, err := os.Stat(filepath.Join(outputDir, "continuous_script.txt")); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(outputDir, "result.json")); err != nil {
		t.Fatal(err)
	}
}

func TestRunSendsWashPromptWhenManifestStyleIsWash(t *testing.T) {
	root := t.TempDir()
	skillRoot := filepath.Join(root, "skill")
	_ = os.MkdirAll(skillRoot, 0o755)
	_ = os.WriteFile(filepath.Join(skillRoot, "SKILL.md"), []byte("# skill\n禁止照抄金句"), 0o644)
	sourcePath := filepath.Join(root, "source.txt")
	_ = os.WriteFile(sourcePath, []byte("又一批人要发财了。人民币要第三次换锚。"), 0o644)
	outputDir := filepath.Join(root, "output")
	manifestPath := filepath.Join(root, "task_manifest.json")
	raw, _ := json.Marshal(map[string]any{
		"task_id": "task-wash-1", "action": "remix.standard", "skill": "finance-viral-remix",
		"output_dir":          outputDir,
		"inputs":              []any{map[string]any{"type": "source_script", "role": "primary_source", "path": sourcePath}},
		"non_secret_settings": map[string]any{"remix_prompt_style": "wash"},
	})
	_ = os.WriteFile(manifestPath, raw, 0o644)
	client := &textClient{content: remixJSON}
	if err := Run(Options{
		ManifestPath:      manifestPath,
		SkillRoot:         skillRoot,
		OutputLastMessage: filepath.Join(root, "last.json"),
		Model:             "test-model",
		BaseURL:           "http://example.invalid/v1",
		APIKey:            "test-key",
		Client:            client,
	}); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(client.last.Messages) < 2 {
		t.Fatalf("messages=%d", len(client.last.Messages))
	}
	system := client.last.Messages[0].Content
	user := client.last.Messages[1].Content
	if !strings.Contains(system, "按洗稿来") || strings.Contains(system, "禁止逐段同义改写") || strings.Contains(system, "补充约束") {
		t.Fatalf("system=%s", system)
	}
	if !strings.Contains(user, "不要另写一篇") || strings.Contains(user, "不当逐句模板") {
		t.Fatalf("user=%s", user)
	}
}

func TestRunWritesFilesWhenModelReturnsPlainScript(t *testing.T) {
	root := t.TempDir()
	skillRoot := filepath.Join(root, "skill")
	_ = os.MkdirAll(skillRoot, 0o755)
	sourcePath := filepath.Join(root, "source.txt")
	_ = os.WriteFile(sourcePath, []byte("又一批人要发财了。"), 0o644)
	outputDir := filepath.Join(root, "output")
	manifestPath := filepath.Join(root, "task_manifest.json")
	raw, _ := json.Marshal(map[string]any{
		"task_id": "task-plain", "action": "remix.standard", "output_dir": outputDir,
		"inputs": []any{map[string]any{"type": "source_script", "path": sourcePath}},
	})
	_ = os.WriteFile(manifestPath, raw, 0o644)
	last := filepath.Join(root, "last.json")
	script := "又一批人要发财了，人民币第三次换锚已经开始。前两波是美元外贸和土地房子，旧锚死了。第三个锚先不说完，现在就上车。"
	if err := Run(Options{
		ManifestPath: manifestPath, SkillRoot: skillRoot, OutputLastMessage: last,
		BaseURL: "http://example.invalid/v1", APIKey: "k",
		Client: &textClient{content: script},
	}); err != nil {
		t.Fatal(err)
	}
	got, _ := os.ReadFile(filepath.Join(outputDir, "continuous_script.txt"))
	if !strings.Contains(string(got), "第三次换锚") {
		t.Fatalf("script=%s", got)
	}
}

const remixJSON = `{"continuous_script":"又一批人要发财了，人民币第三次换锚已经开始。前两波是美元外贸和土地房子，旧锚死了，利率下来，一百七十万亿存款在找出路。第三个锚先不说完，现在就上车。","titles":["人民币第三次换锚来了","下一批先富的人在哪","旧锚退潮钱去哪","一百七十万亿在找出口","第三个锚先不说完","窗口不会一直开着","看懂资金方向先上车","别只盯着工资存款"],"short_titles":["第三次换锚来了","钱会流向哪里","下一批赢家是谁","窗口不会等人","现在就上车吧"],"descriptions":["前两次换锚分别推高了外贸和房子。 #人民币 #财富趋势 #经济周期","看懂资金上游的人先拿位置。 #资金流向 #财富觉醒 #趋势判断","答案先留着，窗口不会一直开着。 #宏观经济 #资产趋势 #时代机会"],"topics":["#人民币","#财富趋势","#资金流向"],"cta":"关掉干扰，现在就去主页橱窗看《财富觉醒方法论》。"}`

type textClient struct {
	content  string
	streamed bool
	last     ChatRequest
}

func (c *textClient) Chat(req ChatRequest) (ChatResponse, error) {
	c.last = req
	c.streamed = req.Stream && len(req.Tools) == 0
	return textResponse(c.content), nil
}
