package main

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"video-production-console/internal/jianyingdraft"
	"video-production-console/internal/settings"
	"video-production-console/internal/setup"
	"video-production-console/internal/store"
)

type provisionProtector struct{}

func (provisionProtector) Protect(b []byte) ([]byte, error) {
	return append([]byte("test:"), b...), nil
}
func (provisionProtector) Unprotect(b []byte) ([]byte, error) {
	return bytes.Clone(bytes.TrimPrefix(b, []byte("test:"))), nil
}

func provisionSettings(t *testing.T) (*settings.Service, string) {
	t.Helper()
	root := t.TempDir()
	db, err := store.Open(filepath.Join(root, "console.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	binary := filepath.Join(root, "codex.exe")
	if err := os.WriteFile(binary, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	cfg := settings.NewService(store.NewSettingsRepository(db), provisionProtector{})
	if err := cfg.InitializeBootSettings(t.Context(), settings.BootSettings{ListenAddr: "127.0.0.1:2030", DataRoot: root, CodexBinaryPath: binary, CodexWorkspaceRoots: []string{root}}); err != nil {
		t.Fatal(err)
	}
	return cfg, root
}

func TestInitializeSetupAppliesProvisioningBeforeRuntimeSnapshot(t *testing.T) {
	seller, _ := provisionSettings(t)
	view, _ := seller.Get(t.Context())
	view.Public.RemixModel = "provided-model"
	if _, err := seller.Update(t.Context(), view.Public, map[string]string{settings.SecretRemixAPIKey: "private-provision-marker"}); err != nil {
		t.Fatal(err)
	}
	code, err := seller.ExportCode(t.Context(), settings.ExportCodeRequest{IncludeSecrets: true, Keys: []string{"remix_model", "remix_api_key"}})
	if err != nil {
		t.Fatal(err)
	}
	cfg, root := provisionSettings(t)
	// 已有安装会保留数据库中的 DataRoot，不采用本次启动推导的默认目录。
	bootRoot := t.TempDir()
	if err := cfg.InitializeBootSettings(t.Context(), settings.BootSettings{DataRoot: bootRoot}); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(root, "provision"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "provision", "activation.jyc"), []byte(code.Code), 0o600); err != nil {
		t.Fatal(err)
	}
	draftRoot := t.TempDir()
	detector := setup.NewService(cfg, setup.Options{Detect: func() (jianyingdraft.Detection, error) {
		current, err := cfg.Get(t.Context())
		if err != nil || current.Public.RemixModel != "provided-model" {
			return jianyingdraft.Detection{}, errors.New("activation must precede detection")
		}
		return jianyingdraft.Detection{Candidates: []jianyingdraft.Candidate{{DraftRoot: draftRoot, HasRootMeta: true}}}, nil
	}})
	var logs bytes.Buffer
	initializeSetup(t.Context(), cfg, detector, slog.New(slog.NewTextHandler(&logs, nil)))
	runtime, err := cfg.Runtime(t.Context())
	if err != nil || runtime.RemixModel != "provided-model" || runtime.RemixAPIKey != "private-provision-marker" || runtime.JianyingRoot != draftRoot {
		t.Fatalf("首次运行快照未采用预置配置：读取失败=%v，模型正确=%v，密钥正确=%v，目录正确=%v，日志=%s", err != nil, runtime.RemixModel == "provided-model", runtime.RemixAPIKey == "private-provision-marker", runtime.JianyingRoot == draftRoot, logs.String())
	}
	current, _ := cfg.Get(t.Context())
	if current.RestartRequired {
		configured, active := reflect.ValueOf(current.Public), reflect.ValueOf(runtime.PublicSettings)
		var differing []string
		for i := 0; i < configured.NumField(); i++ {
			if !reflect.DeepEqual(configured.Field(i).Interface(), active.Field(i).Interface()) {
				differing = append(differing, configured.Type().Field(i).Name)
			}
		}
		t.Fatalf("首启配置不应要求再次重启，差异字段=%v", differing)
	}
	if !strings.Contains(logs.String(), "level=INFO") {
		t.Fatal("未记录首启应用成功")
	}
	for _, forbidden := range []string{code.Code, "private-provision-marker"} {
		if strings.Contains(logs.String(), forbidden) {
			t.Fatal("首启日志暴露配置码或密钥")
		}
	}
}

type failedActivation struct{}

func (failedActivation) Get(context.Context) (settings.View, error) {
	return settings.View{}, nil
}

func (failedActivation) AutoActivate(context.Context, string) (bool, error) {
	return false, errors.New("private-startup-marker?signature=private-value")
}

type countedDetection struct{ called bool }

func (d *countedDetection) AutoDetectJianying(context.Context) (bool, error) {
	d.called = true
	return false, errors.New("private-detection-marker")
}

func TestInitializeSetupFailureDoesNotBlockOtherWorkOrLogRawErrors(t *testing.T) {
	var logs bytes.Buffer
	detector := &countedDetection{}
	initializeSetup(t.Context(), failedActivation{}, detector, slog.New(slog.NewTextHandler(&logs, nil)))
	if !detector.called {
		t.Fatal("激活失败阻止了剪映检测")
	}
	if strings.Contains(logs.String(), "private-") || !strings.Contains(logs.String(), "level=WARN") {
		t.Fatal("启动日志未安全报告失败")
	}
}
