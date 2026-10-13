package setup

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"runtime"
	"testing"

	"video-production-console/internal/jianyingdraft"
	"video-production-console/internal/settings"
	"video-production-console/internal/store"
)

type setupProtector struct{}

func (setupProtector) Protect(value []byte) ([]byte, error) {
	return append([]byte("test:"), value...), nil
}
func (setupProtector) Unprotect(value []byte) ([]byte, error) {
	return bytes.Clone(bytes.TrimPrefix(value, []byte("test:"))), nil
}

func realSettings(t *testing.T) *settings.Service {
	t.Helper()
	db, err := store.Open(filepath.Join(t.TempDir(), "console.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	service := settings.NewService(store.NewSettingsRepository(db), setupProtector{})
	binary := writeTestFile(t, filepath.Join(t.TempDir(), "codex.exe"), nil)
	if err := service.InitializeBootSettings(t.Context(), settings.BootSettings{ListenAddr: "127.0.0.1:2030", DataRoot: t.TempDir(), CodexBinaryPath: binary}); err != nil {
		t.Fatal(err)
	}
	return service
}

func TestApplyJianyingRedetectsAndPreservesMachineProfile(t *testing.T) {
	cfg := realSettings(t)
	view, err := cfg.Get(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	profilePath := filepath.Join(view.Public.DataRoot, "profiles", "default.json")
	original := []byte(`{"python_binary":"python.exe","jianying_root":"old","cache_paths":{"sound":"cache.wav"},"future":{"large":9007199254740993,"array":[true,null,"值"]}}`)
	writeTestFile(t, profilePath, original)
	view.Public.MachineProfilePath = profilePath
	view.Public.RemixModel = "saved-writer"
	if _, err := cfg.Update(t.Context(), view.Public, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := cfg.Runtime(t.Context()); err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	writeTestFile(t, filepath.Join(root, "root_meta_info.json"), []byte(`{"all_draft_store":[]}`))
	candidates := []jianyingdraft.Candidate{{DraftRoot: root, HasRootMeta: true}}
	calls := 0
	service := NewService(cfg, Options{Detect: func() (jianyingdraft.Detection, error) {
		calls++
		return jianyingdraft.Detection{Candidates: candidates, Running: true}, nil
	}})
	detection, err := service.Detect(t.Context())
	if err != nil || detection.ConfiguredMatches || detection.ConfiguredRoot != "" || !detection.Running {
		t.Fatal("初始检测结果错误")
	}
	if _, err := service.ApplyJianying(t.Context(), t.TempDir()); !errors.Is(err, ErrInvalidDraftRoot) {
		t.Fatal("允许写入任意路径")
	}
	result, err := service.ApplyJianying(t.Context(), root)
	if err != nil {
		t.Fatal(err)
	}
	if result.JianyingRoot != root || !result.RestartRequired || !result.MachineProfileUpdated {
		t.Fatalf("应用结果=%+v", result)
	}
	if calls != 3 {
		t.Fatalf("应在每次应用时重新检测，实际=%d", calls)
	}
	updated, err := os.ReadFile(profilePath)
	if err != nil {
		t.Fatal(err)
	}
	var before, after map[string]json.RawMessage
	if json.Unmarshal(original, &before) != nil || json.Unmarshal(updated, &after) != nil {
		t.Fatal("档案JSON无效")
	}
	wantRoot, _ := json.Marshal(root)
	if !bytes.Equal(after["jianying_root"], wantRoot) {
		t.Fatal("机器档案路径未同步")
	}
	delete(before, "jianying_root")
	delete(after, "jianying_root")
	var compactBefore, compactAfter bytes.Buffer
	beforeJSON, _ := json.Marshal(before)
	afterJSON, _ := json.Marshal(after)
	_ = json.Compact(&compactBefore, beforeJSON)
	_ = json.Compact(&compactAfter, afterJSON)
	if !bytes.Equal(compactBefore.Bytes(), compactAfter.Bytes()) {
		t.Fatal("机器档案其他字段或大整数丢失")
	}
	view, _ = cfg.Get(t.Context())
	if view.Public.RemixModel != "saved-writer" || view.Public.JianyingRoot != root {
		t.Fatal("设置合并错误")
	}
	detection, err = service.Detect(t.Context())
	if err != nil || !detection.ConfiguredMatches || detection.ConfiguredRoot != root {
		t.Fatal("应用后检测未匹配保存设置")
	}
	candidates = nil
	if _, err := service.ApplyJianying(t.Context(), root); !errors.Is(err, ErrInvalidDraftRoot) {
		t.Fatal("仍接受已消失的旧候选")
	}
}

func TestApplyJianyingSkipsProfilesOutsideDataRootAndSymlinkEscapes(t *testing.T) {
	for _, alias := range []string{"outside", "symlink", "junction"} {
		t.Run(alias, func(t *testing.T) {
			dataRoot := t.TempDir()
			outside := writeTestFile(t, filepath.Join(t.TempDir(), "profile.json"), []byte(`{"jianying_root":"keep"}`))
			profilePath := outside
			if alias != "outside" {
				linkedDir := filepath.Join(dataRoot, "profiles")
				outsideDir := filepath.Dir(outside)
				if alias == "junction" {
					if runtime.GOOS != "windows" {
						t.Skip("仅 Windows 支持目录联接")
					}
					if output, linkErr := exec.Command("cmd", "/c", "mklink", "/J", linkedDir, outsideDir).CombinedOutput(); linkErr != nil {
						t.Fatalf("创建测试目录联接失败：%v，输出=%s", linkErr, output)
					}
				} else if err := os.Symlink(outsideDir, linkedDir); err != nil {
					t.Skipf("当前系统不允许测试创建符号链接：%v", err)
				}
				profilePath = filepath.Join(linkedDir, "profile.json")
			}
			cfg := &testSettings{}
			cfg.public.DataRoot, cfg.public.MachineProfilePath = dataRoot, profilePath
			root := t.TempDir()
			service := NewService(cfg, Options{Detect: func() (jianyingdraft.Detection, error) {
				return jianyingdraft.Detection{Candidates: []jianyingdraft.Candidate{{DraftRoot: root}}}, nil
			}})
			result, err := service.ApplyJianying(t.Context(), root)
			if err != nil || result.MachineProfileUpdated {
				t.Fatal("不应修改数据目录外的机器档案")
			}
			raw, err := os.ReadFile(outside)
			if err != nil || string(raw) != `{"jianying_root":"keep"}` {
				t.Fatal("越界档案被修改")
			}
		})
	}
}

func TestApplyJianyingInvalidProfileDoesNotSavePartialSettings(t *testing.T) {
	cfg := realSettings(t)
	view, _ := cfg.Get(t.Context())
	view.Public.MachineProfilePath = writeTestFile(t, filepath.Join(view.Public.DataRoot, "bad.json"), []byte("not-json"))
	if _, err := cfg.Update(t.Context(), view.Public, nil); err != nil {
		t.Fatal(err)
	}
	before, _ := cfg.Get(t.Context())
	root := t.TempDir()
	service := NewService(cfg, Options{Detect: func() (jianyingdraft.Detection, error) {
		return jianyingdraft.Detection{Candidates: []jianyingdraft.Candidate{{DraftRoot: root}}}, nil
	}})
	if _, err := service.ApplyJianying(t.Context(), root); err == nil {
		t.Fatal("应拒绝无法读改写的机器档案")
	}
	after, _ := cfg.Get(t.Context())
	if !reflect.DeepEqual(before, after) {
		t.Fatal("机器档案准备失败时不应保存部分设置")
	}
}

func TestAutoDetectJianyingRequiresEmptyConfigurationAndOneRegisteredRoot(t *testing.T) {
	for _, tc := range []struct {
		name       string
		configured bool
		candidates []bool
		want       bool
	}{
		{"none", false, nil, false}, {"without_meta", false, []bool{false}, false},
		{"one", false, []bool{true}, true}, {"one_registered", false, []bool{false, true}, true},
		{"ambiguous", false, []bool{true, true}, false}, {"already_configured", true, []bool{true}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cfg := realSettings(t)
			before, _ := cfg.Get(t.Context())
			if tc.configured {
				before.Public.JianyingRoot = t.TempDir()
				if _, err := cfg.Update(t.Context(), before.Public, nil); err != nil {
					t.Fatal(err)
				}
			}
			candidates := []jianyingdraft.Candidate{}
			wantRoot := ""
			for _, hasMeta := range tc.candidates {
				root := t.TempDir()
				candidates = append(candidates, jianyingdraft.Candidate{DraftRoot: root, HasRootMeta: hasMeta})
				if hasMeta {
					wantRoot = root
				}
			}
			calls := 0
			service := NewService(cfg, Options{Detect: func() (jianyingdraft.Detection, error) {
				calls++
				return jianyingdraft.Detection{Candidates: candidates}, nil
			}})
			applied, err := service.AutoDetectJianying(t.Context())
			if err != nil || applied != tc.want {
				t.Fatalf("自动应用=%v err=%v", applied, err)
			}
			if tc.configured && calls != 0 {
				t.Fatal("已有配置时不应检测或覆盖")
			}
			after, _ := cfg.Get(t.Context())
			if tc.want && (after.Public.JianyingRoot != wantRoot || after.RestartRequired) {
				t.Fatal("首启自动配置错误或提前冻结了运行快照")
			}
			if !tc.want && after.Public.JianyingRoot != before.Public.JianyingRoot {
				t.Fatal("非唯一候选时更改了配置")
			}
		})
	}
}
