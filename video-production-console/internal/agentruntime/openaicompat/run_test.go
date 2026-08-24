package openaicompat

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRewritePromptStamp(t *testing.T) {
	if RewritePromptStamp != "口播copy换说法 2026-08-24" {
		t.Fatalf("stamp=%q", RewritePromptStamp)
	}
}

func TestAssemblePromptUsesCopyMaterials(t *testing.T) {
	system := buildAssemblePrompt()
	for _, want := range []string{"钩子候选", "分镜脚本", "财富觉醒方法论", "cta 必须空字符串", "换词换说法", "不要把分镜里的口播原句念出来", "低于 40%", "50到77万亿", "开场切口必须和原稿前 80 字不同"} {
		if !strings.Contains(system, want) {
			t.Fatalf("system missing %q", want)
		}
	}
	if strings.Contains(system, "只整理") || strings.Contains(system, "按分镜脚本的口播句子往下走") {
		t.Fatal("assemble prompt must not reuse the stitch-together prompt")
	}
	user := buildAssembleUser(manifestLite{}, "原文", "钩子A", "脚本B")
	if !strings.Contains(user, "# 钩子候选") || !strings.Contains(user, "钩子A") || !strings.Contains(user, "脚本B") || !strings.Contains(user, "原文") || !strings.Contains(user, "换词换说法") {
		t.Fatalf("user=%q", user)
	}
}

func TestWriterPromptForbidsLineByLineParaphrase(t *testing.T) {
	system := buildWriterPrompt()
	for _, want := range []string{
		"禁止逐段同义改写",
		"机器以原文为准",
		"财富觉醒方法论",
		"本金乘利率",
		"出场顺序可以换",
		"开场切口必须换",
		"钩子类型不许换",
		"前 3 句内",
		"纯解释句、纯共情句",
		"#干货分享",
		"3到4个",
		"必须从这篇口播长出来",
		"中老年听得懂",
		"关键数字必须原词留下",
		"第N个难题",
		"卖课钩子只在全文最末",
		"禁止带年份",
		"最多四句",
		"前 2～3 句",
		"示范原句",
	} {
		if !strings.Contains(system, want) {
			t.Fatalf("system missing %q", want)
		}
	}
	for _, forbid := range []string{"第三次换锚", "一百七十万亿", "第三个锚故意不说完", "方便面被外卖抢走", "河的上游"} {
		if strings.Contains(system, forbid) {
			t.Fatalf("rewrite prompt must not hardcode plot %q", forbid)
		}
	}
	if strings.Contains(system, "rewrite 还必须带 machine") || strings.Contains(system, "原稿的推进顺序不能倒") {
		t.Fatalf("stale prompt clause still present")
	}
	user := buildWriterUser(manifestLite{}, "法拍房快堆到四十万套")
	if !strings.Contains(user, "不当逐句模板") || !strings.Contains(user, "先从原文锁机器") || !strings.Contains(user, "标题和短标题必须跟这篇新口播走") || !strings.Contains(user, "开场切口必须和原稿第一句不同") || !strings.Contains(user, "钩子类型必须跟原稿第一句同类") || strings.Contains(user, "五十岁以上") || strings.Contains(user, "只换说法和加料，不换题") {
		t.Fatalf("user=%q", user)
	}
}

func TestWriterPromptOmitsSkillExcerpt(t *testing.T) {
	system := buildWriterPrompt()
	if strings.Contains(system, "# 补充约束") {
		t.Fatalf("openai writer prompt must not append SKILL.md excerpt")
	}
	for _, forbid := range []string{"console mode", "task_manifest.json", "baokuan_search_materials", "result envelope", "选题卡"} {
		if strings.Contains(system, forbid) {
			t.Fatalf("writer prompt still carries skill-ops text %q", forbid)
		}
	}
}

func TestNormalizePromptStyle(t *testing.T) {
	got, err := NormalizePromptStyle("")
	if err != nil || got != PromptStyleRewrite {
		t.Fatalf("empty=%q err=%v", got, err)
	}
	if _, err := NormalizePromptStyle("wash"); err == nil {
		t.Fatal("wash style has been removed and must be rejected")
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
		CopyClient:        &stubCopyClient{hooks: "钩子A", scripts: "脚本B"},
	}); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !client.streamed {
		t.Fatalf("remix request must stream and omit tools")
	}
	if len(client.last.Messages) != 2 || !strings.Contains(client.last.Messages[1].Content, "钩子A") || !strings.Contains(client.last.Messages[1].Content, "脚本B") {
		t.Fatalf("assemble user missing copy materials: %#v", client.last.Messages)
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

func TestRunRejectsRemovedWashStyle(t *testing.T) {
	root := t.TempDir()
	skillRoot := filepath.Join(root, "skill")
	_ = os.MkdirAll(skillRoot, 0o755)
	_ = os.WriteFile(filepath.Join(skillRoot, "SKILL.md"), []byte("# skill"), 0o644)
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
	last := filepath.Join(root, "last.json")
	if err := Run(Options{
		ManifestPath:      manifestPath,
		SkillRoot:         skillRoot,
		OutputLastMessage: last,
		Model:             "test-model",
		BaseURL:           "http://example.invalid/v1",
		APIKey:            "test-key",
		Client:            client,
	}); err != nil {
		t.Fatalf("Run must write a failure envelope, err=%v", err)
	}
	body, err := os.ReadFile(last)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(body), `"failed"`) || !strings.Contains(string(body), "remix_prompt_style") {
		t.Fatalf("wash manifests must be rejected, envelope=%s", body)
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
		Client:     &textClient{content: script},
		CopyClient: &stubCopyClient{hooks: "钩子A", scripts: "脚本B"},
	}); err != nil {
		t.Fatal(err)
	}
	got, _ := os.ReadFile(filepath.Join(outputDir, "continuous_script.txt"))
	if !strings.Contains(string(got), "第三次换锚") {
		t.Fatalf("script=%s", got)
	}
}

func TestRunWritesSpokenScriptFromContinuousInput(t *testing.T) {
	root := t.TempDir()
	skillRoot := filepath.Join(root, "skill")
	_ = os.MkdirAll(skillRoot, 0o755)
	sourcePath := filepath.Join(root, "continuous.txt")
	_ = os.WriteFile(sourcePath, []byte("百分之六十七的人还在等窗口。"), 0o644)
	outputDir := filepath.Join(root, "output")
	manifestPath := filepath.Join(root, "task_manifest.json")
	raw, _ := json.Marshal(map[string]any{
		"task_id": "task-spoken-1", "action": "remix.spoken_lines", "skill": "finance-viral-remix",
		"output_dir": outputDir,
		"inputs":     []any{map[string]any{"type": "continuous_script", "role": "continuous_script", "path": sourcePath}},
	})
	_ = os.WriteFile(manifestPath, raw, 0o644)
	client := &textClient{content: "百分之六十七的人\n还在等窗口。"}
	if err := Run(Options{
		ManifestPath:      manifestPath,
		SkillRoot:         skillRoot,
		OutputLastMessage: filepath.Join(root, "last.json"),
		Model:             "test-model",
		BaseURL:           "http://example.invalid/v1",
		APIKey:            "test-key",
		Client:            client,
	}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(client.last.Messages[0].Content, "一句一行") {
		t.Fatalf("system=%q", client.last.Messages[0].Content)
	}
	got, err := os.ReadFile(filepath.Join(outputDir, "spoken_script.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(got), "67%") {
		t.Fatalf("spoken=%s", got)
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
