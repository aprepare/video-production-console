package portable

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

const stateFileName = "state.json"

type VersionState struct {
	Current  string `json:"current"`
	Previous string `json:"previous,omitempty"`
}

var atomicWrite = writeAtomic

func SwitchVersion(appRoot, version string) error {
	appRoot, err := resolveAppRoot(appRoot)
	if err != nil {
		return err
	}
	if err := assertSafeVersionDir(appRoot, version); err != nil {
		return err
	}
	state, err := ReadState(appRoot)
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	next := VersionState{Current: version, Previous: state.Previous}
	if state.Current != version {
		previous := state.Current
		if previous == "" {
			previous = highestOtherVersion(appRoot, version)
		}
		if previous == version {
			previous = ""
		}
		next.Previous = previous
	}
	body, err := json.Marshal(next)
	if err != nil {
		return fmt.Errorf("%w: %v", ErrStateWrite, err)
	}
	return atomicWrite(filepath.Join(appRoot, stateFileName), body)
}

func ReadState(appRoot string) (VersionState, error) {
	appRoot, err := resolveAppRoot(appRoot)
	if err != nil {
		return VersionState{}, err
	}
	body, err := os.ReadFile(filepath.Join(appRoot, stateFileName))
	if err != nil {
		return VersionState{}, err
	}
	var state VersionState
	if err := json.Unmarshal(body, &state); err != nil {
		return VersionState{}, err
	}
	if state.Current != "" && !ValidSemver(state.Current) {
		return VersionState{}, fmt.Errorf("%w: current %q", ErrInvalidVersion, state.Current)
	}
	if state.Previous != "" && !ValidSemver(state.Previous) {
		return VersionState{}, fmt.Errorf("%w: previous %q", ErrInvalidVersion, state.Previous)
	}
	return state, nil
}

func CleanupUnusedVersions(appRoot string, state VersionState) error {
	appRoot, err := resolveAppRoot(appRoot)
	if err != nil {
		return err
	}
	if !ValidSemver(state.Current) {
		return fmt.Errorf("%w: current %q", ErrInvalidVersion, state.Current)
	}
	if state.Previous != "" && !ValidSemver(state.Previous) {
		return fmt.Errorf("%w: previous %q", ErrInvalidVersion, state.Previous)
	}
	entries, err := os.ReadDir(appRoot)
	if err != nil {
		return err
	}
	for _, entry := range entries {
		name := entry.Name()
		if name == stagingDirName {
			if err := removeStaging(appRoot); err != nil {
				return err
			}
			continue
		}
		if !ValidSemver(name) || name == state.Current || name == state.Previous {
			continue
		}
		if err := removeVersionDir(appRoot, name); err != nil {
			return err
		}
	}
	return nil
}

func removeVersionDir(appRoot, version string) error {
	appRoot, err := resolveAppRoot(appRoot)
	if err != nil {
		return err
	}
	if !ValidSemver(version) {
		return fmt.Errorf("%w: %q", ErrOutsideAppRoot, version)
	}
	target := filepath.Join(appRoot, version)
	if filepath.Base(target) != version || !samePath(filepath.Dir(target), appRoot) {
		return fmt.Errorf("%w: %q", ErrOutsideAppRoot, version)
	}
	info, err := os.Lstat(target)
	if err != nil {
		return err
	}
	if isSymlinkOrReparse(info) {
		return fmt.Errorf("%w: %q", ErrSymlink, target)
	}
	if !info.IsDir() {
		return fmt.Errorf("%w: %q", ErrUnsafePath, target)
	}
	if !samePath(filepath.Dir(target), appRoot) {
		return fmt.Errorf("%w: %q", ErrOutsideAppRoot, target)
	}
	return os.RemoveAll(target)
}

func removeStaging(appRoot string) error {
	staging := filepath.Join(appRoot, stagingDirName)
	info, err := os.Lstat(staging)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	if isSymlinkOrReparse(info) {
		return fmt.Errorf("%w: %q", ErrSymlink, staging)
	}
	if !samePath(filepath.Dir(staging), appRoot) {
		return fmt.Errorf("%w: %q", ErrOutsideAppRoot, staging)
	}
	return os.RemoveAll(staging)
}

func highestOtherVersion(appRoot, current string) string {
	entries, err := os.ReadDir(appRoot)
	if err != nil {
		return ""
	}
	best := ""
	for _, entry := range entries {
		name := entry.Name()
		if name == current || !ValidSemver(name) || !entry.IsDir() {
			continue
		}
		if best == "" || compareSemver(name, best) > 0 {
			best = name
		}
	}
	return best
}

func compareSemver(a, b string) int {
	ap := strings.Split(a, ".")
	bp := strings.Split(b, ".")
	for i := 0; i < 3; i++ {
		ai, _ := strconv.Atoi(ap[i])
		bi, _ := strconv.Atoi(bp[i])
		if ai < bi {
			return -1
		}
		if ai > bi {
			return 1
		}
	}
	return 0
}

func writeAtomic(path string, data []byte) error {
	tmp := path + ".tmp"
	file, err := os.OpenFile(tmp, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
	if err != nil {
		return fmt.Errorf("%w: %v", ErrStateWrite, err)
	}
	if _, err := file.Write(data); err != nil {
		_ = file.Close()
		_ = os.Remove(tmp)
		return fmt.Errorf("%w: %v", ErrStateWrite, err)
	}
	if err := file.Sync(); err != nil {
		_ = file.Close()
		_ = os.Remove(tmp)
		return fmt.Errorf("%w: %v", ErrStateWrite, err)
	}
	if err := file.Close(); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("%w: %v", ErrStateWrite, err)
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("%w: %v", ErrStateWrite, err)
	}
	return nil
}

func resolveAppRoot(appRoot string) (string, error) {
	if strings.TrimSpace(appRoot) == "" {
		return "", fmt.Errorf("%w: empty app root", ErrUnsafePath)
	}
	abs, err := filepath.Abs(appRoot)
	if err != nil {
		return "", err
	}
	abs = filepath.Clean(abs)
	if err := os.MkdirAll(abs, 0o700); err != nil {
		return "", err
	}
	info, err := os.Lstat(abs)
	if err != nil {
		return "", err
	}
	if isSymlinkOrReparse(info) {
		return "", fmt.Errorf("%w: app root", ErrSymlink)
	}
	if !info.IsDir() {
		return "", fmt.Errorf("%w: app root is not a directory", ErrUnsafePath)
	}
	resolved, err := filepath.EvalSymlinks(abs)
	if err != nil {
		return "", err
	}
	resolved = filepath.Clean(resolved)
	if !samePath(resolved, abs) {
		resolvedInfo, statErr := os.Lstat(resolved)
		if statErr != nil || isSymlinkOrReparse(resolvedInfo) || !resolvedInfo.IsDir() {
			return "", fmt.Errorf("%w: app root", ErrUnsafePath)
		}
	}
	return resolved, nil
}

func assertSafeVersionDir(appRoot, version string) error {
	if !ValidSemver(version) {
		return fmt.Errorf("%w: %q", ErrInvalidVersion, version)
	}
	target := filepath.Join(appRoot, version)
	if filepath.Base(target) != version || !samePath(filepath.Dir(target), appRoot) {
		return fmt.Errorf("%w: %q", ErrInvalidVersion, version)
	}
	info, err := os.Lstat(target)
	if err != nil {
		return err
	}
	if isSymlinkOrReparse(info) {
		return fmt.Errorf("%w: %q", ErrSymlink, target)
	}
	if !info.IsDir() {
		return fmt.Errorf("%w: %q is not a directory", ErrInvalidVersion, version)
	}
	return nil
}

func assertDirectChildDir(parent, child string, allowMissing bool) error {
	if !samePath(filepath.Dir(child), parent) {
		return fmt.Errorf("%w: %q", ErrOutsideAppRoot, child)
	}
	info, err := os.Lstat(child)
	if os.IsNotExist(err) && allowMissing {
		return nil
	}
	if err != nil {
		return err
	}
	if isSymlinkOrReparse(info) {
		return fmt.Errorf("%w: %q", ErrSymlink, child)
	}
	if !info.IsDir() {
		return fmt.Errorf("%w: %q is not a directory", ErrUnsafePath, child)
	}
	return nil
}

func samePath(a, b string) bool {
	a = filepath.Clean(a)
	b = filepath.Clean(b)
	if filepath.Separator == '\\' {
		return strings.EqualFold(a, b)
	}
	return a == b
}

func isSymlinkOrReparse(info os.FileInfo) bool {
	if info == nil {
		return false
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return true
	}
	return isReparse(info)
}
