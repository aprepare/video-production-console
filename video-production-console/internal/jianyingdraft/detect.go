package jianyingdraft

import (
	"bufio"
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"video-production-console/internal/localpath"
)

// Detection 是本机剪映环境的检测结果。
type Detection struct {
	Candidates []Candidate `json:"candidates"`
	Running    bool        `json:"running"`
}

// Candidate 是一个实际存在的剪映草稿目录。
type Candidate struct {
	DraftRoot       string `json:"draft_root"`
	InstallDir      string `json:"install_dir"`
	Version         string `json:"version"`
	Verified        bool   `json:"verified"`
	CustomDraftPath string `json:"custom_draft_path"`
	DraftCount      int    `json:"draft_count"`
	HasRootMeta     bool   `json:"has_root_meta"`
	Source          string `json:"source"`
}

type installation struct {
	Dir            string
	DisplayVersion string
}

type sourcePath struct {
	Path   string
	Source string
}

type detectionInputs struct {
	Registry      []installation
	LocalUserData string
	InstallDirs   []sourcePath
}

type candidateLocation struct {
	draftRoot       string
	installDir      string
	registryVersion string
	source          string
}

type rankedCandidate struct {
	Candidate
	versionRank int
}

func buildCandidates(in detectionInputs) []Candidate {
	locations := make([]candidateLocation, 0, len(in.Registry)+len(in.InstallDirs)+2)
	for _, item := range in.Registry {
		locations = append(locations, candidateLocation{
			draftRoot:       draftRootUnderInstall(item.Dir),
			installDir:      item.Dir,
			registryVersion: strings.TrimSpace(item.DisplayVersion),
			source:          "registry",
		})
	}

	if userData := strings.TrimSpace(in.LocalUserData); userData != "" {
		locations = append(locations, candidateLocation{
			draftRoot:  draftRootUnderUserData(userData),
			installDir: installDirForUserData(userData),
			source:     "localappdata",
		})
		// 目录联接解析后可能直接暴露真正的安装目录，再按安装目录补查一次。
		if realUserData := cleanRealPath(userData); !samePath(realUserData, userData) {
			installDir := installDirForUserData(realUserData)
			locations = append(locations, candidateLocation{
				draftRoot:  draftRootUnderInstall(installDir),
				installDir: installDir,
				source:     "install_dir",
			})
		}
	}

	for _, item := range in.InstallDirs {
		locations = append(locations, candidateLocation{
			draftRoot:  draftRootUnderInstall(item.Path),
			installDir: item.Path,
			source:     item.Source,
		})
	}

	inspected := make([]rankedCandidate, 0, len(locations))
	for _, location := range locations {
		candidate, versionRank, ok := inspectCandidate(location)
		if !ok {
			continue
		}
		inspected = append(inspected, rankedCandidate{Candidate: candidate, versionRank: versionRank})
	}
	return mergeAndSortCandidates(inspected)
}

func mergeAndSortCandidates(inspected []rankedCandidate) []Candidate {
	byRoot := make(map[string]rankedCandidate, len(inspected))
	for _, candidateWithRank := range inspected {
		candidate := candidateWithRank.Candidate
		versionRank := candidateWithRank.versionRank
		key := pathKey(candidate.DraftRoot)
		current, exists := byRoot[key]
		if !exists {
			byRoot[key] = candidateWithRank
			continue
		}
		candidatePriority := sourcePriority(candidate.Source)
		currentPriority := sourcePriority(current.Source)
		if candidatePriority < currentPriority {
			current.Source = candidate.Source
		}
		if current.InstallDir == "" || candidatePriority < currentPriority {
			current.InstallDir = candidate.InstallDir
		}
		if versionRank > current.versionRank || (versionRank == current.versionRank && versionGreater(candidate.Version, current.Version)) {
			current.Version = candidate.Version
			current.Verified = candidate.Verified
			current.versionRank = versionRank
		}
		if current.CustomDraftPath == "" {
			current.CustomDraftPath = candidate.CustomDraftPath
		}
		byRoot[key] = current
	}

	out := make([]Candidate, 0, len(byRoot))
	for _, item := range byRoot {
		out = append(out, item.Candidate)
	}
	sort.Slice(out, func(i, j int) bool {
		left, right := sourcePriority(out[i].Source), sourcePriority(out[j].Source)
		if left != right {
			return left < right
		}
		return strings.ToLower(out[i].DraftRoot) < strings.ToLower(out[j].DraftRoot)
	})
	return out
}

func inspectCandidate(location candidateLocation) (Candidate, int, bool) {
	draftRoot := strings.TrimSpace(location.draftRoot)
	info, err := os.Stat(draftRoot)
	if err != nil || !info.IsDir() {
		return Candidate{}, 0, false
	}
	draftRoot = cleanRealPath(draftRoot)
	installDir := cleanRealPath(strings.TrimSpace(location.installDir))
	count, hasMeta := readRootMetaCount(draftRoot)
	version, rank := candidateVersion(installDir, location.registryVersion, draftRoot)
	userData, _ := findAncestorNamed(draftRoot, "User Data", 5)
	customPath := ""
	if userData != "" {
		customPath = readCustomDraftPath(filepath.Join(userData, "Config", "globalSetting"))
	}
	return Candidate{
		DraftRoot:       draftRoot,
		InstallDir:      installDir,
		Version:         version,
		Verified:        CompatibleWithVerified(version, VerifiedJianyingVersions),
		CustomDraftPath: customPath,
		DraftCount:      count,
		HasRootMeta:     hasMeta,
		Source:          location.source,
	}, rank, true
}

func candidateVersion(installDir, registryVersion, draftRoot string) (string, int) {
	if version := maxVersionWithExe(installDir); version != "" {
		return version, 3
	}
	if version := strings.TrimSpace(registryVersion); parseVersion(version) != nil {
		return version, 2
	}
	installed, err := DetectInstalledVersion(draftRoot)
	if err == nil && installed.Version != "" {
		return installed.Version, 1
	}
	return "", 0
}

func readRootMetaCount(root string) (int, bool) {
	path := filepath.Join(root, "root_meta_info.json")
	info, err := os.Stat(path)
	if err != nil || !info.Mode().IsRegular() {
		return -1, false
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return -1, !os.IsNotExist(err)
	}
	return parseRootMetaCount(raw), true
}

func parseRootMetaCount(raw []byte) int {
	var doc struct {
		Stores json.RawMessage `json:"all_draft_store"`
	}
	if json.Unmarshal(raw, &doc) != nil || len(doc.Stores) == 0 {
		return -1
	}
	storeRaw := bytes.TrimSpace(doc.Stores)
	if len(storeRaw) == 0 || storeRaw[0] != '[' {
		return -1
	}
	var stores []json.RawMessage
	if json.Unmarshal(storeRaw, &stores) != nil {
		return -1
	}
	return len(stores)
}

func readCustomDraftPath(path string) string {
	raw, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	return parseCustomDraftPath(raw)
}

func parseCustomDraftPath(raw []byte) string {
	section := ""
	scanner := bufio.NewScanner(bytes.NewReader(raw))
	for scanner.Scan() {
		line := strings.TrimSpace(strings.TrimPrefix(scanner.Text(), "\ufeff"))
		if strings.HasPrefix(line, "[") && strings.HasSuffix(line, "]") {
			section = strings.TrimSpace(line[1 : len(line)-1])
			continue
		}
		if !strings.EqualFold(section, "General") {
			continue
		}
		key, value, ok := strings.Cut(line, "=")
		if !ok || !strings.EqualFold(strings.TrimSpace(key), "currentCustomDraftPath") {
			continue
		}
		value = strings.TrimSpace(value)
		if len(value) >= 2 && value[0] == '"' && value[len(value)-1] == '"' {
			value = value[1 : len(value)-1]
		}
		return strings.ReplaceAll(value, `\\`, `\`)
	}
	return ""
}

func draftRootUnderInstall(installDir string) string {
	return draftRootUnderUserData(filepath.Join(strings.TrimSpace(installDir), "AppData", "User Data"))
}

func draftRootUnderUserData(userData string) string {
	return filepath.Join(strings.TrimSpace(userData), "Projects", "com.lveditor.draft")
}

func installDirForUserData(userData string) string {
	parent := filepath.Dir(filepath.Clean(userData))
	if strings.EqualFold(filepath.Base(parent), "AppData") {
		return filepath.Dir(parent)
	}
	return parent
}

func cleanRealPath(path string) string {
	if strings.TrimSpace(path) == "" {
		return ""
	}
	path = filepath.Clean(path)
	if real, err := localpath.Resolve(path); err == nil {
		return filepath.Clean(real)
	}
	return path
}

func samePath(a, b string) bool {
	return pathKey(a) == pathKey(b)
}

func pathKey(path string) string {
	return strings.ToLower(filepath.Clean(path))
}

func sourcePriority(source string) int {
	switch source {
	case "registry":
		return 0
	case "localappdata":
		return 1
	case "install_dir":
		return 2
	case "common_path":
		return 3
	default:
		return 4
	}
}
