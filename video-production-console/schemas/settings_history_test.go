package schemas

import (
	"encoding/json"
	"os"
	"testing"
)

func TestSettingsSchemaOmitsRemovedHistoryLimit(t *testing.T) {
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
	public := definitions["publicSettings"].(map[string]any)
	properties := public["properties"].(map[string]any)
	if _, ok := properties["codex_history_limit"]; ok {
		t.Fatal("public settings schema still exposes codex_history_limit")
	}
	for _, value := range public["required"].([]any) {
		if value == "codex_history_limit" {
			t.Fatal("public settings schema still requires codex_history_limit")
		}
	}
}
