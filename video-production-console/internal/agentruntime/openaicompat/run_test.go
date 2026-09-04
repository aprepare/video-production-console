package openaicompat

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"video-production-console/internal/codex"
	"video-production-console/internal/domain"
)

// validPublishFieldsJSON 是合格的发布字段片段：这些测试关注正文自检，
// 字段给齐了就不会触发发布字段补齐那一轮额外请求。
const validPublishFieldsJSON = `"short_titles":["9月1日起你的数据能换钱了","五次机会你抓住过几回","这次不用本金也能进场"],"descriptions":["三份文件同一天生效，你的痕迹值钱了。","五次机会你抓住过几回？这回不用本金。"],"topics":["#财经","#数据资产","#新规落地"]`

func TestRewritePromptStamp(t *testing.T) {
	if RewritePromptStamp != RewritePromptStampStable {
		t.Fatalf("stamp=%q want %q", RewritePromptStamp, RewritePromptStampStable)
	}
	if RewritePromptStampStable != "语感回流 2026-08-25 批注回流2" {
		t.Fatalf("stable stamp=%q", RewritePromptStampStable)
	}
	if RewritePromptStampSharp != "锋利优先 2026-08-25" {
		t.Fatalf("sharp stamp=%q", RewritePromptStampSharp)
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
	system := buildWriterPrompt(PromptStyleRewrite)
	for _, want := range []string{
		"成功标准",
		"前 3 句与原稿同一件事",
		"力度不输原稿第一句的狠法",
		"第一句就砸事",
		"第一句不许铺现场",
		"宁可冲",
		"允许贴着原文开场金句",
		"禁止为了换说法把最狠那一拳",
		"故意不说完的答案继续藏着",
		"只有两万亿这种整量级才改口播说法",
		"两万亿",
		"2.05万亿",
		"0.95%",
		"950块",
		"百分之零点九五",
		"禁止写成理财课",
		"狠和懂打架，选狠",
		"整篇重写",
		"财富觉醒方法论",
		"本金乘利率",
		"改本金",
		"坏开头",
		"好开头",
		"禁止逐段同义改写",
		"#数据资产",
		"3到4个",
		"必须从这篇口播长出来",
		"再补两三句",
		"禁止带年份",
		"禁止「就这些」",
		"就说到这儿",
	} {
		if !strings.Contains(system, want) {
			t.Fatalf("system missing %q", want)
		}
	}
	for _, forbid := range []string{
		"语感指纹",
		"40 字内",
		"谁的钱、出了什么事",
		"锚点",
		"换锚",
		"2万亿",
		"50到77万亿",
		"第N个难题",
		"中老年听得懂",
		"前 2～3 句",
		"第三次换锚",
		"一百七十万亿",
		"第三个锚故意不说完",
		"方便面被外卖抢走",
		"河的上游",
		"rewrite 还必须带 machine",
		"原稿的推进顺序不能倒",
	} {
		if strings.Contains(system, forbid) {
			t.Fatalf("rewrite prompt must not contain %q", forbid)
		}
	}
	user := buildWriterUser(PromptStyleRewrite, manifestLite{}, "法拍房快堆到四十万套")
	if !strings.Contains(user, "不当逐句模板") || !strings.Contains(user, "标题和短标题必须跟这篇新口播走") || !strings.Contains(user, "第一句必须够狠") || !strings.Contains(user, "贴着原文开场金句") || !strings.Contains(user, "法拍房快堆到四十万套") || strings.Contains(user, "先抽语感指纹") || strings.Contains(user, "四十岁") || strings.Contains(user, "只换说法和加料，不换题") {
		t.Fatalf("user=%q", user)
	}
}

func TestWriterPromptSharpEmphasizesImpact(t *testing.T) {
	system := buildWriterPrompt(PromptStyleRewriteSharp)
	for _, want := range []string{
		"锋利指纹",
		"第一目标不是「合规」",
		"听起来还是同一条爆款",
		"财富觉醒方法论",
		"开场切口必须换",
		"禁止编造日期",
	} {
		if !strings.Contains(system, want) {
			t.Fatalf("sharp system missing %q", want)
		}
	}
	user := buildWriterUser(PromptStyleRewriteSharp, manifestLite{}, "法拍房快堆到四十万套")
	if !strings.Contains(user, "先抓锋利指纹") || !strings.Contains(user, "禁止写软") {
		t.Fatalf("sharp user=%q", user)
	}
}

func TestWriterPromptOmitsSkillExcerpt(t *testing.T) {
	system := buildWriterPrompt(PromptStyleRewrite)
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
	got, err = NormalizePromptStyle("rewrite")
	if err != nil || got != PromptStyleRewrite {
		t.Fatalf("rewrite=%q err=%v", got, err)
	}
	got, err = NormalizePromptStyle("rewrite_sharp")
	if err != nil || got != PromptStyleRewriteSharp {
		t.Fatalf("rewrite_sharp=%q err=%v", got, err)
	}
	got, err = NormalizePromptStyle("copy")
	if err != nil || got != PromptStyleCopy {
		t.Fatalf("copy=%q err=%v", got, err)
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

	copyStub := &stubCopyClient{hooks: "钩子A", scripts: "脚本B"}
	client := &textClient{content: remixJSON}
	if err := Run(Options{
		ManifestPath:      manifestPath,
		SkillRoot:         skillRoot,
		OutputLastMessage: last,
		Model:             "test-model",
		BaseURL:           "http://example.invalid/v1",
		APIKey:            "test-key",
		Client:            client,
		CopyClient:        copyStub,
	}); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !client.streamed {
		t.Fatalf("remix request must stream and omit tools")
	}
	if len(copyStub.calls) != 0 {
		t.Fatalf("default rewrite must not call copy: %#v", copyStub.calls)
	}
	if len(client.last.Messages) != 2 || !strings.Contains(client.last.Messages[0].Content, "成功标准") || !strings.Contains(client.last.Messages[0].Content, "狠和懂打架，选狠") || strings.Contains(client.last.Messages[1].Content, "钩子A") {
		t.Fatalf("default rewrite must use stable writer prompt: %#v", client.last.Messages)
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

func TestRunSharpStyleUsesSharpPrompt(t *testing.T) {
	root := t.TempDir()
	skillRoot := filepath.Join(root, "skill")
	_ = os.MkdirAll(skillRoot, 0o755)
	_ = os.WriteFile(filepath.Join(skillRoot, "SKILL.md"), []byte("# skill"), 0o644)
	sourcePath := filepath.Join(root, "source.txt")
	_ = os.WriteFile(sourcePath, []byte("又一批人要发财了。人民币要第三次换锚。"), 0o644)
	outputDir := filepath.Join(root, "output")
	manifestPath := filepath.Join(root, "task_manifest.json")
	raw, _ := json.Marshal(map[string]any{
		"task_id": "task-sharp-1", "action": "remix.standard", "skill": "finance-viral-remix",
		"output_dir":          outputDir,
		"inputs":              []any{map[string]any{"type": "source_script", "role": "primary_source", "path": sourcePath}},
		"non_secret_settings": map[string]any{"remix_prompt_style": "rewrite_sharp"},
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
		CopyClient:        &stubCopyClient{},
	}); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(client.last.Messages) != 2 || !strings.Contains(client.last.Messages[0].Content, "锋利指纹") || !strings.Contains(client.last.Messages[1].Content, "先抓锋利指纹") {
		t.Fatalf("rewrite_sharp must use sharp writer prompt: %#v", client.last.Messages)
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

func TestRunCopyStyleUsesAssemblePrompt(t *testing.T) {
	root := t.TempDir()
	skillRoot := filepath.Join(root, "skill")
	_ = os.MkdirAll(skillRoot, 0o755)
	_ = os.WriteFile(filepath.Join(skillRoot, "SKILL.md"), []byte("# skill"), 0o644)
	sourcePath := filepath.Join(root, "source.txt")
	_ = os.WriteFile(sourcePath, []byte("又一批人要发财了。人民币要第三次换锚。"), 0o644)
	outputDir := filepath.Join(root, "output")
	manifestPath := filepath.Join(root, "task_manifest.json")
	raw, _ := json.Marshal(map[string]any{
		"task_id": "task-copy-1", "action": "remix.standard", "skill": "finance-viral-remix",
		"output_dir":          outputDir,
		"inputs":              []any{map[string]any{"type": "source_script", "role": "primary_source", "path": sourcePath}},
		"non_secret_settings": map[string]any{"remix_prompt_style": "copy"},
	})
	_ = os.WriteFile(manifestPath, raw, 0o644)
	copyStub := &stubCopyClient{hooks: "钩子A", scripts: "脚本B"}
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
		CopyClient:        copyStub,
	}); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(copyStub.calls) != 2 {
		t.Fatalf("copy style must call hooks and scripts: %#v", copyStub.calls)
	}
	if len(client.last.Messages) != 2 || !strings.Contains(client.last.Messages[0].Content, "低于 40%") || !strings.Contains(client.last.Messages[1].Content, "钩子A") || !strings.Contains(client.last.Messages[1].Content, "脚本B") {
		t.Fatalf("copy style must use assemble prompt: %#v", client.last.Messages)
	}
}

func TestRunCopyStillRunsQualityGate(t *testing.T) {
	root := t.TempDir()
	skillRoot := filepath.Join(root, "skill")
	_ = os.MkdirAll(skillRoot, 0o755)
	sourcePath := filepath.Join(root, "source.txt")
	source := "问一个让你后背发凉的问题，如果全国老百姓存在银行里的钱突然少了整整2万亿，而且不是买了房，不是炒个股，连最火的黄金都没接住这笔钱，那它到底变成了什么？"
	_ = os.WriteFile(sourcePath, []byte(source), 0o644)
	outputDir := filepath.Join(root, "output")
	manifestPath := filepath.Join(root, "task_manifest.json")
	raw, _ := json.Marshal(map[string]any{
		"task_id": "11111111-1111-1111-1111-111111111111", "action": "remix.standard", "output_dir": outputDir,
		"inputs":              []any{map[string]any{"type": "source_script", "path": sourcePath}},
		"non_secret_settings": map[string]any{"remix_prompt_style": "copy"},
	})
	_ = os.WriteFile(manifestPath, raw, 0o644)
	last := filepath.Join(root, "last.json")
	script := source + "点开主页橱窗看《财富觉醒方法论》。"
	payload := `{"continuous_script":` + mustJSONString(script) + `,"titles":[],` + validPublishFieldsJSON + `,"cta":""}`
	if err := Run(Options{
		ManifestPath: manifestPath, SkillRoot: skillRoot, OutputLastMessage: last,
		BaseURL: "http://example.invalid/v1", APIKey: "test-key",
		Client:     &textClient{content: payload},
		CopyClient: &stubCopyClient{hooks: "钩子A", scripts: "脚本B"},
	}); err != nil {
		t.Fatal(err)
	}
	logRaw, err := os.ReadFile(filepath.Join(outputDir, "remix_run.json"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(logRaw), "quality_failed") {
		t.Fatalf("copy style must still run overlap QC, log=%s", logRaw)
	}
	env, err := os.ReadFile(last)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(env), `"failed"`) {
		t.Fatalf("copy high-overlap draft must fail QC: %s", env)
	}
}

func TestRunSharpStillRunsQualityGate(t *testing.T) {
	root := t.TempDir()
	skillRoot := filepath.Join(root, "skill")
	_ = os.MkdirAll(skillRoot, 0o755)
	sourcePath := filepath.Join(root, "source.txt")
	source := "问一个让你后背发凉的问题，如果全国老百姓存在银行里的钱突然少了整整2万亿，而且不是买了房，不是炒个股，连最火的黄金都没接住这笔钱，那它到底变成了什么？"
	_ = os.WriteFile(sourcePath, []byte(source), 0o644)
	outputDir := filepath.Join(root, "output")
	manifestPath := filepath.Join(root, "task_manifest.json")
	raw, _ := json.Marshal(map[string]any{
		"task_id": "11111111-1111-1111-1111-111111111111", "action": "remix.standard", "output_dir": outputDir,
		"inputs":              []any{map[string]any{"type": "source_script", "path": sourcePath}},
		"non_secret_settings": map[string]any{"remix_prompt_style": "rewrite_sharp"},
	})
	_ = os.WriteFile(manifestPath, raw, 0o644)
	last := filepath.Join(root, "last.json")
	script := source + "点开主页橱窗看《财富觉醒方法论》。"
	payload := `{"continuous_script":` + mustJSONString(script) + `,"titles":[],` + validPublishFieldsJSON + `,"cta":""}`
	if err := Run(Options{
		ManifestPath: manifestPath, SkillRoot: skillRoot, OutputLastMessage: last,
		BaseURL: "http://example.invalid/v1", APIKey: "test-key",
		Client: &textClient{content: payload},
	}); err != nil {
		t.Fatal(err)
	}
	logRaw, err := os.ReadFile(filepath.Join(outputDir, "remix_run.json"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(logRaw), "quality_failed") {
		t.Fatalf("rewrite_sharp must still run overlap QC, log=%s", logRaw)
	}
	env, err := os.ReadFile(last)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(env), `"failed"`) {
		t.Fatalf("rewrite_sharp high-overlap draft must fail QC: %s", env)
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
	if _, err := os.Stat(filepath.Join(outputDir, "model_raw.txt")); err != nil {
		t.Fatalf("model_raw.txt: %v", err)
	}
	if _, err := os.Stat(filepath.Join(outputDir, "remix_run.json")); err != nil {
		t.Fatalf("remix_run.json: %v", err)
	}
}

// selfCheckTestSource 内容字超过 200，够触发 rewrite 自检。
const selfCheckTestSource = "9月1日之后，你存银行的钱还是你的，但别人拿数据换来的钱可能比你干一辈子活挣得还多。财政部和国家数据局出了三份文件，一个管分类，让数据分得清。一个管登记，让数据找得到主。一个管产权分配，让数据赚了钱有你一份。以前没人说得清数据到底算谁的，现在说得清了。以前没人能给数据定个价，现在有规矩了。以前数据赚的钱跟你没关系，现在有关系了。同样一份数据，这家公司说值100万，那家公司说一分不值，银行谁的话都不敢信，因为没有全国统一的标准。9月1日之后，所有数据统一分类、统一编号、统一入库，全国任何一个合规机构看到编号就知道它是什么，归谁，值多少。很多人一辈子就栽在名词恐惧上，听到一个新词觉得自己不懂就不听了，然后跟机会擦肩而过。"

// selfCheckCleanScript 同一批事实全部换说法，只保留锁词，篇幅不低于原文。
const selfCheckCleanScript = "再过几天，一条新规矩就落地了：你天天刷手机留下的那些痕迹，从此算你名下的家当。财政部跟国家数据局联手发了三份文件，头一份定类别，什么样的痕迹归哪一类写得明明白白；第二份办登记，每一条都记上主人的名字；第三份分收益，卖出去的钱里头写清楚有你的份。搁在过去，这三件事没有一件办得成：归属不明，价钱不定，赚了钱也轮不到你。乱到什么程度？同一批痕迹，甲公司开口就是100万，乙公司摇头说不值钱，银行两边都不敢认账，谁也拿不出全国通行的尺子。往后不一样了，所有数据统一分类、统一编号、统一入库，随便哪家有资质的机构一查号码，来历、主人、身价全都清清楚楚。还有不少人一碰到新词就往后缩，觉得自己弄不懂干脆不碰，结果眼睁睁看着别人先动手。去我主页橱窗看《财富觉醒方法论》。"

func TestRunRewriteSelfCheckRepairsCopiedDraft(t *testing.T) {
	root := t.TempDir()
	skillRoot := filepath.Join(root, "skill")
	_ = os.MkdirAll(skillRoot, 0o755)
	sourcePath := filepath.Join(root, "source.txt")
	_ = os.WriteFile(sourcePath, []byte(selfCheckTestSource), 0o644)
	outputDir := filepath.Join(root, "output")
	manifestPath := filepath.Join(root, "task_manifest.json")
	raw, _ := json.Marshal(map[string]any{
		"task_id": "11111111-1111-1111-1111-111111111111", "action": "remix.standard", "output_dir": outputDir,
		"inputs": []any{map[string]any{"type": "source_script", "path": sourcePath}},
	})
	_ = os.WriteFile(manifestPath, raw, 0o644)
	last := filepath.Join(root, "last.json")
	copied := selfCheckTestSource + "去我主页橱窗看《财富觉醒方法论》。"
	copiedPayload := `{"continuous_script":` + mustJSONString(copied) + `,"titles":[],` + validPublishFieldsJSON + `,"cta":""}`
	cleanPayload := `{"continuous_script":` + mustJSONString(selfCheckCleanScript) + `,"titles":[],` + validPublishFieldsJSON + `,"cta":""}`
	client := &sequenceClient{responses: []string{copiedPayload, cleanPayload}}
	if err := Run(Options{
		ManifestPath: manifestPath, SkillRoot: skillRoot, OutputLastMessage: last,
		BaseURL: "http://example.invalid/v1", APIKey: "test-key",
		Client: client,
	}); err != nil {
		t.Fatal(err)
	}
	if len(client.requests) != 2 {
		t.Fatalf("照抄稿必须触发一轮自检返工, requests=%d", len(client.requests))
	}
	repair := client.requests[1]
	if len(repair.Messages) != 4 {
		t.Fatalf("返工请求必须带完整对话链: %#v", repair.Messages)
	}
	if !strings.Contains(repair.Messages[3].Content, "自检") || !strings.Contains(repair.Messages[3].Content, "换说法") {
		t.Fatalf("返工提示必须列出撞车片段: %s", repair.Messages[3].Content)
	}
	got, err := os.ReadFile(filepath.Join(outputDir, "continuous_script.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(got), "乙公司摇头") || strings.Contains(string(got), "干一辈子活") {
		t.Fatalf("必须交付返工后的干净稿: %s", got)
	}
	logRaw, err := os.ReadFile(filepath.Join(outputDir, "remix_run.json"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(logRaw), "self_check") || !strings.Contains(string(logRaw), "quality_passed") {
		t.Fatalf("run log=%s", logRaw)
	}
	env, err := os.ReadFile(last)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(env), `"completed"`) || !strings.Contains(string(env), "自检") {
		t.Fatalf("envelope=%s", env)
	}
}

func TestRunRewriteSelfCheckFailsWhenRepairKeepsCopying(t *testing.T) {
	root := t.TempDir()
	skillRoot := filepath.Join(root, "skill")
	_ = os.MkdirAll(skillRoot, 0o755)
	sourcePath := filepath.Join(root, "source.txt")
	_ = os.WriteFile(sourcePath, []byte(selfCheckTestSource), 0o644)
	outputDir := filepath.Join(root, "output")
	manifestPath := filepath.Join(root, "task_manifest.json")
	raw, _ := json.Marshal(map[string]any{
		"task_id": "11111111-1111-1111-1111-111111111111", "action": "remix.standard", "output_dir": outputDir,
		"inputs": []any{map[string]any{"type": "source_script", "path": sourcePath}},
	})
	_ = os.WriteFile(manifestPath, raw, 0o644)
	last := filepath.Join(root, "last.json")
	copied := selfCheckTestSource + "去我主页橱窗看《财富觉醒方法论》。"
	copiedPayload := `{"continuous_script":` + mustJSONString(copied) + `,"titles":[],` + validPublishFieldsJSON + `,"cta":""}`
	client := &sequenceClient{responses: []string{copiedPayload, copiedPayload}}
	if err := Run(Options{
		ManifestPath: manifestPath, SkillRoot: skillRoot, OutputLastMessage: last,
		BaseURL: "http://example.invalid/v1", APIKey: "test-key",
		Client: client,
	}); err != nil {
		t.Fatal(err)
	}
	env, err := os.ReadFile(last)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(env), `"failed"`) || !strings.Contains(string(env), "自检不通过") {
		t.Fatalf("返工无改善且超硬性上限必须失败: %s", env)
	}
	logRaw, _ := os.ReadFile(filepath.Join(outputDir, "remix_run.json"))
	if !strings.Contains(string(logRaw), "quality_failed") {
		t.Fatalf("run log=%s", logRaw)
	}
}

func TestRunRewriteSelfCheckSkipsShortSource(t *testing.T) {
	root := t.TempDir()
	skillRoot := filepath.Join(root, "skill")
	_ = os.MkdirAll(skillRoot, 0o755)
	sourcePath := filepath.Join(root, "source.txt")
	source := "问一个让你后背发凉的问题，如果全国老百姓存在银行里的钱突然少了整整2万亿，那它到底变成了什么？"
	_ = os.WriteFile(sourcePath, []byte(source), 0o644)
	outputDir := filepath.Join(root, "output")
	manifestPath := filepath.Join(root, "task_manifest.json")
	raw, _ := json.Marshal(map[string]any{
		"task_id": "11111111-1111-1111-1111-111111111111", "action": "remix.standard", "output_dir": outputDir,
		"inputs": []any{map[string]any{"type": "source_script", "path": sourcePath}},
	})
	_ = os.WriteFile(manifestPath, raw, 0o644)
	last := filepath.Join(root, "last.json")
	script := source + "去我主页橱窗看《财富觉醒方法论》。"
	payload := `{"continuous_script":` + mustJSONString(script) + `,"titles":[],` + validPublishFieldsJSON + `,"cta":""}`
	client := &textClient{content: payload}
	if err := Run(Options{
		ManifestPath: manifestPath, SkillRoot: skillRoot, OutputLastMessage: last,
		BaseURL: "http://example.invalid/v1", APIKey: "test-key",
		Client: client,
	}); err != nil {
		t.Fatal(err)
	}
	if len(client.last.Messages) != 2 {
		t.Fatalf("原文过短必须跳过返工: %#v", client.last.Messages)
	}
	env, err := os.ReadFile(last)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(env), `"completed"`) {
		t.Fatalf("envelope=%s", env)
	}
}

func TestWriteFailureCaptionKeywordsOmitsRemixArtifacts(t *testing.T) {
	root := t.TempDir()
	outputDir := filepath.Join(root, "output")
	if err := os.MkdirAll(outputDir, 0o755); err != nil {
		t.Fatal(err)
	}
	taskID := "11111111-1111-1111-1111-111111111111"
	manifestPath := filepath.Join(root, "task_manifest.json")
	raw, _ := json.Marshal(map[string]any{
		"task_id": taskID, "action": "remix.caption_keywords", "output_dir": outputDir,
	})
	if err := os.WriteFile(manifestPath, raw, 0o644); err != nil {
		t.Fatal(err)
	}
	last := filepath.Join(root, "last.json")
	if err := writeFailure(last, manifestPath, fmt.Errorf("chat completions status 400: model_not_found")); err != nil {
		t.Fatal(err)
	}
	env, err := os.ReadFile(last)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(env), "remix_run") || strings.Contains(string(env), "model_raw") {
		t.Fatalf("caption_keywords failure must not advertise remix artifacts: %s", env)
	}
	if _, err := os.Stat(filepath.Join(outputDir, "remix_run.json")); err != nil {
		t.Fatalf("local remix_run.json still written: %v", err)
	}
	if _, err := codex.ValidateResultEnvelopeJSON(env, taskID, domain.ActionCaptionKeywords, outputDir); err != nil {
		t.Fatalf("failed caption_keywords envelope must validate: %v body=%s", err, env)
	}
}

func TestRunUsesOverrideSystemAndUserPrompts(t *testing.T) {
	root := t.TempDir()
	skillRoot := filepath.Join(root, "skill")
	_ = os.MkdirAll(skillRoot, 0o755)
	sourcePath := filepath.Join(root, "source.txt")
	_ = os.WriteFile(sourcePath, []byte("对标原文在这里。"), 0o644)
	outputDir := filepath.Join(root, "output")
	manifestPath := filepath.Join(root, "task_manifest.json")
	raw, _ := json.Marshal(map[string]any{
		"task_id": "task-override-1", "action": "remix.standard", "skill": "finance-viral-remix",
		"output_dir": outputDir,
		"inputs":     []any{map[string]any{"type": "source_script", "role": "primary_source", "path": sourcePath}},
		"non_secret_settings": map[string]any{
			"remix_prompt_style":  "rewrite",
			"remix_system_prompt": "OVERRIDE-SYSTEM-PROMPT",
			"remix_user_prompt":   "OVERRIDE-USER\n{{SOURCE}}",
			"remix_prompt_stamp":  "交叉试验覆盖",
		},
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
		t.Fatal(err)
	}
	if len(client.last.Messages) != 2 {
		t.Fatalf("messages=%d", len(client.last.Messages))
	}
	if client.last.Messages[0].Content != "OVERRIDE-SYSTEM-PROMPT" {
		t.Fatalf("system=%q", client.last.Messages[0].Content)
	}
	if !strings.Contains(client.last.Messages[1].Content, "OVERRIDE-USER") || !strings.Contains(client.last.Messages[1].Content, "对标原文在这里。") {
		t.Fatalf("user=%q", client.last.Messages[1].Content)
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
