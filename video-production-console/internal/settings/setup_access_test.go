package settings

import (
	"errors"
	"strings"
	"testing"
)

func TestReadSecretUsesSavedValueWithoutChangingRuntime(t *testing.T) {
	service, _, _, public := newSettingsTestService(t, Options{})
	if _, err := service.Update(t.Context(), public, map[string]string{SecretRemixAPIKey: "old-test-key"}); err != nil {
		t.Fatal(err)
	}
	if _, err := service.Runtime(t.Context()); err != nil {
		t.Fatal(err)
	}
	if err := service.PutSecret(t.Context(), SecretRemixAPIKey, "new-test-key"); err != nil {
		t.Fatal(err)
	}
	value, err := service.ReadSecret(t.Context(), SecretRemixAPIKey)
	if err != nil || value != "new-test-key" {
		t.Fatal("读取未使用最新保存的密钥")
	}
	runtime, err := service.Runtime(t.Context())
	if err != nil || runtime.RemixAPIKey != "old-test-key" {
		t.Fatal("读取密钥不应改变已加载的运行快照")
	}
	if _, err := service.ReadSecret(t.Context(), "unsupported"); !errors.Is(err, ErrUnknownSecret) {
		t.Fatal("未拒绝未知密钥项")
	}
	if value, err := service.ReadSecret(t.Context(), SecretGrokAPIKey); err != nil || value != "" {
		t.Fatal("未配置密钥应返回空值")
	}
}

func TestReadSecretDoesNotExposeUndecryptableMaterial(t *testing.T) {
	service, _, protector, _ := newSettingsTestService(t, Options{})
	if err := service.PutSecret(t.Context(), SecretRemixAPIKey, "private-test-marker"); err != nil {
		t.Fatal(err)
	}
	protector.corrupt = true
	value, err := service.ReadSecret(t.Context(), SecretRemixAPIKey)
	if err == nil || value != "" || strings.Contains(err.Error(), "private-test-marker") {
		t.Fatal("解密失败不应暴露保存内容")
	}
}
