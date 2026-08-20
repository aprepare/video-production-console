package partnerprofile

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

const (
	CodeRootPath                = "root_path"
	CodeMissingOriginals        = "missing_originals"
	CodeUnwritableJianying      = "unwritable_jianying"
	CodeReparsePoint            = "reparse_point"
	CodeMissingPython           = "missing_python"
	CodeMissingFFmpeg           = "missing_ffmpeg"
	CodeMissingFFprobe          = "missing_ffprobe"
	CodeMissingPyJianYingDraft  = "missing_pyjianyingdraft"
	CodeMissingMontageResources = "missing_montage_resources"
	CodeInvalidPath             = "invalid_path"
)

// ProfileSchemaVersion is required by the montage runtime script; profiles
// with any other value are rejected before a draft is built.
const ProfileSchemaVersion = "1.0"

type MachineProfile struct {
	SchemaVersion        string            `json:"schema_version"`
	PythonBinary         string            `json:"python_binary"`
	JianyingRoot         string            `json:"jianying_root"`
	MediaRoot            string            `json:"media_root"`
	MediaIndexPath       string            `json:"media_index_path"`
	MontageResourcesPath string            `json:"montage_resources_path"`
	CachePaths           map[string]string `json:"cache_paths"`
}

// bundledCacheFiles maps the montage script's required cache keys to paths
// inside the bundled resources/montage directory. The transition entry is a
// directory (a Jianying effect cache folder); every other entry is an audio
// file.
var bundledCacheFiles = map[string]string{
	"bgm_yawaraka_hikari":       filepath.Join("bgm", "yawaraka_hikari.mp3"),
	"sfx_opening_hit":           filepath.Join("sfx", "opening_hit.mp3"),
	"sfx_water_drop":            filepath.Join("sfx", "water_drop.mp3"),
	"sfx_whoosh":                filepath.Join("sfx", "whoosh.mp3"),
	"sfx_conclusion_hit":        filepath.Join("sfx", "conclusion_hit.mp3"),
	"transition_cross_dissolve": filepath.Join("transitions", "cross_dissolve"),
}

// BundledCachePaths resolves the montage cache resources shipped under the
// given app root. It fails when any resource is missing so a broken payload
// is caught during setup instead of inside the montage script.
func BundledCachePaths(appRoot string) (map[string]string, error) {
	resources := filepath.Join(appRoot, "resources", "montage")
	paths := make(map[string]string, len(bundledCacheFiles))
	for key, rel := range bundledCacheFiles {
		full := filepath.Join(resources, rel)
		if _, err := os.Lstat(full); err != nil {
			return nil, reject(CodeMissingMontageResources, "bundled montage resource %s is missing", key)
		}
		paths[key] = full
	}
	return paths, nil
}

type BuildOptions struct {
	AppRoot      string
	DataRoot     string
	MediaRoot    string
	JianyingRoot string
}

type Error struct {
	Code string
	Msg  string
}

func (e *Error) Error() string {
	if e == nil {
		return ""
	}
	return e.Msg
}

func CodeOf(err error) string {
	var typed *Error
	if errors.As(err, &typed) {
		return typed.Code
	}
	return ""
}

func reject(code, format string, args ...any) error {
	return &Error{Code: code, Msg: fmt.Sprintf(format, args...)}
}

func Build(opts BuildOptions) (MachineProfile, error) {
	appRoot, err := canonicalizeExistingDir(opts.AppRoot)
	if err != nil {
		return MachineProfile{}, reject(CodeInvalidPath, "app root is invalid: %v", err)
	}
	dataRoot, err := absClean(opts.DataRoot)
	if err != nil {
		return MachineProfile{}, reject(CodeInvalidPath, "data root is invalid: %v", err)
	}
	mediaRoot, err := validateMediaRoot(opts.MediaRoot)
	if err != nil {
		return MachineProfile{}, err
	}
	jianyingRoot, err := validateJianyingRoot(opts.JianyingRoot)
	if err != nil {
		return MachineProfile{}, err
	}

	python := filepath.Join(appRoot, "runtime", "python", "python.exe")
	if err := requireRegularFile(python, CodeMissingPython, "bundled python is missing"); err != nil {
		return MachineProfile{}, err
	}
	if err := requirePyJianYingDraft(appRoot); err != nil {
		return MachineProfile{}, err
	}
	ffmpeg := filepath.Join(appRoot, "runtime", "ffmpeg", "bin", "ffmpeg.exe")
	if err := requireRegularFile(ffmpeg, CodeMissingFFmpeg, "bundled ffmpeg is missing"); err != nil {
		return MachineProfile{}, err
	}
	ffprobe := filepath.Join(appRoot, "runtime", "ffmpeg", "bin", "ffprobe.exe")
	if err := requireRegularFile(ffprobe, CodeMissingFFprobe, "bundled ffprobe is missing"); err != nil {
		return MachineProfile{}, err
	}
	resources := filepath.Join(appRoot, "resources", "montage")
	if err := requireExistingDir(resources, CodeMissingMontageResources, "bundled montage resources are missing"); err != nil {
		return MachineProfile{}, err
	}
	cachePaths, err := BundledCachePaths(appRoot)
	if err != nil {
		return MachineProfile{}, err
	}

	profile := MachineProfile{
		SchemaVersion:        ProfileSchemaVersion,
		PythonBinary:         python,
		JianyingRoot:         jianyingRoot,
		MediaRoot:            mediaRoot,
		MediaIndexPath:       filepath.Join(dataRoot, "media", "index.json"),
		MontageResourcesPath: resources,
		CachePaths:           cachePaths,
	}
	if err := writeProfile(dataRoot, profile); err != nil {
		return MachineProfile{}, err
	}
	return profile, nil
}

func DetectJianyingRoot() (string, error) {
	local := strings.TrimSpace(os.Getenv("LOCALAPPDATA"))
	if local == "" {
		return "", reject(CodeInvalidPath, "LOCALAPPDATA is required")
	}
	candidates := []string{
		filepath.Join(local, "JianyingPro", "User Data", "Projects", "com.lveditor.draft"),
		filepath.Join(local, "JianyingPro", "User Data", "Projects"),
	}
	for _, candidate := range candidates {
		if _, err := validateJianyingRoot(candidate); err == nil {
			return candidate, nil
		}
	}
	return "", reject(CodeInvalidPath, "no writable Jianying draft directory was detected")
}

func FFmpegPaths(appRoot string) (ffmpeg, ffprobe string) {
	return filepath.Join(appRoot, "runtime", "ffmpeg", "bin", "ffmpeg.exe"),
		filepath.Join(appRoot, "runtime", "ffmpeg", "bin", "ffprobe.exe")
}

func ProfilePath(dataRoot string) string {
	return filepath.Join(dataRoot, "config", "machine-profile.json")
}

// RefreshBundledRuntime rewrites python and montage-resource paths that still
// point at a deleted partner app version. Jianying and media roots stay as-is.
func RefreshBundledRuntime(dataRoot, appRoot string) (MachineProfile, bool, error) {
	path := ProfilePath(dataRoot)
	raw, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return MachineProfile{}, false, nil
		}
		return MachineProfile{}, false, err
	}
	var profile MachineProfile
	if err := json.Unmarshal(raw, &profile); err != nil {
		return MachineProfile{}, false, err
	}
	python := filepath.Join(appRoot, "runtime", "python", "python.exe")
	resources := filepath.Join(appRoot, "resources", "montage")
	changed := false
	if regularFileExists(python) && !pathUnder(profile.PythonBinary, filepath.Join(appRoot, "runtime", "python")) {
		profile.PythonBinary = python
		changed = true
	}
	if dirExists(resources) && !pathUnder(profile.MontageResourcesPath, filepath.Join(appRoot, "resources", "montage")) {
		profile.MontageResourcesPath = resources
		changed = true
	}
	if profile.SchemaVersion != ProfileSchemaVersion {
		profile.SchemaVersion = ProfileSchemaVersion
		changed = true
	}
	if cachePaths, err := BundledCachePaths(appRoot); err == nil {
		for key, bundled := range cachePaths {
			current := strings.TrimSpace(profile.CachePaths[key])
			if current != "" {
				if _, statErr := os.Lstat(current); statErr == nil {
					continue
				}
			}
			if profile.CachePaths == nil {
				profile.CachePaths = make(map[string]string, len(cachePaths))
			}
			profile.CachePaths[key] = bundled
			changed = true
		}
	}
	if !changed {
		return profile, false, nil
	}
	if err := writeProfile(dataRoot, profile); err != nil {
		return profile, false, err
	}
	return profile, true, nil
}

func regularFileExists(path string) bool {
	info, err := os.Lstat(strings.TrimSpace(path))
	return err == nil && info.Mode().IsRegular() && !isReparsePoint(path)
}

func dirExists(path string) bool {
	info, err := os.Lstat(strings.TrimSpace(path))
	return err == nil && info.IsDir() && !isReparsePoint(path)
}

func pathUnder(child, root string) bool {
	child = filepath.Clean(strings.TrimSpace(child))
	root = filepath.Clean(strings.TrimSpace(root))
	if child == "" || root == "" {
		return false
	}
	rel, err := filepath.Rel(root, child)
	if err != nil {
		return false
	}
	return rel != ".." && !strings.HasPrefix(rel, ".."+string(os.PathSeparator))
}

func writeProfile(dataRoot string, profile MachineProfile) error {
	path := ProfilePath(dataRoot)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	raw, err := json.MarshalIndent(profile, "", "  ")
	if err != nil {
		return err
	}
	return writeAtomic(path, append(raw, '\n'))
}

func writeAtomic(path string, body []byte) error {
	temp := path + ".tmp"
	if err := os.WriteFile(temp, body, 0o600); err != nil {
		return err
	}
	if err := os.Rename(temp, path); err != nil {
		_ = os.Remove(temp)
		return err
	}
	return nil
}

func validateMediaRoot(path string) (string, error) {
	abs, err := absClean(path)
	if err != nil {
		return "", reject(CodeInvalidPath, "media root is invalid: %v", err)
	}
	if isDriveOrWindowsRoot(abs) {
		return "", reject(CodeRootPath, "media root must not be a drive or Windows root")
	}
	if isReparsePoint(abs) {
		return "", reject(CodeReparsePoint, "media root must not be a reparse point")
	}
	if err := requireExistingDir(abs, CodeInvalidPath, "media root must be an existing directory"); err != nil {
		return "", err
	}
	originals := filepath.Join(abs, "originals")
	if info, err := os.Lstat(originals); err != nil || !info.IsDir() {
		return "", reject(CodeMissingOriginals, "media root is missing an originals directory")
	}
	if isReparsePoint(originals) {
		return "", reject(CodeReparsePoint, "media originals must not be a reparse point")
	}
	return abs, nil
}

func validateJianyingRoot(path string) (string, error) {
	abs, err := absClean(path)
	if err != nil {
		return "", reject(CodeInvalidPath, "jianying root is invalid: %v", err)
	}
	if isDriveOrWindowsRoot(abs) {
		return "", reject(CodeRootPath, "jianying root must not be a drive or Windows root")
	}
	if isReparsePoint(abs) {
		return "", reject(CodeReparsePoint, "jianying root must not be a reparse point")
	}
	if err := requireExistingDir(abs, CodeUnwritableJianying, "jianying root must be an existing writable directory"); err != nil {
		return "", err
	}
	if err := assertWritableDir(abs); err != nil {
		return "", reject(CodeUnwritableJianying, "jianying root is not writable")
	}
	return abs, nil
}

func requirePyJianYingDraft(appRoot string) error {
	path := filepath.Join(appRoot, "runtime", "python", "Lib", "site-packages", "pyJianYingDraft")
	info, err := os.Lstat(path)
	if err != nil || isReparsePoint(path) {
		return reject(CodeMissingPyJianYingDraft, "bundled pyJianYingDraft is missing")
	}
	if info.IsDir() || info.Mode().IsRegular() {
		return nil
	}
	return reject(CodeMissingPyJianYingDraft, "bundled pyJianYingDraft is missing")
}

func requireRegularFile(path, code, message string) error {
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() || isReparsePoint(path) {
		return reject(code, "%s", message)
	}
	return nil
}

func requireExistingDir(path, code, message string) error {
	info, err := os.Lstat(path)
	if err != nil || !info.IsDir() || isReparsePoint(path) {
		return reject(code, "%s", message)
	}
	return nil
}

func canonicalizeExistingDir(path string) (string, error) {
	abs, err := absClean(path)
	if err != nil {
		return "", err
	}
	if isDriveOrWindowsRoot(abs) {
		return "", errors.New("directory is a drive root")
	}
	if isReparsePoint(abs) {
		return "", errors.New("directory is a reparse point")
	}
	info, err := os.Lstat(abs)
	if err != nil || !info.IsDir() {
		return "", errors.New("directory does not exist")
	}
	return abs, nil
}

func absClean(path string) (string, error) {
	trimmed := strings.TrimSpace(path)
	if trimmed == "" {
		return "", errors.New("path is empty")
	}
	abs, err := filepath.Abs(trimmed)
	if err != nil {
		return "", err
	}
	return filepath.Clean(abs), nil
}

func isDriveOrWindowsRoot(path string) bool {
	clean := filepath.Clean(path)
	volume := filepath.VolumeName(clean)
	if volume != "" {
		root := volume + string(os.PathSeparator)
		if strings.EqualFold(clean, volume) || strings.EqualFold(clean, root) {
			return true
		}
	}
	return clean == string(os.PathSeparator) || clean == "/"
}

func assertWritableDir(path string) error {
	probe := filepath.Join(path, ".vpc-write-probe")
	if err := os.WriteFile(probe, []byte("ok"), 0o600); err != nil {
		return err
	}
	return os.Remove(probe)
}
