package settings

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestAnalysisSettingsRoundTripAndHotUpdate(t *testing.T) {
	s, db, _, public := newSettingsTestService(t, Options{})
	if _, err := s.PutPublic(t.Context(), public); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Runtime(t.Context()); err != nil {
		t.Fatal(err)
	}
	public.AnalysisBaseURL, public.AnalysisModel = "https://analysis.example/v1", "vision-custom"
	view, err := s.Update(t.Context(), public, map[string]string{SecretAnalysisAPIKey: "test-analysis-secret"})
	if err != nil {
		t.Fatal(err)
	}
	if view.RestartRequired || view.Public.AnalysisModel != "vision-custom" || view.ActivePublic.AnalysisBaseURL != public.AnalysisBaseURL {
		t.Fatal("拆解设置没有立即生效")
	}
	rt, err := s.Runtime(t.Context())
	if err != nil || rt.AnalysisAPIKey != "test-analysis-secret" || rt.AnalysisModel != "vision-custom" {
		t.Fatal("运行时没有更新拆解配置")
	}
	raw, err := json.Marshal(view)
	if err != nil || strings.Contains(string(raw), "test-analysis-secret") || !view.Secrets[SecretAnalysisAPIKey].Configured {
		t.Fatal("密钥状态或掩码错误")
	}
	var ciphertext string
	if err := db.QueryRow(`SELECT ciphertext FROM encrypted_secrets WHERE key='analysis_api_key'`).Scan(&ciphertext); err != nil || ciphertext == "test-analysis-secret" {
		t.Fatal("拆解密钥未加密存储")
	}
	if _, err := s.Update(t.Context(), public, map[string]string{SecretAnalysisAPIKey: "rotated-analysis-secret"}); err != nil {
		t.Fatal(err)
	}
	rt, err = s.Runtime(t.Context())
	if err != nil || rt.AnalysisAPIKey != "rotated-analysis-secret" {
		t.Fatal("拆解密钥轮换没有立即生效")
	}
	public.AnalysisBaseURL, public.AnalysisModel = "", ""
	view, err = s.Update(t.Context(), public, nil)
	if err != nil || view.RestartRequired || view.ActivePublic.AnalysisModel != "" || view.ActivePublic.AnalysisBaseURL != "" {
		t.Fatal("清空拆解设置没有立即生效")
	}
}

func TestAnalysisSettingsValidationAndOverlay(t *testing.T) {
	s, _, _, public := newSettingsTestService(t, Options{})
	merged, err := OverlayPublic(public, json.RawMessage(`{"analysis_base_url":"https://example.test/v1","analysis_model":"模型甲"}`))
	if err != nil || merged.AnalysisModel != "模型甲" || merged.AnalysisBaseURL != "https://example.test/v1" {
		t.Fatalf("overlay: %v", err)
	}
	for _, bad := range []string{"ftp://example.test", "javascript:alert(1)", "https://user:secret@example.test", "https://example.test?token=secret"} {
		public.AnalysisBaseURL = bad
		if _, err := s.Update(t.Context(), public, nil); err == nil || !strings.Contains(UserMessage(err), "拆解模型地址") {
			t.Fatalf("未拒绝非法拆解地址或缺少中文标签: %v", err)
		}
	}
	public.AnalysisBaseURL, public.AnalysisModel = "", strings.Repeat("m", 257)
	if _, err := s.Update(t.Context(), public, nil); err == nil || !strings.Contains(UserMessage(err), "拆解模型名") {
		t.Fatalf("未拒绝过长模型名: %v", err)
	}
}
