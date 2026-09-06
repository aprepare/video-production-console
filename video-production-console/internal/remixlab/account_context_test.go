package remixlab

import (
	"encoding/json"
	"testing"
	"video-production-console/internal/store"
)

func TestExperimentViewIncludesOwningAccountEvenBeforeProduction(t *testing.T) {
	view := mapExperiment(store.RemixLabExperimentRecord{ID: "project", ProduceAccountID: "cloud-account"}, nil, nil, nil)
	raw, err := json.Marshal(view)
	if err != nil {
		t.Fatal(err)
	}
	var payload map[string]any
	if err = json.Unmarshal(raw, &payload); err != nil {
		t.Fatal(err)
	}
	if payload["account_id"] != "cloud-account" {
		t.Fatalf("owning account missing: %s", raw)
	}
}
