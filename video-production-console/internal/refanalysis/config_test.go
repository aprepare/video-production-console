package refanalysis

import (
	"errors"
	"testing"

	"video-production-console/internal/domain"
	"video-production-console/internal/settings"
)

func TestResolveConfigOrderAndPairedCredentials(t *testing.T) {
	rt := settings.Runtime{PublicSettings: domain.PublicSettings{
		AnalysisBaseURL: "https://analysis.test/v1/", AnalysisModel: "analysis-model",
		VisionBaseURL: "https://vision.test/v1", VisionModel: "vision-model",
		GrokBaseURL: "https://grok.test/v1", GrokModel: "grok-model",
		RemixBaseURL: "https://remix.test/v1", RemixModel: "remix-model",
	}, AnalysisAPIKey: "a", VisionAPIKey: "v", GrokAPIKey: "g", RemixAPIKey: "r"}
	check := func(override, model, base, key string) {
		t.Helper()
		got, err := ResolveConfig(rt, override)
		if err != nil || got.Model != model || got.BaseURL != base || got.APIKey != key {
			t.Fatalf("配置优先级或地址密钥配对错误: %v", err)
		}
	}
	check(" requested ", "requested", "https://analysis.test/v1", "a")
	check("", "analysis-model", "https://analysis.test/v1", "a")
	rt.AnalysisModel, rt.AnalysisAPIKey = "", ""
	check("", "vision-model", "https://analysis.test/v1", "")
	rt.AnalysisBaseURL, rt.AnalysisAPIKey = " ", "orphan"
	check("", "vision-model", "https://vision.test/v1", "v")
	rt.VisionBaseURL, rt.VisionModel = "", ""
	check("", "grok-model", "https://grok.test/v1", "g")
	rt.GrokBaseURL = ""
	check("", "grok-model", "https://remix.test/v1", "r")
	rt.GrokModel = ""
	if _, err := ResolveConfig(rt, ""); !errors.Is(err, ErrNotConfigured) {
		t.Fatal("不应使用 remix_model 作为拆解模型回退")
	}
	rt.RemixBaseURL = ""
	if _, err := ResolveConfig(rt, "requested"); !errors.Is(err, ErrNotConfigured) || err.Error() != "请先在设置里配置拆解模型" {
		t.Fatalf("空地址的配置错误不正确: %v", err)
	}
}
