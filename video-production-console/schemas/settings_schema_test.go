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
	for _, key := range []string{"grok_api_key", "pexels_api_key"} {
		property, ok := properties[key].(map[string]any)
		if !ok || property["maxLength"] != float64(16<<10) {
			t.Fatalf("%s schema=%v", key, property)
		}
	}
	if secretInput["additionalProperties"] != false {
		t.Fatal("secret input permits unknown keys")
	}
}
