package aishorts

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"video-production-console/internal/agentruntime/openaicompat"
	"video-production-console/internal/narration"
	"video-production-console/internal/spokenlines"
)

// 竖版 AI 短片的字幕走混剪系统同一套口播稿：文本模型按 spokenlines 的提示词把整篇解说
// 切成一行一屏（≤9 个实字、不拆词、数字转阿拉伯数字），再用逐字对齐的时间轴给每行定时。
// 模型不可用时退回 splitClauses 的规则切分，出片不受影响。

const spokenLinesFile = "spoken_lines.json"

// ChatClientFactory 由 httpapi 注入：按运行时连接信息建文本模型客户端。为空时用 HTTP 客户端。
type ChatClientFactory func(rt Runtime) openaicompat.ChatClient

type spokenLinesCache struct {
	ScriptSHA string   `json:"script_sha"`
	Model     string   `json:"model"`
	Lines     []string `json:"lines"`
}

// spokenLinesFor 返回整篇解说的口播稿行；按脚本指纹缓存在短片目录，脚本不变就复用。
func (a *DraftAssembler) spokenLinesFor(ctx context.Context, rt Runtime, short *Short, assetDir, script string, progress func(string)) []string {
	digest := scriptDigest(script)
	cachePath := filepath.Join(assetDir, spokenLinesFile)
	if raw, err := os.ReadFile(cachePath); err == nil {
		var cached spokenLinesCache
		if json.Unmarshal(raw, &cached) == nil && cached.ScriptSHA == digest && len(cached.Lines) > 0 {
			return cached.Lines
		}
	}
	model := strings.TrimSpace(short.TextModel)
	if model == "" {
		model = strings.TrimSpace(rt.Models.Text)
	}
	if model == "" || strings.TrimSpace(rt.BaseURL) == "" {
		return nil
	}
	var chat openaicompat.ChatClient
	if a.Chat != nil {
		chat = a.Chat(rt)
	} else {
		chat = &openaicompat.HTTPChatClient{BaseURL: rt.BaseURL, APIKey: rt.APIKey}
	}
	progress("按口播稿切字幕行")
	lines, err := generateSpokenLines(ctx, chat, model, script)
	if err != nil {
		slog.Default().Warn("ai short: spoken lines unavailable, falling back to rule-based captions", "id", short.ID, "error", err)
		return nil
	}
	if raw, err := json.Marshal(spokenLinesCache{ScriptSHA: digest, Model: model, Lines: lines}); err == nil {
		_ = os.WriteFile(cachePath, raw, 0o644)
	}
	return lines
}

// generateSpokenLines 复用混剪的口播稿链路：按句子边界切块并发请求，按序拼回，再 Format 成标准行。
func generateSpokenLines(ctx context.Context, chat openaicompat.ChatClient, model, script string) ([]string, error) {
	chunks := spokenlines.SplitForParallel(script)
	if len(chunks) == 0 {
		return nil, errors.New("script is empty")
	}
	results := make([]string, len(chunks))
	errs := make([]error, len(chunks))
	var wg sync.WaitGroup
	for i, chunk := range chunks {
		wg.Add(1)
		go func(i int, chunk string) {
			defer wg.Done()
			if ctx.Err() != nil {
				errs[i] = ctx.Err()
				return
			}
			resp, err := chat.Chat(openaicompat.ChatRequest{
				Model:           model,
				Stream:          true,
				ReasoningEffort: "low",
				Messages: []openaicompat.Message{
					{Role: "system", Content: spokenlines.SystemPrompt},
					{Role: "user", Content: spokenlines.UserPrompt(chunk)},
				},
			})
			if err != nil {
				errs[i] = err
				return
			}
			if len(resp.Choices) == 0 {
				errs[i] = errors.New("empty chat choices")
				return
			}
			results[i] = strings.TrimSpace(resp.Choices[0].Message.Content)
		}(i, chunk)
	}
	wg.Wait()
	for i, err := range errs {
		if err != nil {
			return nil, fmt.Errorf("spoken chunk %d/%d: %w", i+1, len(chunks), err)
		}
	}
	formatted, err := spokenlines.Format(strings.Join(results, "\n"))
	if err != nil {
		return nil, err
	}
	lines := spokenlines.Lines(formatted)
	// 模型漏掉一大截就不用它：对不上的行会挤在一起闪过。
	if got, want := coverageRunes(lines), contentRuneCount(script); got < want*9/10 {
		return nil, fmt.Errorf("spoken lines cover %d of %d runes", got, want)
	}
	return lines, nil
}

func coverageRunes(lines []string) int {
	n := 0
	for _, line := range lines {
		n += substantiveRunes(line)
	}
	return n
}

func contentRuneCount(s string) int { return substantiveRunes(s) }

// contentRunesOf 是 substantiveRunes 的字符版：和 charTimes 的下标用同一套过滤规则。
func contentRunesOf(s string) []rune {
	out := make([]rune, 0, len(s))
	for _, r := range s {
		if !isPunctOrSpace(r) {
			out = append(out, r)
		}
	}
	return out
}

// spokenLineCaptions 把口播稿行对到脚本实字流上，用逐字时间轴定时：一行一条字幕。
// 对不上的行（模型改写过头）夹在前后行之间，不丢字也不重叠；行跨镜时关键词按所在镜取。
func spokenLineCaptions(lines []string, ready []Shot, script string, charTimes [][2]float64, total float64, keywordsOn bool) []jobCaption {
	stream := contentRunesOf(script)
	if len(stream) == 0 || len(charTimes) == 0 {
		return nil
	}
	spans := narration.AlignLinesToRunes(lines, stream, contentRunesOf)
	// 每个实字下标属于哪一镜。
	shotAt := make([]int, len(stream))
	pos := 0
	for i, shot := range ready {
		for k := 0; k < substantiveRunes(shot.Narration) && pos < len(stream); k++ {
			shotAt[pos] = i
			pos++
		}
	}
	for ; pos < len(stream); pos++ {
		shotAt[pos] = len(ready) - 1
	}
	clampIdx := func(i int) int {
		if i < 0 {
			return 0
		}
		if i >= len(charTimes) {
			return len(charTimes) - 1
		}
		return i
	}
	caps := make([]jobCaption, 0, len(lines))
	lastEnd := 0.0
	for li, line := range lines {
		if substantiveRunes(line) == 0 {
			continue
		}
		first, last := spans[li][0], spans[li][1]
		cap := jobCaption{Text: line}
		if first < 0 {
			// 没对上：从上一条结束处起，长度等下一条对上的行来定；先记零长，后面补齐。
			cap.StartS, cap.EndS = lastEnd, lastEnd
		} else {
			cap.StartS = charTimes[clampIdx(first)][0]
			cap.EndS = charTimes[clampIdx(last)][1]
			if cap.StartS < lastEnd {
				cap.StartS = lastEnd
			}
			if cap.EndS < cap.StartS {
				cap.EndS = cap.StartS
			}
			if keywordsOn {
				shot := ready[shotAt[clampIdx(first)]]
				cap.Keywords = normalizedKeywords(line, append([]ShotKeyword{}, shot.Keywords...))
			}
		}
		lastEnd = cap.EndS
		caps = append(caps, cap)
	}
	// 零长的行按字数从相邻区间里分一段出来。
	for i := range caps {
		if caps[i].EndS > caps[i].StartS {
			continue
		}
		next := total
		for j := i + 1; j < len(caps); j++ {
			if caps[j].EndS > caps[j].StartS {
				next = caps[j].StartS
				break
			}
		}
		if next <= caps[i].StartS && i > 0 {
			// 前一条让出一部分。
			prev := &caps[i-1]
			share := (prev.EndS - prev.StartS) * float64(substantiveRunes(caps[i].Text)) / float64(substantiveRunes(prev.Text)+substantiveRunes(caps[i].Text))
			prev.EndS -= share
			caps[i].StartS, caps[i].EndS = prev.EndS, prev.EndS+share
			continue
		}
		caps[i].EndS = next
	}
	return caps
}
