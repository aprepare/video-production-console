package httpapi

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"

	"video-production-console/internal/aishorts"
	"video-production-console/internal/bgmlibrary"
	"video-production-console/internal/domain"
)

// 机器模板里混剪验证过的内置 BGM / 音效缓存键（见 jianying-montage-draft 的 audio case）。
const (
	builtinBGMCacheKey       = "bgm_yawaraka_hikari"
	builtinBGMUsableHeadS    = 313.7
	builtinBGMClimaxStartS   = 67.3
	builtinBGMClimaxDuration = 57.633
)

var sfxCacheKeys = map[string]string{
	"opening":    "sfx_opening_hit",
	"drop":       "sfx_water_drop",
	"whoosh":     "sfx_whoosh",
	"conclusion": "sfx_conclusion_hit",
}

// newAIShortsBrandResolver 把混剪那套账号包装搬给 AI 短片：
// 账号背景图、账号/全局混剪样式里的 BGM 与字幕样式、机器模板缓存里的内置 BGM 和音效。
// 任何一项缺失都只是少一项，不阻塞出片。
func NewAIShortsBrandResolver(dataRoot string, runtime AssetRuntimeProvider, accounts accountStore) func(ctx context.Context, accountID string) (aishorts.BrandKit, error) {
	return func(ctx context.Context, accountID string) (aishorts.BrandKit, error) {
		kit := aishorts.DefaultBrandKit()
		rt, err := runtime.Runtime(ctx)
		if err != nil {
			return kit, err
		}
		style := rt.MontageStyle
		if accounts != nil && strings.TrimSpace(accountID) != "" {
			if account, err := accounts.Get(ctx, accountID); err == nil {
				if account.BackgroundPath != nil && fileExists(*account.BackgroundPath) {
					kit.BackgroundPath = *account.BackgroundPath
				}
				if account.Overrides != nil && account.Overrides.MontageStyle != nil {
					style = *account.Overrides.MontageStyle
				}
			}
		}
		style = style.Normalized()

		// 字幕样式：竖版内嵌布局里字幕压在画面底边，字号按混剪默认缩一点。
		if style.CaptionFont != "" {
			kit.CaptionFont = style.CaptionFont
		}
		if style.CaptionBgColor != "" {
			kit.CaptionBgColor = style.CaptionBgColor
			if style.CaptionBgAlpha > 0 {
				kit.CaptionBgAlpha = style.CaptionBgAlpha
			}
		}

		cache := loadMachineCachePaths(rt.MachineProfilePath)

		// BGM：账号选的库曲 → 内置 → 无。
		if style.BGMVolume > 0 {
			kit.BGMVolume = style.BGMVolume
		}
		switch {
		case style.BGMID != "" && style.BGMID != domain.BuiltinBGMID:
			index := bgmlibrary.LoadIndex(filepath.Join(dataRoot, bgmlibrary.IndexFileName))
			if track, ok := bgmlibrary.FindTrack(index, style.BGMID); ok && fileExists(track.Path) {
				kit.BGMPath = track.Path
				kit.BGMUsableHeadS, kit.BGMClimaxStartS, kit.BGMClimaxDurationS = track.UsableHeadS, track.ClimaxStartS, track.ClimaxDurationS
			}
		}
		if kit.BGMPath == "" {
			if p := cache[builtinBGMCacheKey]; fileExists(p) {
				kit.BGMPath = p
				kit.BGMUsableHeadS, kit.BGMClimaxStartS, kit.BGMClimaxDurationS = builtinBGMUsableHeadS, builtinBGMClimaxStartS, builtinBGMClimaxDuration
			}
		}

		kit.SFX = map[string]string{}
		for role, key := range sfxCacheKeys {
			if p := cache[key]; fileExists(p) {
				kit.SFX[role] = p
			}
		}
		return kit, nil
	}
}

// loadMachineCachePaths 读机器模板的 cache_paths（剪映本机缓存里的音乐/音效文件位置）。
func loadMachineCachePaths(profilePath string) map[string]string {
	out := map[string]string{}
	if strings.TrimSpace(profilePath) == "" {
		return out
	}
	raw, err := os.ReadFile(profilePath)
	if err != nil {
		return out
	}
	var profile struct {
		CachePaths map[string]string `json:"cache_paths"`
	}
	if json.Unmarshal(raw, &profile) != nil {
		return out
	}
	for k, v := range profile.CachePaths {
		out[k] = v
	}
	return out
}
