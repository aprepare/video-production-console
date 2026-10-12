package jianyingdraft

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
)

// jianyingVersionDir matches an install folder such as 11.1.0.14287.
var jianyingVersionDir = regexp.MustCompile(`^\d+\.\d+\.\d+\.\d+$`)

// VerifiedJianyingVersions is the major.minor list this console has checked.
// Go cannot const a slice; treat this variable as that fixed list.
// Source, read-only on 2026-10-12: D:\JianyingPro\11.1.0.14287\JianyingPro.exe.
// The draft root D:\JianyingPro\AppData\User Data\Projects\com.lveditor.draft
// walks up to User Data; the version folder sits beside AppData, not under Apps/.
var VerifiedJianyingVersions = []string{"11.1"}

// InstalledVersion is a Jianying version and where it was read from.
type InstalledVersion struct {
	Version string `json:"version"`
	Source  string `json:"source"`
	Note    string `json:"note"`
}

// DetectInstalledVersion finds the installed Jianying version for jianyingRoot.
func DetectInstalledVersion(jianyingRoot string) (InstalledVersion, error) {
	root := strings.TrimSpace(jianyingRoot)
	if root == "" {
		return InstalledVersion{Note: "未配置剪映草稿目录。"}, nil
	}
	if version, ok := installedVersionNearDraftRoot(root); ok {
		return InstalledVersion{
			Version: version,
			Source:  "install_dir",
			Note:    "从安装目录版本文件夹里的 JianyingPro.exe 读取。",
		}, nil
	}
	version, foundFile, invalid, err := versionFromRootMeta(root)
	if err != nil {
		return InstalledVersion{}, err
	}
	if version != "" {
		return InstalledVersion{
			Version: version,
			Source:  "root_meta_info",
			Note:    "从 root_meta_info.json 的 draft_new_version 读取。",
		}, nil
	}
	if invalid {
		return InstalledVersion{Note: "未找到安装目录版本，且 root_meta_info.json 无法解析。"}, nil
	}
	if foundFile {
		return InstalledVersion{Note: "未找到安装目录版本，root_meta_info.json 里也没有可用的版本字段。"}, nil
	}
	return InstalledVersion{Note: "未找到剪映安装版本，也没有 root_meta_info.json。"}, nil
}

// CompatibleWithVerified reports whether installed shares a verified major.minor.
func CompatibleWithVerified(installed string, verifiedMajorMinor []string) bool {
	got, ok := majorMinor(installed)
	if !ok {
		return false
	}
	for _, candidate := range verifiedMajorMinor {
		want, ok := majorMinor(candidate)
		if ok && want == got {
			return true
		}
	}
	return false
}

func installedVersionNearDraftRoot(jianyingRoot string) (string, bool) {
	userData, ok := findAncestorNamed(jianyingRoot, "User Data", 8)
	if !ok {
		return "", false
	}
	appData := filepath.Dir(userData)
	product := filepath.Dir(appData)
	dirs := []string{
		product,
		filepath.Join(product, "Apps"),
		appData,
		filepath.Join(appData, "Apps"),
	}
	if entries, err := os.ReadDir(appData); err == nil {
		for _, entry := range entries {
			if !entry.IsDir() || strings.EqualFold(entry.Name(), "User Data") {
				continue
			}
			sibling := filepath.Join(appData, entry.Name())
			dirs = append(dirs, sibling, filepath.Join(sibling, "Apps"))
		}
	}
	best := ""
	seen := map[string]struct{}{}
	for _, dir := range dirs {
		dir = filepath.Clean(dir)
		if _, ok := seen[dir]; ok {
			continue
		}
		seen[dir] = struct{}{}
		if version := maxVersionWithExe(dir); versionGreater(version, best) {
			best = version
		}
	}
	return best, best != ""
}

func findAncestorNamed(start, name string, limit int) (string, bool) {
	dir := filepath.Clean(start)
	for i := 0; i < limit; i++ {
		if strings.EqualFold(filepath.Base(dir), name) {
			return dir, true
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
		dir = parent
	}
	return "", false
}

func maxVersionWithExe(dir string) string {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return ""
	}
	best := ""
	for _, entry := range entries {
		if !entry.IsDir() || !jianyingVersionDir.MatchString(entry.Name()) {
			continue
		}
		exe := filepath.Join(dir, entry.Name(), "JianyingPro.exe")
		info, err := os.Stat(exe)
		if err != nil || info.IsDir() {
			continue
		}
		if versionGreater(entry.Name(), best) {
			best = entry.Name()
		}
	}
	return best
}

func versionFromRootMeta(root string) (version string, foundFile bool, invalid bool, err error) {
	raw, err := os.ReadFile(filepath.Join(root, "root_meta_info.json"))
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return "", false, false, nil
		}
		return "", false, false, err
	}
	var doc map[string]json.RawMessage
	if json.Unmarshal(raw, &doc) != nil {
		return "", true, true, nil
	}
	best := ""
	consider := func(value string) {
		value = strings.TrimSpace(value)
		if parseVersion(value) == nil {
			return
		}
		if versionGreater(value, best) {
			best = value
		}
	}
	for key, rawValue := range doc {
		if !strings.Contains(strings.ToLower(key), "version") {
			continue
		}
		var text string
		if json.Unmarshal(rawValue, &text) == nil {
			consider(text)
		}
	}
	if rawStore, ok := doc["all_draft_store"]; ok {
		var store []map[string]any
		if json.Unmarshal(rawStore, &store) == nil {
			for _, item := range store {
				if text, ok := item["draft_new_version"].(string); ok {
					consider(text)
				}
			}
		}
	}
	return best, true, false, nil
}

func majorMinor(version string) (string, bool) {
	parts := parseVersion(version)
	if len(parts) < 2 {
		return "", false
	}
	return strconv.Itoa(parts[0]) + "." + strconv.Itoa(parts[1]), true
}

func parseVersion(version string) []int {
	version = strings.TrimSpace(version)
	if version == "" {
		return nil
	}
	parts := strings.Split(version, ".")
	nums := make([]int, 0, len(parts))
	for _, part := range parts {
		if part == "" {
			return nil
		}
		n, err := strconv.Atoi(part)
		if err != nil || n < 0 {
			return nil
		}
		nums = append(nums, n)
	}
	return nums
}

func versionGreater(a, b string) bool {
	if strings.TrimSpace(a) == "" {
		return false
	}
	if strings.TrimSpace(b) == "" {
		return true
	}
	aa, bb := parseVersion(a), parseVersion(b)
	if aa == nil {
		return false
	}
	if bb == nil {
		return true
	}
	n := len(aa)
	if len(bb) > n {
		n = len(bb)
	}
	for i := 0; i < n; i++ {
		av, bv := 0, 0
		if i < len(aa) {
			av = aa[i]
		}
		if i < len(bb) {
			bv = bb[i]
		}
		if av != bv {
			return av > bv
		}
	}
	return false
}
