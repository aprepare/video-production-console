//go:build windows

package jianyingdraft

import (
	"errors"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"unsafe"

	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/registry"
)

var (
	readRegistryInstallations = queryRegistryInstallations
	jianyingProcessRunning    = queryJianyingProcessRunning
	localAppDataPath          = func() string { return os.Getenv("LOCALAPPDATA") }
	commonInstallDirs         = []string{
		`C:\Program Files\JianyingPro`,
		`C:\JianyingPro`,
		`D:\JianyingPro`,
		`E:\JianyingPro`,
	}
)

// Detect 汇总注册表、用户目录和常见安装目录中的剪映草稿位置。
func Detect() (Detection, error) {
	registryItems, _ := readRegistryInstallations()
	installDirs := make([]sourcePath, 0, len(commonInstallDirs))
	for _, path := range commonInstallDirs {
		if info, err := os.Stat(path); err == nil && info.IsDir() {
			installDirs = append(installDirs, sourcePath{Path: path, Source: "common_path"})
		}
	}
	localUserData := ""
	if local := strings.TrimSpace(localAppDataPath()); local != "" {
		localUserData = filepath.Join(local, "JianyingPro", "User Data")
	}
	running, err := jianyingProcessRunning()
	if err != nil {
		running = false
	}
	candidates := buildCandidates(detectionInputs{
		Registry:      registryItems,
		LocalUserData: localUserData,
		InstallDirs:   installDirs,
	})
	if candidates == nil {
		candidates = []Candidate{}
	}
	return Detection{Candidates: candidates, Running: running}, nil
}

func queryRegistryInstallations() ([]installation, error) {
	type registryRoot struct {
		key  registry.Key
		path string
	}
	roots := []registryRoot{
		{registry.CURRENT_USER, `Software\Microsoft\Windows\CurrentVersion\Uninstall`},
		{registry.CURRENT_USER, `Software\WOW6432Node\Microsoft\Windows\CurrentVersion\Uninstall`},
		{registry.LOCAL_MACHINE, `Software\Microsoft\Windows\CurrentVersion\Uninstall`},
		{registry.LOCAL_MACHINE, `Software\WOW6432Node\Microsoft\Windows\CurrentVersion\Uninstall`},
	}
	var out []installation
	var errs []error
	index := map[string]int{}
	for _, root := range roots {
		items, err := installationsUnderKey(root.key, root.path)
		if err != nil {
			if !errors.Is(err, registry.ErrNotExist) {
				errs = append(errs, err)
			}
			continue
		}
		for _, item := range items {
			out = appendInstallation(out, index, item)
		}
	}
	sort.Slice(out, func(i, j int) bool { return pathKey(out[i].Dir) < pathKey(out[j].Dir) })
	return out, errors.Join(errs...)
}

func appendInstallation(items []installation, index map[string]int, item installation) []installation {
	key := pathKey(item.Dir)
	position, exists := index[key]
	if !exists {
		index[key] = len(items)
		return append(items, item)
	}
	current := items[position]
	if current.DisplayVersion == "" || versionGreater(item.DisplayVersion, current.DisplayVersion) {
		current.DisplayVersion = item.DisplayVersion
		items[position] = current
	}
	return items
}

func installationsUnderKey(root registry.Key, path string) ([]installation, error) {
	key, err := registry.OpenKey(root, path, registry.READ)
	if err != nil {
		return nil, err
	}
	defer key.Close()
	names, err := key.ReadSubKeyNames(-1)
	if err != nil {
		return nil, err
	}
	var out []installation
	for _, name := range names {
		subkey, err := registry.OpenKey(key, name, registry.READ)
		if err != nil {
			continue
		}
		displayName, _, _ := subkey.GetStringValue("DisplayName")
		if !isJianyingRegistryEntry(name, displayName) {
			subkey.Close()
			continue
		}
		location, _, _ := subkey.GetStringValue("InstallLocation")
		uninstall, _, _ := subkey.GetStringValue("UninstallString")
		displayIcon, _, _ := subkey.GetStringValue("DisplayIcon")
		version, _, _ := subkey.GetStringValue("DisplayVersion")
		subkey.Close()
		dir := installDirFromRegistryValues(location, uninstall, displayIcon)
		if dir != "" {
			out = append(out, installation{Dir: dir, DisplayVersion: strings.TrimSpace(version)})
		}
	}
	return out, nil
}

func isJianyingRegistryEntry(keyName, displayName string) bool {
	name := strings.ToLower(keyName + " " + displayName)
	return strings.Contains(name, "jianyingpro") || strings.Contains(name, "剪映")
}

func installDirFromRegistryValues(location, uninstall, displayIcon string) string {
	if dir := cleanRegistryPath(location); dir != "" {
		return normalizeInstallDir(dir)
	}
	for _, command := range []string{uninstall, displayIcon} {
		if executable := executableFromCommand(command); executable != "" {
			return normalizeInstallDir(filepath.Dir(executable))
		}
	}
	return ""
}

func normalizeInstallDir(dir string) string {
	if jianyingVersionDir.MatchString(filepath.Base(dir)) {
		return filepath.Dir(dir)
	}
	return dir
}

func cleanRegistryPath(path string) string {
	path = strings.Trim(strings.TrimSpace(expandPercentEnvironment(path)), `"`)
	if path == "" {
		return ""
	}
	return filepath.Clean(path)
}

func executableFromCommand(command string) string {
	command = strings.TrimSpace(expandPercentEnvironment(command))
	if command == "" {
		return ""
	}
	if command[0] == '"' {
		if end := strings.Index(command[1:], `"`); end >= 0 {
			return filepath.Clean(command[1 : end+1])
		}
	}
	lower := strings.ToLower(command)
	if end := strings.Index(lower, ".exe"); end >= 0 {
		return filepath.Clean(strings.Trim(command[:end+4], ` "`))
	}
	if end := strings.IndexAny(command, " \t,"); end >= 0 {
		command = command[:end]
	}
	return filepath.Clean(strings.Trim(command, ` "`))
}

func expandPercentEnvironment(value string) string {
	var expanded strings.Builder
	for offset := 0; offset < len(value); {
		startOffset := strings.IndexByte(value[offset:], '%')
		if startOffset < 0 {
			expanded.WriteString(value[offset:])
			break
		}
		start := offset + startOffset
		expanded.WriteString(value[offset:start])
		endOffset := strings.IndexByte(value[start+1:], '%')
		if endOffset < 0 {
			expanded.WriteString(value[start:])
			break
		}
		end := start + 1 + endOffset
		if replacement, ok := os.LookupEnv(value[start+1 : end]); ok {
			expanded.WriteString(replacement)
		} else {
			expanded.WriteString(value[start : end+1])
		}
		offset = end + 1
	}
	return expanded.String()
}

func queryJianyingProcessRunning() (bool, error) {
	snapshot, err := windows.CreateToolhelp32Snapshot(windows.TH32CS_SNAPPROCESS, 0)
	if err != nil {
		return false, err
	}
	defer windows.CloseHandle(snapshot)
	entry := windows.ProcessEntry32{Size: uint32(unsafe.Sizeof(windows.ProcessEntry32{}))}
	if err := windows.Process32First(snapshot, &entry); err != nil {
		return false, err
	}
	for {
		if strings.EqualFold(windows.UTF16ToString(entry.ExeFile[:]), "JianyingPro.exe") {
			return true, nil
		}
		if err := windows.Process32Next(snapshot, &entry); err != nil {
			if errors.Is(err, windows.ERROR_NO_MORE_FILES) {
				return false, nil
			}
			return false, err
		}
	}
}
