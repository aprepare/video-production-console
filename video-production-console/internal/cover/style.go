package cover

import (
	"encoding/json"
	"errors"
	"fmt"
	"image/color"
	"math"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"
)

var (
	ErrNoTemplate   = errors.New("account has no cover template")
	ErrInvalidTitle = errors.New("cover title must contain 1 to 12 characters without punctuation or whitespace")
)

var defaultColors = [2]string{"#FFD166", "#F5F2E9"}
var defaultSeparators = [2]float64{0.34, 0.53}

// DefaultOutputDir 只计算路径；生成封面时才创建目录。
func DefaultOutputDir() string {
	home := strings.TrimSpace(os.Getenv("USERPROFILE"))
	if home == "" {
		home, _ = os.UserHomeDir()
	}
	return filepath.Join(home, "Videos", "封面图")
}

type AccountStyle struct {
	Account        string
	ReferencePath  string
	Colors         [2]string
	SeparatorLines [2]float64
	TitleBox       [2]int
}

// NormalizeTitle 接受粘贴进来的标点；清理后仍须是可放进一行的短标题。
func NormalizeTitle(text string) (string, error) {
	text = strings.TrimSpace(strings.Map(func(r rune) rune {
		if unicode.IsPunct(r) {
			return -1
		}
		return r
	}, strings.TrimSpace(text)))
	if !utf8.ValidString(text) || utf8.RuneCountInString(text) < 1 || utf8.RuneCountInString(text) > 12 || strings.ContainsFunc(text, func(r rune) bool {
		return unicode.IsSpace(r) || unicode.IsSymbol(r) || unicode.IsControl(r)
	}) {
		return "", ErrInvalidTitle
	}
	return text, nil
}

type accountProfile struct {
	Account        string `json:"account"`
	ConsoleID      string `json:"console_id"`
	CoverReference string `json:"cover_reference"`
	CoverStyle     string `json:"cover_style"`
}

// ResolveAccountStyle 按 console_id 找账号；技能里没登记 console_id 的账号按 accountName 兜底。
func ResolveAccountStyle(skillRoot, accountID, accountName string) (AccountStyle, error) {
	accountID, accountName = strings.TrimSpace(accountID), strings.TrimSpace(accountName)
	if accountID == "" && accountName == "" {
		return AccountStyle{}, ErrNoTemplate
	}
	if strings.TrimSpace(skillRoot) == "" {
		home := strings.TrimSpace(os.Getenv("USERPROFILE"))
		if home == "" {
			var err error
			home, err = os.UserHomeDir()
			if err != nil {
				return AccountStyle{}, err
			}
		}
		skillRoot = filepath.Join(home, ".codex", "skills", "multi-account-video-production")
	}
	accounts, err := filepath.Abs(filepath.Join(skillRoot, "accounts"))
	if err != nil {
		return AccountStyle{}, err
	}
	dirs, err := os.ReadDir(accounts)
	if os.IsNotExist(err) {
		return AccountStyle{}, ErrNoTemplate
	}
	if err != nil {
		return AccountStyle{}, fmt.Errorf("read cover accounts: %w", err)
	}
	var fallbackDir string
	var fallback accountProfile
	for _, dir := range dirs {
		if !dir.IsDir() {
			continue
		}
		accountDir := filepath.Join(accounts, dir.Name())
		raw, err := os.ReadFile(filepath.Join(accountDir, "profile.json"))
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			return AccountStyle{}, fmt.Errorf("read cover profile: %w", err)
		}
		var profile accountProfile
		if err := json.Unmarshal(raw, &profile); err != nil {
			return AccountStyle{}, fmt.Errorf("decode cover profile %s: %w", dir.Name(), err)
		}
		if profile.Account = strings.TrimSpace(profile.Account); profile.Account == "" {
			profile.Account = dir.Name()
		}
		consoleID := strings.TrimSpace(profile.ConsoleID)
		if accountID != "" && consoleID == accountID {
			return loadAccountStyle(accountDir, profile)
		}
		if fallbackDir == "" && consoleID == "" && accountName != "" && (dir.Name() == accountName || profile.Account == accountName) {
			fallbackDir, fallback = accountDir, profile
		}
	}
	if fallbackDir != "" {
		return loadAccountStyle(fallbackDir, fallback)
	}
	return AccountStyle{}, ErrNoTemplate
}

func loadAccountStyle(accountDir string, profile accountProfile) (AccountStyle, error) {
	style := AccountStyle{Account: profile.Account, Colors: defaultColors, SeparatorLines: defaultSeparators}
	style.ReferencePath = accountFile(accountDir, profile.CoverReference, filepath.Join("cover-style", "cover-reference.png"))
	info, err := os.Stat(style.ReferencePath)
	if os.IsNotExist(err) || (err == nil && !info.Mode().IsRegular()) {
		return AccountStyle{}, ErrNoTemplate
	}
	if err != nil {
		return AccountStyle{}, fmt.Errorf("read cover reference: %w", err)
	}
	raw, err := os.ReadFile(accountFile(accountDir, profile.CoverStyle, filepath.Join("cover-style", "style.json")))
	if os.IsNotExist(err) {
		return style, nil
	}
	if err != nil {
		return AccountStyle{}, fmt.Errorf("read cover style: %w", err)
	}
	var config struct {
		Cover struct {
			TitleColors    []string  `json:"title_colors"`
			SeparatorLines []float64 `json:"separator_lines"`
			TitleBox       []int     `json:"title_box"`
		} `json:"cover"`
	}
	if err := json.Unmarshal(raw, &config); err != nil {
		return AccountStyle{}, fmt.Errorf("decode cover style: %w", err)
	}
	if len(config.Cover.TitleColors) > 0 {
		if len(config.Cover.TitleColors) != 2 {
			return AccountStyle{}, errors.New("cover.title_colors must contain two colors")
		}
		for i, value := range config.Cover.TitleColors {
			if _, err := parseColor(value); err != nil {
				return AccountStyle{}, err
			}
			style.Colors[i] = strings.ToUpper(value)
		}
	}
	if box := config.Cover.TitleBox; len(box) == 2 && box[1] > box[0] && box[0] >= 0 {
		style.TitleBox = [2]int{box[0], box[1]}
	}
	if len(config.Cover.SeparatorLines) > 0 {
		if len(config.Cover.SeparatorLines) != 2 {
			return AccountStyle{}, errors.New("cover.separator_lines must contain two ratios")
		}
		style.SeparatorLines = [2]float64{config.Cover.SeparatorLines[0], config.Cover.SeparatorLines[1]}
		if !validSeparators(style.SeparatorLines) {
			return AccountStyle{}, errors.New("cover.separator_lines must be increasing ratios between 0 and 1")
		}
	}
	return style, nil
}

func accountFile(dir, value, fallback string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		value = fallback
	}
	if filepath.IsAbs(value) {
		return filepath.Clean(value)
	}
	return filepath.Join(dir, value)
}

func validSeparators(lines [2]float64) bool {
	return !math.IsNaN(lines[0]) && !math.IsNaN(lines[1]) && lines[0] >= 0 && lines[0] < lines[1] && lines[1] < 1
}

func parseColor(value string) (color.NRGBA, error) {
	if len(value) != 7 || value[0] != '#' {
		return color.NRGBA{}, fmt.Errorf("invalid cover color %q: expected #RRGGBB", value)
	}
	n, err := strconv.ParseUint(value[1:], 16, 24)
	if err != nil {
		return color.NRGBA{}, fmt.Errorf("invalid cover color %q: %w", value, err)
	}
	return color.NRGBA{R: uint8(n >> 16), G: uint8(n >> 8), B: uint8(n), A: 255}, nil
}
