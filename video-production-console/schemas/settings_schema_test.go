package schemas

import (
	"encoding/json"
	"os"
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
	secretKeys := []string{"grok_api_key", "pexels_api_key", "volc_speech_api_key", "image_api_key", "image_text_api_key"}
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
	imageKeys := []string{"image_base_url", "image_model", "image_text_base_url", "image_text_model", "max_image_concurrency", "default_image_ratio", "default_image_style"}
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
	baseURL := properties["image_base_url"].(map[string]any)
	if baseURL["maxLength"] != float64(2048) {
		t.Fatalf("image_base_url schema=%v", baseURL)
	}
	model := properties["image_model"].(map[string]any)
	if model["default"] != "gpt-image-2" || model["pattern"] != "^(?:\\S|\\S.*\\S)$" {
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

func stringSet(value any) map[string]bool {
	result := map[string]bool{}
	for _, item := range value.([]any) {
		result[item.(string)] = true
	}
	return result
}
