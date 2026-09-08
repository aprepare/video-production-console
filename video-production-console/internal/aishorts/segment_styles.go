package aishorts

import (
	"errors"
	"regexp"
	"strings"
)

var errSegmentStyle = errors.New("分段画风请选一套具体画风，或留空跟随底色")

// 分段画风（用户 2026-09-08 认可）：整篇一套底色，关键位置换画风——
// 开头前几镜抓人、讲钱的镜用静物、祝福词那一镜用剪纸、课尾前的处境场景用油画。
// 做法：拆完分镜后按规则给每镜打一个 Role，再由 VisualSettings.SegmentStyles 把 Role 映射成画风；
// 用户手动改过某一镜画风的（StylePinned）不再被覆盖。

// SegmentStyles 各位置用什么画风；空串 = 跟随底色（项目画风）。
type SegmentStyles struct {
	Opening      string `json:"opening,omitempty"`
	OpeningShots int    `json:"opening_shots,omitempty"` // 开头算几镜，默认 1，最多 5
	Money        string `json:"money,omitempty"`
	Blessing     string `json:"blessing,omitempty"`
	Scenario     string `json:"scenario,omitempty"`
}

// 镜头角色。course 单独标出来是为了让"结尾处境"不会误伸进课尾。
const (
	RoleBody     = "body"
	RoleOpening  = "opening"
	RoleMoney    = "money"
	RoleBlessing = "blessing"
	RoleScenario = "scenario"
	RoleCourse   = "course"
)

// RecommendedSegmentStyles 是 07 里给用户的建议方案："用推荐方案"按钮填的就是它。
func RecommendedSegmentStyles() SegmentStyles {
	return SegmentStyles{Opening: "retro_film", OpeningShots: 1, Money: "macro_money", Blessing: "papercut", Scenario: "oil_painting"}
}

func (s SegmentStyles) isZero() bool {
	return s.Opening == "" && s.Money == "" && s.Blessing == "" && s.Scenario == "" && s.OpeningShots == 0
}

// forRole 返回该角色指定的画风；没指定返回空。
func (s SegmentStyles) forRole(role string) string {
	switch role {
	case RoleOpening:
		return s.Opening
	case RoleMoney:
		return s.Money
	case RoleBlessing:
		return s.Blessing
	case RoleScenario:
		return s.Scenario
	}
	return ""
}

func (s SegmentStyles) openingShots() int {
	if s.OpeningShots <= 0 {
		return 1
	}
	if s.OpeningShots > 5 {
		return 5
	}
	return s.OpeningShots
}

// normalizeSegmentStyles 校验：每个位置要么空，要么是一套具体画风（混合策略不算画风）。
func normalizeSegmentStyles(s SegmentStyles) (SegmentStyles, error) {
	fix := func(key string) (string, error) {
		key = strings.TrimSpace(key)
		if key == "" {
			return "", nil
		}
		if key == financeEditorial {
			return "", errSegmentStyle
		}
		if StyleByKey(key).Key != key {
			return "", errSegmentStyle
		}
		return key, nil
	}
	var err error
	if s.Opening, err = fix(s.Opening); err != nil {
		return s, err
	}
	if s.Money, err = fix(s.Money); err != nil {
		return s, err
	}
	if s.Blessing, err = fix(s.Blessing); err != nil {
		return s, err
	}
	if s.Scenario, err = fix(s.Scenario); err != nil {
		return s, err
	}
	if s.OpeningShots < 0 {
		s.OpeningShots = 0
	}
	if s.OpeningShots > 5 {
		s.OpeningShots = 5
	}
	return s, nil
}

var (
	// 讲钱的镜：带单位的金额 / 利率优先，其次利息、存款这类银行词。年份单独出现不算（"2001年"是历史段）。
	// 静物只能做点不能做面：一篇最多 moneyMaxShots 镜，先取有数字的。09-08 用天中观局稿实测，不限的话 41 镜里 12 镜都成了钱堆。
	moneyNumberRE = regexp.MustCompile(`[0-9０-９]+(?:[.．][0-9０-９]+)?\s*(?:%|％|万亿|亿|万块|万元|块钱|块|元)`)
	moneyWords    = []string{"利息", "利率", "定存", "存款", "存银行", "万亿", "块钱", "一万块"}
	moneyMaxShots = 4
	// 祝福词镜：中段那一句"评论区留四个字"。
	blessingWords = []string{"评论区", "四个字", "财源滚滚", "时来运转", "八方来财", "一帆风顺", "顺风顺水", "财源广进", "好运连连", "招财进宝"}
	// 课尾从第一次提到课/橱窗开始。不用"五块钱"——"九十五块钱"会误中。
	courseWords = []string{"橱窗", "财富觉醒方法论", "一门课"}
	// 结尾处境场景："老伴半夜进医院先交的押金，您拿不拿得出来"那一段，在课尾之前几镜。
	scenarioWords   = []string{"底气", "有底", "的时候", "拿不拿得出", "凑不凑得齐", "给不给得起", "脸色", "撑住", "撑着的", "手里有粮", "才有资格"}
	scenarioWindow  = 8 // 只在课尾前这么多镜里找
	scenarioMaxShot = 4
)

func containsAny(text string, words []string) bool {
	for _, w := range words {
		if strings.Contains(text, w) {
			return true
		}
	}
	return false
}

// assignShotRoles 按旁白给每镜打角色。优先级：course > opening > blessing > scenario > money > body。
func assignShotRoles(shots []Shot, openingShots int) {
	if openingShots <= 0 {
		openingShots = 1
	}
	courseStart := len(shots)
	for i, s := range shots {
		if containsAny(s.Narration, courseWords) {
			courseStart = i
			break
		}
	}
	// 处境场景：课尾前 scenarioWindow 镜里，命中标记词的第一镜到最后一镜，最多 scenarioMaxShot 镜。
	scenarioFrom, scenarioTo := -1, -1
	for i := max(0, courseStart-scenarioWindow); i < courseStart; i++ {
		if containsAny(shots[i].Narration, scenarioWords) {
			if scenarioFrom < 0 {
				scenarioFrom = i
			}
			scenarioTo = i
		}
	}
	if scenarioFrom >= 0 && scenarioTo-scenarioFrom+1 > scenarioMaxShot {
		scenarioFrom = scenarioTo - scenarioMaxShot + 1
	}
	moneyScore := map[int]int{}
	for i := range shots {
		n := shots[i].Narration
		switch {
		case i >= courseStart:
			shots[i].Role = RoleCourse
		case i < openingShots:
			shots[i].Role = RoleOpening
		case containsAny(n, blessingWords):
			shots[i].Role = RoleBlessing
		case scenarioFrom >= 0 && i >= scenarioFrom && i <= scenarioTo:
			shots[i].Role = RoleScenario
		case moneyNumberRE.MatchString(n):
			shots[i].Role, moneyScore[i] = RoleMoney, 2
		case containsAny(n, moneyWords):
			shots[i].Role, moneyScore[i] = RoleMoney, 1
		default:
			shots[i].Role = RoleBody
		}
	}
	// 超出上限的"讲钱"镜退回正文：先保有数字的，同分保靠前的，且不连着两镜都是静物（连着就成"面"了）。
	if len(moneyScore) > moneyMaxShots {
		keep := map[int]bool{}
		for _, score := range []int{2, 1} {
			for i := range shots {
				if len(keep) < moneyMaxShots && moneyScore[i] == score && !keep[i-1] && !keep[i+1] {
					keep[i] = true
				}
			}
		}
		for i := range moneyScore {
			if !keep[i] {
				shots[i].Role = RoleBody
			}
		}
	}
}

func segmentStylesOf(short *Short) SegmentStyles {
	if short == nil || short.VisualSettings == nil {
		return SegmentStyles{}
	}
	return short.VisualSettings.SegmentStyles
}

// resolvedShotStyleFor 是单镜画风的唯一出口：手动钉住的 > 分段画风 > 项目画风（混合策略保留模型选的拼贴/微缩）。
func resolvedShotStyleFor(short *Short, shot Shot) string {
	if shot.StylePinned && strings.TrimSpace(shot.StyleKey) != "" && StyleByKey(shot.StyleKey).Key == shot.StyleKey {
		return shot.StyleKey
	}
	if key := segmentStylesOf(short).forRole(shot.Role); key != "" {
		return StyleByKey(key).Key
	}
	return resolvedShotStyle(short.Style, shot.StyleKey)
}

// reapplyShotStyles 重新打角色、重新解析每镜画风；画风变了的镜把旧图标成需要重生，视频作废。
// 返回变了几镜。
func reapplyShotStyles(short *Short) int {
	if short == nil || !short.IsExplainer() {
		return 0
	}
	assignShotRoles(short.Shots, segmentStylesOf(short).openingShots())
	changed := 0
	for i := range short.Shots {
		shot := &short.Shots[i]
		next := resolvedShotStyleFor(short, *shot)
		if next == shot.StyleKey {
			continue
		}
		shot.StyleKey = next
		shot.VideoPath, shot.VideoStatus, shot.VideoRequestID = "", ShotPending, ""
		shot.Hero, shot.VideoPrompt, shot.Error = false, "", ""
		changed++
	}
	if changed > 0 {
		fillAllPrompts(short, false)
		// 图是不是旧的看它真正用过的提示词：换过去又换回来的镜，图和现在的提示词一致就不用重生。
		for i := range short.Shots {
			shot := &short.Shots[i]
			if shot.ImagePath != "" && shot.ImagePromptUsed != "" {
				shot.ImageStale = shot.ImagePromptUsed != shot.ImagePrompt
			} else if shot.ImagePath != "" {
				shot.ImageStale = true
			}
		}
	}
	return changed
}
