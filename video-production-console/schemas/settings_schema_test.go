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
	secretKeys := []string{"grok_api_key", "pexels_api_key", "volc_speech_api_key"}
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
