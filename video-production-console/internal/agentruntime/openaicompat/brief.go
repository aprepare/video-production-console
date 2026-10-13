package openaicompat

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"
)

// 与 remixlab.Brief 使用同一 JSON 形状，运行快照不反向依赖存储层。
type briefSpec struct {
	Industry      string `json:"industry"`
	Audience      string `json:"audience"`
	Offer         string `json:"offer"`
	SellingPoints string `json:"selling_points"`
	Conversion    string `json:"conversion"`
	Persona       string `json:"persona"`
	Platform      string `json:"platform"`
	DurationS     int    `json:"duration_s"`
	Banned        string `json:"banned"`
	Notes         string `json:"notes"`
}

func (b *briefSpec) render() string {
	if b == nil {
		return ""
	}
	var lines []string
	add := func(label, value string) {
		if value = strings.TrimSpace(value); value != "" {
			lines = append(lines, label+"："+value)
		}
	}
	add("行业/品类", b.Industry)
	add("目标观众", b.Audience)
	add("产品/服务", b.Offer)
	add("真实卖点与可用事实", b.SellingPoints)
	add("转化动作", b.Conversion)
	add("出镜人设与口吻", b.Persona)
	add("平台", b.Platform)
	if b.DurationS > 0 {
		lines = append(lines, fmt.Sprintf("目标时长约 %d 秒，按每分钟约 220–260 字口播折算约 %d–%d 字", b.DurationS, b.DurationS*220/60, b.DurationS*260/60))
	}
	add("禁用词/不能说的话", b.Banned)
	add("其他要求", b.Notes)
	if len(lines) == 0 {
		return ""
	}
	return "【内容简报｜事实边界】\n" + strings.Join(lines, "\n")
}

func (b *briefSpec) hash() string {
	if b == nil {
		return ""
	}
	raw, _ := json.Marshal(b)
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])[:12]
}

func (b *briefSpec) bannedTerms() []string {
	if b == nil {
		return nil
	}
	var terms []string
	for _, line := range strings.FieldsFunc(b.Banned, func(r rune) bool { return r == '\r' || r == '\n' }) {
		if term := strings.TrimSpace(line); term != "" && !containsExact(terms, term) {
			terms = append(terms, term)
		}
	}
	return terms
}

func bannedWordWarnings(script string, brief *briefSpec) []string {
	var found []string
	for _, term := range brief.bannedTerms() {
		if strings.Contains(script, term) {
			found = append(found, term)
		}
	}
	if len(found) == 0 {
		return nil
	}
	return []string{"正文出现禁用词：" + strings.Join(found, "、")}
}

func injectBrief(user, brief string) string {
	if brief == "" {
		return user
	}
	return injectIntel(user, brief)
}

const universalReviewerPrompt = "依据内容简报与原文事实审稿，核对正文、发布字段与转化动作，只修有依据的问题。"
