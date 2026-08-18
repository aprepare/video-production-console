package schemas

import (
	"encoding/json"
	"os"
	"strings"
	"testing"
)

func TestSettingsSchemaParsesAndCapsSecretInputs(t *testing.T) {
	raw, err := os.ReadFile("settings.schema.json")
	if err != nil {
		t.Fatal(err)
	}
	var schema map[string]any
	if err := json.Unmarshal(raw, &schema); err != nil {
		t.Fatal(err)
	}
	definitions, ok := schema["$defs"].(map[string]any)
	if !ok {
		t.Fatal("schema has no $defs")
	}
	secretInput := definitions["secretInput"].(map[string]any)
	properties := secretInput["properties"].(map[string]any)
	secretKeys := []string{"grok_api_key", "remix_api_key", "pexels_api_key", "volc_speech_api_key", "aurastd_tts_api_key", "image_api_key", "image_text_api_key", "vision_api_key", "embedding_api_key", "pixabay_api_key"}
	for _, key := range secretKeys {
		property, ok := properties[key].(map[string]any)
		if !ok || property["maxLength"] != float64(16<<10) {
			t.Fatalf("%s schema=%v", key, property)
		}
	}
	if secretInput["additionalProperties"] != false {
		t.Fatal("secret input permits unknown keys")
	}
	// Every masked secret is always reported, so the view must require them all.
	secretView := definitions["secretView"].(map[string]any)
	required := map[string]bool{}
	for _, key := range secretView["required"].([]any) {
		required[key.(string)] = true
	}
	viewProperties := secretView["properties"].(map[string]any)
	for _, key := range secretKeys {
		if !required[key] || viewProperties[key] == nil {
			t.Fatalf("%s missing from the secret view", key)
		}
	}
}

func TestSettingsSchemaDescribesRemixModelSettings(t *testing.T) {
	raw, err := os.ReadFile("settings.schema.json")
	if err != nil {
		t.Fatal(err)
	}
	var schema map[string]any
	if err := json.Unmarshal(raw, &schema); err != nil {
		t.Fatal(err)
	}
	definitions := schema["$defs"].(map[string]any)
	public := definitions["publicSettings"].(map[string]any)
	properties := public["properties"].(map[string]any)
	remixKeys := []string{"remix_base_url", "remix_model", "remix_reasoning_effort"}
	for _, key := range remixKeys {
		if properties[key] == nil {
			t.Fatalf("%s missing from public settings schema", key)
		}
	}
	updateRequired := stringSet(definitions["publicSettingsUpdate"].(map[string]any)["allOf"].([]any)[1].(map[string]any)["required"])
	viewRequired := stringSet(definitions["publicSettingsView"].(map[string]any)["allOf"].([]any)[1].(map[string]any)["required"])
	for _, key := range remixKeys {
		if updateRequired[key] {
			t.Fatalf("legacy settings update unexpectedly requires %s", key)
		}
		if !viewRequired[key] {
			t.Fatalf("settings view does not require %s", key)
		}
	}
}

func TestSettingsSchemaDescribesImageGenerationSettings(t *testing.T) {
	raw, err := os.ReadFile("settings.schema.json")
	if err != nil {
		t.Fatal(err)
	}
	var schema map[string]any
	if err := json.Unmarshal(raw, &schema); err != nil {
		t.Fatal(err)
	}
	definitions := schema["$defs"].(map[string]any)
	public := definitions["publicSettings"].(map[string]any)
	properties := public["properties"].(map[string]any)
	imageKeys := []string{"image_base_url", "image_model", "image_text_base_url", "image_text_model", "image_text_reasoning_effort", "max_image_concurrency", "image_generation_attempts", "default_image_ratio", "default_image_style"}
	for _, key := range imageKeys {
		if properties[key] == nil {
			t.Fatalf("%s missing from public settings schema", key)
		}
	}

	// Legacy clients may PUT their old complete public object without the newly
	// added image fields, while GET responses must remain fully shaped.
	updatePublic := definitions["publicSettingsUpdate"].(map[string]any)
	updateRequired := stringSet(updatePublic["allOf"].([]any)[1].(map[string]any)["required"])
	viewPublic := definitions["publicSettingsView"].(map[string]any)
	viewRequired := stringSet(viewPublic["allOf"].([]any)[1].(map[string]any)["required"])
	for _, key := range imageKeys {
		if updateRequired[key] {
			t.Fatalf("legacy settings update unexpectedly requires %s", key)
		}
		if !viewRequired[key] {
			t.Fatalf("settings view does not require %s", key)
		}
	}
	if definitions["settingsUpdate"].(map[string]any)["properties"].(map[string]any)["public"].(map[string]any)["$ref"] != "#/$defs/publicSettingsUpdate" {
		t.Fatal("settingsUpdate does not use the compatibility update shape")
	}
	if definitions["settingsView"].(map[string]any)["properties"].(map[string]any)["public"].(map[string]any)["$ref"] != "#/$defs/publicSettingsView" {
		t.Fatal("settingsView does not use the required response shape")
	}

	concurrency := properties["max_image_concurrency"].(map[string]any)
	if concurrency["minimum"] != float64(1) || concurrency["maximum"] != float64(18) || concurrency["default"] != float64(3) {
		t.Fatalf("max_image_concurrency schema=%v", concurrency)
	}
	attempts := properties["image_generation_attempts"].(map[string]any)
	if attempts["minimum"] != float64(1) || attempts["maximum"] != float64(4) || attempts["default"] != float64(2) {
		t.Fatalf("image_generation_attempts schema=%v", attempts)
	}
	baseURL := properties["image_base_url"].(map[string]any)
	if baseURL["maxLength"] != float64(2048) {
		t.Fatalf("image_base_url schema=%v", baseURL)
	}
	model := properties["image_model"].(map[string]any)
	if model["default"] != "gpt-image-2" || model["maxLength"] != float64(128) {
		t.Fatalf("image_model schema=%v", model)
	}
	ratio := properties["default_image_ratio"].(map[string]any)
	if ratio["default"] != "3:4" {
		t.Fatalf("default_image_ratio schema=%v", ratio)
	}
	style := properties["default_image_style"].(map[string]any)
	if style["default"] != "finance_documentary" {
		t.Fatalf("default_image_style schema=%v", style)
	}
}

func TestSettingsSchemaDescribesMediaIntelligenceSettings(t *testing.T) {
	raw, err := os.ReadFile("settings.schema.json")
	if err != nil {
		t.Fatal(err)
	}
	var schema map[string]any
	if err := json.Unmarshal(raw, &schema); err != nil {
		t.Fatal(err)
	}
	definitions := schema["$defs"].(map[string]any)
	public := definitions["publicSettings"].(map[string]any)
	properties := public["properties"].(map[string]any)
	mediaKeys := []string{
		"media_catalog_path", "ffmpeg_path", "ffprobe_path",
		"vision_base_url", "vision_model", "embedding_base_url", "embedding_model",
		"pexels_api_base_url", "pixabay_api_base_url", "max_external_results_per_query",
	}
	for _, key := range mediaKeys {
		if properties[key] == nil {
			t.Fatalf("%s missing from public settings schema", key)
		}
	}
	// 旧客户端 PUT 的完整 public 对象可以没有新字段，GET 响应必须完整。
	updateRequired := stringSet(definitions["publicSettingsUpdate"].(map[string]any)["allOf"].([]any)[1].(map[string]any)["required"])
	viewRequired := stringSet(definitions["publicSettingsView"].(map[string]any)["allOf"].([]any)[1].(map[string]any)["required"])
	for _, key := range mediaKeys {
		if updateRequired[key] {
			t.Fatalf("legacy settings update unexpectedly requires %s", key)
		}
		if !viewRequired[key] {
			t.Fatalf("settings view does not require %s", key)
		}
	}
	limit := properties["max_external_results_per_query"].(map[string]any)
	if limit["minimum"] != float64(1) || limit["maximum"] != float64(50) || limit["default"] != float64(20) {
		t.Fatalf("max_external_results_per_query schema=%v", limit)
	}
	pexels := properties["pexels_api_base_url"].(map[string]any)
	if pexels["default"] != "https://api.pexels.com" || !strings.Contains(pexels["pattern"].(string), "api\\.pexels\\.com") {
		t.Fatalf("pexels_api_base_url schema=%v", pexels)
	}
	pixabay := properties["pixabay_api_base_url"].(map[string]any)
	if pixabay["default"] != "https://pixabay.com" || !strings.Contains(pixabay["pattern"].(string), "pixabay\\.com") {
		t.Fatalf("pixabay_api_base_url schema=%v", pixabay)
	}
}

func TestSettingsSchemaDescribesAuraSTDTTsSettings(t *testing.T) {
	raw, err := os.ReadFile("settings.schema.json")
	if err != nil {
		t.Fatal(err)
	}
	var schema map[string]any
	if err := json.Unmarshal(raw, &schema); err != nil {
		t.Fatal(err)
	}
	definitions := schema["$defs"].(map[string]any)
	properties := definitions["publicSettings"].(map[string]any)["properties"].(map[string]any)
	keys := []string{
		"tts_provider", "aurastd_base_url", "aurastd_model", "aurastd_voice_id",
		"aurastd_speed", "aurastd_volume", "aurastd_pitch", "aurastd_modify_intensity",
		"aurastd_modify_timbre", "aurastd_sound_effects",
	}
	for _, key := range keys {
		if properties[key] == nil {
			t.Fatalf("%s missing from public settings schema", key)
		}
	}
	updateRequired := stringSet(definitions["publicSettingsUpdate"].(map[string]any)["allOf"].([]any)[1].(map[string]any)["required"])
	viewRequired := stringSet(definitions["publicSettingsView"].(map[string]any)["allOf"].([]any)[1].(map[string]any)["required"])
	for _, key := range keys {
		if updateRequired[key] {
			t.Fatalf("legacy settings update unexpectedly requires %s", key)
		}
		if !viewRequired[key] {
			t.Fatalf("settings view does not require %s", key)
		}
	}
	speed := properties["aurastd_speed"].(map[string]any)
	if speed["minimum"] != 0.5 || speed["maximum"] != float64(2) || speed["default"] != 1.21 {
		t.Fatalf("aurastd_speed schema=%v", speed)
	}
}

func stringSet(value any) map[string]bool {
	result := map[string]bool{}
	for _, item := range value.([]any) {
		result[item.(string)] = true
	}
	return result
}
