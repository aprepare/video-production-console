package httpapi

import (
	"context"
	"testing"

	"video-production-console/internal/domain"
	"video-production-console/internal/taskmodel"
)

func TestModelKindForActionKeepsMontageOnCodex(t *testing.T) {
	if got := modelKindForAction(domain.ActionMontageExecute); got != taskmodel.KindCodex {
		t.Fatalf("montage kind=%q", got)
	}
	if got := modelKindForAction(domain.ActionRemixStandard); got != taskmodel.KindRemix {
		t.Fatalf("remix kind=%q", got)
	}
}

func TestDefaultTaskModelResolverUsesProductDefaults(t *testing.T) {
	got, err := resolveTaskModel(context.Background(), nil, taskmodel.Selection{})
	if err != nil {
		t.Fatal(err)
	}
	if got.Model != "gpt-5.6-sol" || got.ReasoningEffort != "medium" {
		t.Fatalf("selection=%+v", got)
	}
}
