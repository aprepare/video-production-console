//go:build windows

package jianyingdraft

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

func TestDetectUsesReplaceableWindowsProbes(t *testing.T) {
	install := t.TempDir()
	root := makeDraftRoot(t, filepath.Join(install, "AppData", "User Data"), `{"all_draft_store":[]}`)

	oldRegistry := readRegistryInstallations
	oldRunning := jianyingProcessRunning
	oldLocal := localAppDataPath
	oldCommon := commonInstallDirs
	t.Cleanup(func() {
		readRegistryInstallations = oldRegistry
		jianyingProcessRunning = oldRunning
		localAppDataPath = oldLocal
		commonInstallDirs = oldCommon
	})
	readRegistryInstallations = func() ([]installation, error) {
		return []installation{{Dir: install, DisplayVersion: "11.1.0.7"}}, nil
	}
	jianyingProcessRunning = func() (bool, error) { return true, nil }
	localAppDataPath = func() string { return "" }
	commonInstallDirs = nil

	got, err := Detect()
	if err != nil {
		t.Fatal(err)
	}
	if !got.Running || len(got.Candidates) != 1 || got.Candidates[0].DraftRoot != cleanRealPath(root) {
		t.Fatalf("detection=%+v", got)
	}
}

func TestDetectTreatsProcessEnumerationFailureAsNotRunning(t *testing.T) {
	oldRegistry := readRegistryInstallations
	oldRunning := jianyingProcessRunning
	oldLocal := localAppDataPath
	oldCommon := commonInstallDirs
	t.Cleanup(func() {
		readRegistryInstallations = oldRegistry
		jianyingProcessRunning = oldRunning
		localAppDataPath = oldLocal
		commonInstallDirs = oldCommon
	})
	readRegistryInstallations = func() ([]installation, error) { return nil, errors.New("registry unavailable") }
	jianyingProcessRunning = func() (bool, error) { return false, errors.New("snapshot unavailable") }
	localAppDataPath = func() string { return "" }
	commonInstallDirs = nil

	got, err := Detect()
	if err != nil {
		t.Fatal(err)
	}
	if got.Running || got.Candidates == nil || len(got.Candidates) != 0 {
		t.Fatalf("detection=%+v", got)
	}
}

func TestInstallDirFromRegistryValues(t *testing.T) {
	tests := []struct {
		name        string
		location    string
		uninstall   string
		displayIcon string
		want        string
	}{
		{name: "location wins", location: ` D:\JianyingPro `, uninstall: `"E:\Other\uninst.exe" /S`, want: `D:\JianyingPro`},
		{name: "location version folder", location: `D:\JianyingPro\11.1.0.14287`, want: `D:\JianyingPro`},
		{name: "quoted uninstall", uninstall: `"D:\Apps\Jianying Pro\uninst.exe" /S`, want: `D:\Apps\Jianying Pro`},
		{name: "display icon suffix", displayIcon: `"E:\JianyingPro\JianyingPro.exe",0`, want: `E:\JianyingPro`},
		{name: "display icon in version folder", displayIcon: `"D:\JianyingPro\11.1.0.14287\JianyingPro.exe",0`, want: `D:\JianyingPro`},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := installDirFromRegistryValues(tc.location, tc.uninstall, tc.displayIcon); got != tc.want {
				t.Fatalf("install dir=%q want=%q", got, tc.want)
			}
		})
	}
}

func TestAppendInstallationKeepsHighestRegistryVersion(t *testing.T) {
	index := map[string]int{}
	var got []installation
	got = appendInstallation(got, index, installation{Dir: `D:\JianyingPro`})
	got = appendInstallation(got, index, installation{Dir: `d:\jianyingpro`, DisplayVersion: "11.1.0.14287"})
	got = appendInstallation(got, index, installation{Dir: `D:\JianyingPro`, DisplayVersion: "10.9.0.1"})

	if len(got) != 1 || got[0].DisplayVersion != "11.1.0.14287" {
		t.Fatalf("installations=%+v", got)
	}
}

func TestBuildCandidatesDeduplicatesDirectoryLinkSources(t *testing.T) {
	for _, linkType := range []string{"symlink", "junction"} {
		t.Run(linkType, func(t *testing.T) {
			testBuildCandidatesDeduplicatesDirectoryLinkSources(t, linkType)
		})
	}
}

func testBuildCandidatesDeduplicatesDirectoryLinkSources(t *testing.T, linkType string) {
	install := t.TempDir()
	realUserData := filepath.Join(install, "AppData", "User Data")
	realRoot := makeDraftRoot(t, realUserData, `{"all_draft_store":[]}`)
	linkParent := filepath.Join(t.TempDir(), "JianyingPro")
	if err := os.MkdirAll(linkParent, 0o755); err != nil {
		t.Fatal(err)
	}
	linkedUserData := filepath.Join(linkParent, "User Data")
	if linkType == "junction" {
		output, junctionErr := exec.Command("cmd", "/c", "mklink", "/J", linkedUserData, realUserData).CombinedOutput()
		if junctionErr != nil {
			t.Fatalf("cannot create directory junction: %v output=%s", junctionErr, output)
		}
	} else if err := os.Symlink(realUserData, linkedUserData); err != nil {
		t.Skipf("cannot create symbolic link: %v", err)
	}

	got := buildCandidates(detectionInputs{
		Registry:      []installation{{Dir: install, DisplayVersion: "11.1.0.1"}},
		LocalUserData: linkedUserData,
	})
	if len(got) != 1 || got[0].DraftRoot != cleanRealPath(realRoot) || got[0].Source != "registry" {
		t.Fatalf("candidates=%+v", got)
	}
}

func TestExpandPercentEnvironmentDoesNotLoopOnSelfReference(t *testing.T) {
	t.Setenv("JY_SELF_REF", `%JY_SELF_REF%`)
	done := make(chan string, 1)
	go func() {
		done <- expandPercentEnvironment(`%JY_SELF_REF%\bin`)
	}()
	select {
	case got := <-done:
		if got != `%JY_SELF_REF%\bin` {
			t.Fatalf("expanded=%q", got)
		}
	case <-time.After(250 * time.Millisecond):
		t.Fatal("environment expansion did not terminate")
	}
}
