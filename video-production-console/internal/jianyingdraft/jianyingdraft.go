// Package jianyingdraft reads the user's own Jianying drafts and lifts the
// typography they styled by hand into a domain.MontageStyle, so an account's
// production can copy the look of a draft the operator tuned in Jianying.
package jianyingdraft

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"video-production-console/internal/domain"
)

// DraftInfo is one folder under the Jianying draft root.
type DraftInfo struct {
	Name       string    `json:"name"`
	ModifiedAt time.Time `json:"modified_at"`
	Encrypted  bool      `json:"encrypted"`
}

var ErrNoDecryptTool = errors.New("jy-draftc decrypt tool not found")

// ListDrafts returns draft folders newest first. Hidden/recycle folders and
// folders without draft_content.json are skipped.
func ListDrafts(root string) ([]DraftInfo, error) {
	entries, err := os.ReadDir(root)
	if err != nil {
		return nil, err
	}
	out := make([]DraftInfo, 0, len(entries))
	for _, entry := range entries {
		if !entry.IsDir() || strings.HasPrefix(entry.Name(), ".") {
			continue
		}
		content := filepath.Join(root, entry.Name(), "draft_content.json")
		stat, err := os.Stat(content)
		if err != nil {
			continue
		}
		info := DraftInfo{Name: entry.Name(), ModifiedAt: stat.ModTime()}
		if head, err := readHead(content, 64); err == nil {
			info.Encrypted = !looksLikeJSON(head)
		}
		out = append(out, info)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ModifiedAt.After(out[j].ModifiedAt) })
	return out, nil
}

func readHead(path string, n int) ([]byte, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	buf := make([]byte, n)
	read, err := f.Read(buf)
	if err != nil && read == 0 {
		return nil, err
	}
	return buf[:read], nil
}

func looksLikeJSON(head []byte) bool {
	trimmed := bytes.TrimLeft(head, " \t\r\n\ufeff")
	return len(trimmed) > 0 && (trimmed[0] == '{' || trimmed[0] == '[')
}

// FindDecryptTool locates jy-draftc.exe: env JY_DRAFTC_PATH, then the
// console data dir, then next to the executable, then the known dev path.
func FindDecryptTool(dataRoot string) string {
	candidates := []string{strings.TrimSpace(os.Getenv("JY_DRAFTC_PATH"))}
	if dataRoot != "" {
		candidates = append(candidates, filepath.Join(dataRoot, "tools", "jy-draftc.exe"))
	}
	if exe, err := os.Executable(); err == nil {
		candidates = append(candidates, filepath.Join(filepath.Dir(exe), "tools", "jy-draftc.exe"))
	}
	candidates = append(candidates, `D:\finance-video-system\tools\jy-draftc\dist\jy-draftc-amd64-windows\jy-draftc.exe`)
	for _, candidate := range candidates {
		if candidate == "" {
			continue
		}
		if stat, err := os.Stat(candidate); err == nil && !stat.IsDir() {
			return candidate
		}
	}
	return ""
}

// LoadContent returns the plaintext draft_content.json of a draft. Encrypted
// drafts are decrypted through jy-draftc into a temp file; the source draft
// is never modified.
func LoadContent(ctx context.Context, root, name, tool string) (map[string]any, error) {
	if strings.ContainsAny(name, `/\`) || name == "." || name == ".." {
		return nil, fmt.Errorf("invalid draft name %q", name)
	}
	src := filepath.Join(root, name, "draft_content.json")
	raw, err := os.ReadFile(src)
	if err != nil {
		return nil, err
	}
	if !looksLikeJSON(raw[:min(len(raw), 64)]) {
		if tool == "" {
			return nil, ErrNoDecryptTool
		}
		tmp, err := os.CreateTemp("", "jy-draft-*.json")
		if err != nil {
			return nil, err
		}
		tmpPath := tmp.Name()
		tmp.Close()
		defer os.Remove(tmpPath)
		cmd := exec.CommandContext(ctx, tool, "-d", src, tmpPath)
		cmd.Dir = filepath.Dir(tool)
		if out, err := cmd.CombinedOutput(); err != nil {
			return nil, fmt.Errorf("jy-draftc failed: %v: %s", err, strings.TrimSpace(string(out)))
		}
		raw, err = os.ReadFile(tmpPath)
		if err != nil {
			return nil, err
		}
	}
	var content map[string]any
	if err := json.Unmarshal(raw, &content); err != nil {
		return nil, fmt.Errorf("draft content is not valid JSON: %w", err)
	}
	return content, nil
}

// Extraction is the style read from a draft plus what the reader noticed.
type Extraction struct {
	Style    domain.MontageStyle `json:"style"`
	Notes    []string            `json:"notes"`
	Timeline string              `json:"timeline"` // "combination" when the draft was a nested compound clip
	// BGM the draft uses. Jianying keeps library music in its local cache, so
	// the path is a real file that can be copied into the console BGM library.
	BGMName string `json:"bgm_name,omitempty"`
	BGMPath string `json:"bgm_path,omitempty"`
}

// DraftBGM returns the first library-music track of the draft and the cache
// file it points at ("" when the file is not on this machine).
func DraftBGM(content map[string]any) (name, path string) {
	timeline := descend(content, &Extraction{})
	materials, _ := timeline["materials"].(map[string]any)
	audios, _ := materials["audios"].([]any)
	for _, raw := range audios {
		audio, _ := raw.(map[string]any)
		if str(audio["type"]) != "music" {
			continue
		}
		name = str(audio["name"])
		candidate := strings.TrimPrefix(str(audio["path"]), `\\?\`)
		candidate = filepath.FromSlash(candidate)
		if stat, err := os.Stat(candidate); err == nil && !stat.IsDir() {
			return name, candidate
		}
		return name, ""
	}
	return "", ""
}

type textRun struct {
	size        float64
	color       string
	borderColor string
	borderWidth float64 // Jianying 0-100
	font        string
}

type textMaterial struct {
	runs        []textRun
	font        string
	bgColor     string
	bgAlpha     float64
	borderColor string
	borderWidth float64
}

// ExtractStyle lifts caption / title / subtitle typography from the draft.
// Caption = the text track with the most segments; title / subtitle = the
// single-segment text tracks (by name when present, otherwise by height).
func ExtractStyle(content map[string]any) Extraction {
	ex := Extraction{}
	timeline := descend(content, &ex)
	materials, _ := timeline["materials"].(map[string]any)
	texts := indexTexts(materials)
	tracks, _ := timeline["tracks"].([]any)

	type textTrack struct {
		name string
		segs []any
	}
	var textTracks []textTrack
	for _, raw := range tracks {
		track, _ := raw.(map[string]any)
		if str(track["type"]) != "text" {
			continue
		}
		segs, _ := track["segments"].([]any)
		if len(segs) == 0 {
			continue
		}
		textTracks = append(textTracks, textTrack{name: str(track["name"]), segs: segs})
	}
	if len(textTracks) == 0 {
		ex.Notes = append(ex.Notes, "草稿里没有文本轨，未提取任何样式")
		return ex
	}

	// Caption track: the one named 字幕, else the multi-segment track with
	// the most segments. Every one-segment track is a title candidate.
	var caption *textTrack
	for i := range textTracks {
		t := &textTracks[i]
		if t.name == "字幕" {
			caption = t
			break
		}
		if len(t.segs) > 1 && (caption == nil || len(t.segs) > len(caption.segs)) {
			caption = t
		}
	}
	var singles []textTrack
	for i := range textTracks {
		if len(textTracks[i].segs) == 1 && &textTracks[i] != caption {
			singles = append(singles, textTracks[i])
		}
	}

	style := domain.MontageStyle{}
	if caption != nil {
		extractCaption(caption.segs, texts, &style, &ex)
	} else {
		ex.Notes = append(ex.Notes, "没有找到多段字幕轨，字幕样式沿用默认")
	}

	// Title / subtitle: named tracks win; otherwise the higher one is the title.
	var title, subtitle *textTrack
	for i := range singles {
		switch singles[i].name {
		case "标题":
			title = &singles[i]
		case "副标题":
			subtitle = &singles[i]
		}
	}
	if title == nil && subtitle == nil && len(singles) > 0 {
		sort.Slice(singles, func(i, j int) bool { return segmentY(singles[i].segs[0]) > segmentY(singles[j].segs[0]) })
		title = &singles[0]
		if len(singles) > 1 {
			subtitle = &singles[1]
		}
	}
	if title != nil {
		if mat, y, ok := materialOf(title.segs[0], texts); ok {
			applyBrand(mat, y, &style.TitleFont, &style.TitleSize, &style.TitleColor, &style.TitleY,
				&style.TitleBorderColor, &style.TitleBgColor, &style.TitleBgAlpha)
		}
	} else {
		style.TitleHidden = true
		ex.Notes = append(ex.Notes, "草稿没有标题轨，已按隐藏标题记录")
	}
	if subtitle != nil {
		if mat, y, ok := materialOf(subtitle.segs[0], texts); ok {
			applyBrand(mat, y, &style.SubtitleFont, &style.SubtitleSize, &style.SubtitleColor, &style.SubtitleY,
				&style.SubtitleBorderColor, &style.SubtitleBgColor, &style.SubtitleBgAlpha)
		}
	} else {
		style.SubtitleHidden = true
		ex.Notes = append(ex.Notes, "草稿没有副标题轨，已按隐藏副标题记录")
	}

	ex.BGMName, ex.BGMPath = DraftBGM(content)
	switch {
	case ex.BGMName != "" && ex.BGMPath != "":
		ex.Notes = append(ex.Notes, "草稿 BGM「"+ex.BGMName+"」已在剪映缓存里找到，可一键收进本地曲库选用")
	case ex.BGMName != "":
		ex.Notes = append(ex.Notes, "草稿 BGM「"+ex.BGMName+"」的缓存文件不在这台电脑上，BGM 请在账号配置里另选")
	}
	ex.Style = style
	return ex
}

// descend follows compound clips: a draft whose only clip is a combination
// keeps the real timeline at materials.drafts[0].draft.
func descend(content map[string]any, ex *Extraction) map[string]any {
	current := content
	for depth := 0; depth < 4; depth++ {
		materials, _ := current["materials"].(map[string]any)
		drafts, _ := materials["drafts"].([]any)
		if len(drafts) == 0 {
			return current
		}
		first, _ := drafts[0].(map[string]any)
		inner, _ := first["draft"].(map[string]any)
		if inner == nil {
			inner = first
		}
		if tracks, _ := inner["tracks"].([]any); len(tracks) == 0 {
			return current
		}
		current = inner
		ex.Timeline = "combination"
	}
	return current
}

func indexTexts(materials map[string]any) map[string]map[string]any {
	out := map[string]map[string]any{}
	texts, _ := materials["texts"].([]any)
	for _, raw := range texts {
		mat, _ := raw.(map[string]any)
		if id := str(mat["id"]); id != "" {
			out[id] = mat
		}
	}
	return out
}

func materialOf(seg any, texts map[string]map[string]any) (textMaterial, float64, bool) {
	segment, _ := seg.(map[string]any)
	mat, ok := texts[str(segment["material_id"])]
	if !ok {
		return textMaterial{}, 0, false
	}
	return parseTextMaterial(mat), segmentY(seg), true
}

func segmentY(seg any) float64 {
	segment, _ := seg.(map[string]any)
	clip, _ := segment["clip"].(map[string]any)
	transform, _ := clip["transform"].(map[string]any)
	return num(transform["y"])
}

func parseTextMaterial(mat map[string]any) textMaterial {
	out := textMaterial{
		bgColor:     normalizeHex(str(mat["background_color"])),
		bgAlpha:     1,
		borderColor: normalizeHex(str(mat["border_color"])),
	}
	if alpha, ok := mat["background_alpha"].(float64); ok && alpha > 0 {
		out.bgAlpha = alpha
	}
	if width := num(mat["border_width"]); width > 0 {
		out.borderWidth = draftBorderToJianying(width)
	}
	if fonts, _ := mat["fonts"].([]any); len(fonts) > 0 {
		font, _ := fonts[0].(map[string]any)
		out.font = pickDraftFont(str(font["title"]), str(font["path"]))
	}
	if out.font == "" {
		out.font = pickDraftFont(str(mat["font_title"]), str(mat["font_path"]))
	}
	var payload struct {
		Styles []map[string]any `json:"styles"`
	}
	if raw := str(mat["content"]); raw != "" {
		_ = json.Unmarshal([]byte(raw), &payload)
	}
	for _, s := range payload.Styles {
		run := textRun{size: num(s["size"])}
		if fill, ok := s["fill"].(map[string]any); ok {
			run.color = solidColor(fill)
		}
		if strokes, _ := s["strokes"].([]any); len(strokes) > 0 {
			stroke, _ := strokes[0].(map[string]any)
			run.borderColor = solidColor(stroke)
			if width := num(stroke["width"]); width > 0 {
				run.borderWidth = draftBorderToJianying(width)
			}
		}
		// The run's own font wins; the material-level font is often just the
		// system fallback on rich-text captions.
		if font, ok := s["font"].(map[string]any); ok {
			run.font = pickDraftFont(str(font["title"]), str(font["path"]))
		}
		if run.font == "" {
			run.font = out.font
		}
		out.runs = append(out.runs, run)
	}
	if out.font == "" {
		for _, run := range out.runs {
			if run.font != "" {
				out.font = run.font
				break
			}
		}
	}
	if out.borderColor == "" {
		for _, run := range out.runs {
			if run.borderColor != "" {
				out.borderColor = run.borderColor
				out.borderWidth = run.borderWidth
				break
			}
		}
	}
	return out
}

// extractCaption reads the dominant caption look, and the keyword highlight
// when captions carry a second, larger run.
func extractCaption(segs []any, texts map[string]map[string]any, style *domain.MontageStyle, ex *Extraction) {
	type key struct {
		size  float64
		color string
	}
	baseCount := map[key]int{}
	baseSample := map[key]textRun{}
	var bg textMaterial
	bgSeen := false
	keywordCount := map[key]int{}
	keywordSample := map[key]textRun{}
	plainCount := map[key]int{}
	multi := 0
	var ySum float64
	yCount := 0
	for _, seg := range segs {
		mat, y, ok := materialOf(seg, texts)
		if !ok || len(mat.runs) == 0 {
			continue
		}
		ySum += y
		yCount++
		if !bgSeen {
			bg, bgSeen = mat, true
		}
		if len(mat.runs) == 1 {
			k := key{roundSize(mat.runs[0].size), mat.runs[0].color}
			baseCount[k]++
			baseSample[k] = mat.runs[0]
			continue
		}
		multi++
		largest, smallest := mat.runs[0], mat.runs[0]
		for _, run := range mat.runs[1:] {
			if run.size > largest.size {
				largest = run
			}
			if run.size < smallest.size {
				smallest = run
			}
		}
		if largest.size == smallest.size {
			k := key{roundSize(largest.size), largest.color}
			baseCount[k]++
			baseSample[k] = largest
			continue
		}
		kk := key{roundSize(largest.size), largest.color}
		keywordCount[kk]++
		keywordSample[kk] = largest
		pk := key{roundSize(smallest.size), smallest.color}
		plainCount[pk]++
		baseSample[pk] = smallest
	}
	if len(baseCount) == 0 && len(plainCount) == 0 {
		ex.Notes = append(ex.Notes, "字幕轨没有可读的文字样式")
		return
	}
	// With keyword highlights present, whole-line keyword captions (a single
	// run in the keyword look) must not be mistaken for the base look.
	var kwKey key
	hasKeyword := len(keywordCount) > 0
	if hasKeyword {
		_, kwKey = mostCommon(keywordCount, keywordSample)
	}
	// Base look = the most common whole-line style that is not the keyword
	// look (lines without highlights keep full size, e.g. 20 vs plain 17).
	// Only when every line carries a highlight do the plain runs stand in.
	candidates := map[key]int{}
	for k, n := range baseCount {
		if !hasKeyword || k != kwKey {
			candidates[k] += n
		}
	}
	if len(candidates) == 0 {
		for k, n := range plainCount {
			candidates[k] += n
		}
	}
	if len(candidates) == 0 {
		candidates = baseCount
	}
	base, baseKey := mostCommon(candidates, baseSample)
	style.CaptionSize = baseKey.size
	style.CaptionColor = base.color
	style.CaptionFont = base.font
	if base.borderColor != "" {
		style.CaptionBorderColor = base.borderColor
		style.CaptionBorderWidth = base.borderWidth
	} else {
		style.CaptionBorderHidden = true
	}
	if bgSeen && bg.bgColor != "" {
		style.CaptionBgColor = bg.bgColor
		style.CaptionBgAlpha = bg.bgAlpha
	}
	if yCount > 0 {
		style.CaptionPosition, style.CaptionY = captionPosition(ySum / float64(yCount))
	}
	if hasKeyword {
		kw := keywordSample[kwKey]
		style.KeywordSize = kwKey.size
		style.KeywordColor = kw.color
		if kw.borderColor != "" {
			style.KeywordBorderColor = kw.borderColor
		}
		if len(plainCount) > 0 {
			_, plainKey := mostCommon(plainCount, baseSample)
			style.PlainSize = plainKey.size
		}
		ex.Notes = append(ex.Notes, fmt.Sprintf("字幕里 %d 段带关键词高亮（%.0f 号 %s），已开启关键词标注", multi, kwKey.size, kw.color))
	} else {
		style.KeywordsHidden = true
		ex.Notes = append(ex.Notes, "字幕全部单一样式，已关闭关键词标注")
	}
}

func applyBrand(mat textMaterial, y float64, font *string, size *float64, color *string, yOut *float64,
	border, bgColor *string, bgAlpha *float64) {
	if len(mat.runs) > 0 {
		run := mat.runs[0]
		*size = roundSize(run.size)
		if run.color != "" {
			*color = run.color
		}
	}
	*font = mat.font
	*yOut = math.Round(y*1000) / 1000
	if mat.borderColor != "" {
		*border = mat.borderColor
	}
	if mat.bgColor != "" {
		*bgColor = mat.bgColor
		*bgAlpha = mat.bgAlpha
	}
}

func mostCommon[K comparable](counts map[K]int, samples map[K]textRun) (textRun, K) {
	var best K
	bestN := -1
	for k, n := range counts {
		if n > bestN {
			best, bestN = k, n
		}
	}
	return samples[best], best
}

func captionPosition(y float64) (string, float64) {
	switch {
	case math.Abs(y) < 0.02:
		return "middle", 0
	case math.Abs(y+0.3) < 0.02:
		return "bottom", 0
	default:
		return "custom", math.Round(y*1000) / 1000
	}
}

// draftBorderToJianying maps the draft's 0..0.2 stroke width to Jianying's
// 0..100 slider (the inverse of TextBorder in pyJianYingDraft).
func draftBorderToJianying(width float64) float64 {
	return math.Round(width/0.2*100*10) / 10
}

func pickDraftFont(title, path string) string {
	if name := domain.NormalizeMontageFont(title); name != "" {
		return name
	}
	return domain.NormalizeMontageFont(fontFromPath(path))
}

func fontFromPath(path string) string {
	base := filepath.Base(strings.ReplaceAll(path, "\\", "/"))
	base = strings.TrimSuffix(base, filepath.Ext(base))
	base = strings.TrimSpace(base)
	// System fallback font carries no style choice.
	if base == "" || strings.HasPrefix(strings.ToLower(base), "zh-") {
		return ""
	}
	// "新青年体-文跃新青年体" is the same family as FontType "新青年体".
	if idx := strings.Index(base, "-"); idx > 0 {
		base = base[:idx]
	}
	return strings.TrimSpace(base)
}

func solidColor(container map[string]any) string {
	content, _ := container["content"].(map[string]any)
	solid, _ := content["solid"].(map[string]any)
	rgb, _ := solid["color"].([]any)
	if len(rgb) < 3 {
		return ""
	}
	channel := func(v any) int {
		return int(math.Round(math.Max(0, math.Min(1, num(v))) * 255))
	}
	return fmt.Sprintf("#%02X%02X%02X", channel(rgb[0]), channel(rgb[1]), channel(rgb[2]))
}

func normalizeHex(hex string) string {
	hex = strings.TrimSpace(hex)
	if len(hex) != 7 || hex[0] != '#' {
		return ""
	}
	return strings.ToUpper(hex)
}

func roundSize(size float64) float64 {
	return math.Round(size*10) / 10
}

func str(v any) string {
	s, _ := v.(string)
	return s
}

func num(v any) float64 {
	switch n := v.(type) {
	case float64:
		return n
	case int:
		return float64(n)
	case json.Number:
		f, _ := n.Float64()
		return f
	}
	return 0
}
