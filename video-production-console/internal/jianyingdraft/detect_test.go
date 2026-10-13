package jianyingdraft

import (
	"os"
	"path/filepath"
	"testing"
)

func TestReadCustomDraftPathUnescapesINIValue(t *testing.T) {
	path := filepath.Join(t.TempDir(), "globalSetting")
	raw := "\ufeff[Other]\ncurrentCustomDraftPath=ignored\n[General]\r\ncurrentCustomDraftPath = D:\\\\JianyingPro Drafts ; keep this in the path\r\n"
	if err := os.WriteFile(path, []byte(raw), 0o644); err != nil {
		t.Fatal(err)
	}

	got := readCustomDraftPath(path)
	if got != `D:\JianyingPro Drafts ; keep this in the path` {
		t.Fatalf("custom draft path = %q", got)
	}
}

func TestReadCustomDraftPathAcceptsQuotedValue(t *testing.T) {
	path := filepath.Join(t.TempDir(), "globalSetting")
	if err := os.WriteFile(path, []byte("[general]\ncurrentcustomdraftpath=\"E:\\\\Drafts\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	if got := readCustomDraftPath(path); got != `E:\Drafts` {
		t.Fatalf("custom draft path = %q", got)
	}
}

func TestParseCustomDraftPathUsesOnlyGeneralSection(t *testing.T) {
	raw := []byte("[Other]\ncurrentCustomDraftPath=C:\\\\Wrong\n[General]\ncurrentCustomDraftPath=D:\\\\Drafts\n")
	if got := parseCustomDraftPath(raw); got != `D:\Drafts` {
		t.Fatalf("custom draft path = %q", got)
	}
}

func TestReadRootMetaCountsDraftStores(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "root_meta_info.json"), []byte(`{"all_draft_store":[{}, {}, {}]}`), 0o644); err != nil {
		t.Fatal(err)
	}

	count, exists := readRootMetaCount(root)
	if !exists || count != 3 {
		t.Fatalf("count=%d exists=%v", count, exists)
	}
}

func TestParseRootMetaCountRejectsNonArray(t *testing.T) {
	for _, raw := range [][]byte{
		[]byte(`{"all_draft_store":null}`),
		[]byte(`{"all_draft_store":{}}`),
		[]byte(`{"other":[]}`),
		[]byte(`not-json`),
	} {
		if got := parseRootMetaCount(raw); got != -1 {
			t.Fatalf("parseRootMetaCount(%q)=%d", raw, got)
		}
	}
}

func TestReadRootMetaDistinguishesMissingAndUnreadable(t *testing.T) {
	missingCount, missing := readRootMetaCount(t.TempDir())
	if missing || missingCount != -1 {
		t.Fatalf("missing: count=%d exists=%v", missingCount, missing)
	}

	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "root_meta_info.json"), []byte(`{"all_draft_store":`), 0o644); err != nil {
		t.Fatal(err)
	}
	invalidCount, exists := readRootMetaCount(root)
	if !exists || invalidCount != -1 {
		t.Fatalf("invalid: count=%d exists=%v", invalidCount, exists)
	}

	if err := os.WriteFile(filepath.Join(root, "root_meta_info.json"), []byte(`{"all_draft_store":null}`), 0o644); err != nil {
		t.Fatal(err)
	}
	nullCount, exists := readRootMetaCount(root)
	if !exists || nullCount != -1 {
		t.Fatalf("null store: count=%d exists=%v", nullCount, exists)
	}
}

func TestReadRootMetaRejectsDirectoryAtMetadataPath(t *testing.T) {
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, "root_meta_info.json"), 0o755); err != nil {
		t.Fatal(err)
	}

	count, exists := readRootMetaCount(root)
	if exists || count != -1 {
		t.Fatalf("directory: count=%d exists=%v", count, exists)
	}
}

func TestBuildCandidatesDeduplicatesAndUsesSourcePriority(t *testing.T) {
	install := t.TempDir()
	userData := filepath.Join(install, "AppData", "User Data")
	draft := makeDraftRoot(t, userData, `{"all_draft_store":[{}]}`)

	got := buildCandidates(detectionInputs{
		Registry:      []installation{{Dir: install, DisplayVersion: "11.1.0.1"}},
		LocalUserData: userData,
		InstallDirs:   []sourcePath{{Path: install, Source: "install_dir"}, {Path: install, Source: "common_path"}},
	})

	if len(got) != 1 {
		t.Fatalf("candidates=%+v", got)
	}
	wantRoot, err := filepath.EvalSymlinks(draft)
	if err != nil {
		t.Fatal(err)
	}
	if got[0].DraftRoot != wantRoot || got[0].Source != "registry" {
		t.Fatalf("candidate=%+v", got[0])
	}
	if !got[0].HasRootMeta || got[0].DraftCount != 1 {
		t.Fatalf("root meta fields=%+v", got[0])
	}
}

func TestBuildCandidatesSortsDeterministicallyAndSkipsMissingDraftDirs(t *testing.T) {
	commonInstall := t.TempDir()
	commonRoot := makeDraftRoot(t, filepath.Join(commonInstall, "AppData", "User Data"), "")
	localInstall := t.TempDir()
	localUserData := filepath.Join(localInstall, "User Data")
	localRoot := makeDraftRoot(t, localUserData, `{"all_draft_store":[]}`)

	got := buildCandidates(detectionInputs{
		LocalUserData: localUserData,
		InstallDirs: []sourcePath{
			{Path: filepath.Join(t.TempDir(), "does-not-exist"), Source: "registry"},
			{Path: commonInstall, Source: "common_path"},
		},
	})

	if len(got) != 2 {
		t.Fatalf("candidates=%+v", got)
	}
	if got[0].Source != "localappdata" || got[0].DraftRoot != cleanRealPath(localRoot) {
		t.Fatalf("first=%+v", got[0])
	}
	if got[1].Source != "common_path" || got[1].DraftRoot != cleanRealPath(commonRoot) {
		t.Fatalf("second=%+v", got[1])
	}
	if got[1].HasRootMeta || got[1].DraftCount != -1 {
		t.Fatalf("candidate without root meta=%+v", got[1])
	}
}

func TestBuildCandidatesUsesVersionPriority(t *testing.T) {
	t.Run("executable beats registry and root meta", func(t *testing.T) {
		install := t.TempDir()
		draft := makeDraftRoot(t, filepath.Join(install, "AppData", "User Data"), `{"draft_new_version":"99.0.0.1","all_draft_store":[]}`)
		writeExe(t, filepath.Join(install, "11.2.0.9"))
		writeExe(t, filepath.Join(install, "11.10.0.2"))
		got := buildCandidates(detectionInputs{Registry: []installation{{Dir: install, DisplayVersion: "12.0.0.1"}}})
		assertCandidateVersion(t, got, draft, "11.10.0.2")
	})

	t.Run("registry beats root meta", func(t *testing.T) {
		install := t.TempDir()
		draft := makeDraftRoot(t, filepath.Join(install, "AppData", "User Data"), `{"draft_new_version":"99.0.0.1","all_draft_store":[]}`)
		got := buildCandidates(detectionInputs{Registry: []installation{{Dir: install, DisplayVersion: "11.1.0.7"}}})
		assertCandidateVersion(t, got, draft, "11.1.0.7")
	})

	t.Run("root meta is the final fallback", func(t *testing.T) {
		install := t.TempDir()
		draft := makeDraftRoot(t, filepath.Join(install, "AppData", "User Data"), `{"draft_new_version":"9.8.7.6","all_draft_store":[]}`)
		got := buildCandidates(detectionInputs{InstallDirs: []sourcePath{{Path: install, Source: "common_path"}}})
		assertCandidateVersion(t, got, draft, "9.8.7.6")
	})
}

func TestMergeAndSortCandidatesUsesNoFilesystemState(t *testing.T) {
	got := mergeAndSortCandidates([]rankedCandidate{
		{Candidate: Candidate{DraftRoot: `D:\Drafts`, InstallDir: `D:\fallback`, Version: "10.0.0.1", Source: "common_path"}, versionRank: 1},
		{Candidate: Candidate{DraftRoot: `d:\drafts`, InstallDir: `D:\registered`, Version: "11.1.0.1", Verified: true, Source: "registry"}, versionRank: 2},
		{Candidate: Candidate{DraftRoot: `E:\Drafts`, Source: "localappdata"}},
	})
	if len(got) != 2 {
		t.Fatalf("candidates=%+v", got)
	}
	if got[0].Source != "registry" || got[0].InstallDir != `D:\registered` || got[0].Version != "11.1.0.1" {
		t.Fatalf("merged=%+v", got[0])
	}
	if got[1].Source != "localappdata" {
		t.Fatalf("sorted=%+v", got)
	}
}

func makeDraftRoot(t *testing.T, userData, meta string) string {
	t.Helper()
	root := filepath.Join(userData, "Projects", "com.lveditor.draft")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	if meta != "" {
		if err := os.WriteFile(filepath.Join(root, "root_meta_info.json"), []byte(meta), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return root
}

func assertCandidateVersion(t *testing.T, got []Candidate, root, want string) {
	t.Helper()
	if len(got) != 1 || got[0].DraftRoot != cleanRealPath(root) || got[0].Version != want {
		t.Fatalf("candidates=%+v, want root=%q version=%q", got, cleanRealPath(root), want)
	}
}
