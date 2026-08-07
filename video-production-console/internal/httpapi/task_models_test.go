package httpapi

import (
	"context"
	"testing"

	"video-production-console/internal/taskmodel"
)

func TestDefaultTaskModelResolverUsesProductDefaults(t *testing.T) {
	got, err := resolveTaskModel(context.Background(), nil, taskmodel.Selection{})
	if err != nil {
		t.Fatal(err)
	}
	if got.Model != "gpt-5.6-sol" || got.ReasoningEffort != "medium" {
		t.Fatalf("selection=%+v", got)
	}
}
