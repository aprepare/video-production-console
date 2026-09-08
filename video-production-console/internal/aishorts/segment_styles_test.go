package aishorts

import (
	"strings"
	"testing"
)

// 用天中观局改1 的真实旁白节选：开头、讲钱、祝福词、结尾处境、课尾各一段。
func segmentTestShort() *Short {
	lines := []string{
		"劝您一句：这两年，手里的钱别再随手花了。因为现在的钱，正在变得越来越值钱。",
		"前十几年是什么光景？您还记得。房价一年一个价，今天不买明天更贵。",
		"这话不是我说的，是全国老百姓用脚投的票。一年期定存利息 0.95%，一万块存一年九十五块钱。",
		"说到这儿，觉得说到心坎里的朋友，评论区留四个字——财源滚滚，给自己也给家里人。",
		"第一条，一分钱都别为面子花。面子是给别人看的，存折上的数是给自己活的。",
		"咱们这个岁数的人，底气是什么？老伴半夜进医院，先交的那笔押金，您拿不拿得出来；",
		"儿女办婚事，彩礼加酒席那笔钱，您凑不凑得齐；想给孙子包个像样的红包，用不用看谁的脸色。",
		"可我得多问您一句：您手里到底有多少粮，说得上来吗？一年到底攒下了多少，是个感觉还是个数？",
		"这一步，我做成了一门课，就在我主页橱窗里，叫《财富觉醒方法论》，只要五块钱，一斤豆腐的钱。",
		"点开我的主页，橱窗里就能找到这门课。橱窗里等你。",
	}
	short := &Short{Mode: ModeExplainer, Style: "cinematic_doc", VisualSettings: DefaultVisualSettings()}
	for _, l := range lines {
		short.Shots = append(short.Shots, Shot{Narration: l})
	}
	return short
}

func TestAssignShotRolesFromNarration(t *testing.T) {
	short := segmentTestShort()
	assignShotRoles(short.Shots, 1)
	// 第 5 镜"存折上的数"不算讲钱：只有带单位的金额 / 利率或银行词才算，静物只能做点。
	want := []string{RoleOpening, RoleBody, RoleMoney, RoleBlessing, RoleBody, RoleScenario, RoleScenario, RoleBody, RoleCourse, RoleCourse}
	for i, w := range want {
		if short.Shots[i].Role != w {
			t.Fatalf("shot %d role = %s, want %s (%s)", i, short.Shots[i].Role, w, short.Shots[i].Narration[:12])
		}
	}
}

func TestSegmentStylesResolvePinAndReapply(t *testing.T) {
	short := segmentTestShort()
	finalizeExplainerShots(short)
	for i, s := range short.Shots {
		if s.StyleKey != "cinematic_doc" {
			t.Fatalf("without segment styles shot %d should follow base, got %s", i, s.StyleKey)
		}
	}
	// 用推荐方案：开头胶片、讲钱静物、祝福剪纸、处境油画；课尾和正文跟底色。
	short.Shots[2].ImagePath = "money.jpg"
	short.VisualSettings.SegmentStyles = RecommendedSegmentStyles()
	if changed := reapplyShotStyles(short); changed != 5 {
		t.Fatalf("changed = %d, want 5 (opening + money + blessing + 2 scenario)", changed)
	}
	want := map[int]string{0: "retro_film", 1: "cinematic_doc", 2: "macro_money", 3: "papercut", 4: "cinematic_doc", 5: "oil_painting", 6: "oil_painting", 7: "cinematic_doc", 8: "cinematic_doc", 9: "cinematic_doc"}
	for i, w := range want {
		if short.Shots[i].StyleKey != w {
			t.Fatalf("shot %d style = %s, want %s", i, short.Shots[i].StyleKey, w)
		}
	}
	if !short.Shots[2].ImageStale {
		t.Fatal("shot whose style changed must mark its old image stale")
	}
	if !strings.Contains(short.Shots[2].ImagePrompt, "静物特写") || strings.Contains(short.Shots[2].ImagePrompt, explainerPeopleRule) {
		t.Fatalf("macro_money prompt should switch to still life without people rule: %s", short.Shots[2].ImagePrompt)
	}
	// 手动钉住第 0 镜为水墨：再改分段画风不覆盖它。
	short.Shots[0].StyleKey, short.Shots[0].StylePinned = "ink_wash", true
	short.VisualSettings.SegmentStyles.Opening = "woodcut_poster"
	reapplyShotStyles(short)
	if short.Shots[0].StyleKey != "ink_wash" {
		t.Fatalf("pinned shot overridden: %s", short.Shots[0].StyleKey)
	}
	// 清空分段画风：回到底色，钉住的仍不动。
	short.VisualSettings.SegmentStyles = SegmentStyles{}
	reapplyShotStyles(short)
	if short.Shots[0].StyleKey != "ink_wash" || short.Shots[2].StyleKey != "cinematic_doc" || short.Shots[5].StyleKey != "cinematic_doc" {
		t.Fatalf("clearing segment styles: %s %s %s", short.Shots[0].StyleKey, short.Shots[2].StyleKey, short.Shots[5].StyleKey)
	}
}

func TestNormalizeSegmentStylesRejectsStrategyKey(t *testing.T) {
	if _, err := normalizeSegmentStyles(SegmentStyles{Money: financeEditorial}); err == nil {
		t.Fatal("mixed strategy is not a concrete style")
	}
	if _, err := normalizeSegmentStyles(SegmentStyles{Opening: "nope"}); err == nil {
		t.Fatal("unknown style must be rejected")
	}
	got, err := normalizeSegmentStyles(SegmentStyles{Opening: " retro_film ", OpeningShots: 9})
	if err != nil || got.Opening != "retro_film" || got.OpeningShots != 5 {
		t.Fatalf("normalize: %+v %v", got, err)
	}
}
