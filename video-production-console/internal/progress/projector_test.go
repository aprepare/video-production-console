package progress

import (
	"testing"

	phasetiming "video-production-console/internal/timing"
)

func TestProjectShowsGrokResearchInChinese(t *testing.T) {
	event := Project(Input{TaskID: "task", Method: "item.started", RawJSON: `{"command":"grok_search.py"}`})
	if !event.Visible || event.DisplayText != "正在使用 Grok 联网核对最新信息" {
		t.Fatalf("event = %+v", event)
	}
}

func TestProjectDoesNotTreatGrokModelNameAsWebSearchWithoutRemix(t *testing.T) {
	event := Project(Input{
		TaskID:  "task",
		Method:  "item.started",
		RawJSON: `{"model":"cursor-grok-4.6-xhigh-fast","grok_model":"cursor-grok-4.6-xhigh-fast"}`,
	})
	if event.DisplayText == "正在使用 Grok 联网核对最新信息" {
		t.Fatalf("event = %+v", event)
	}
}

func TestProjectDoesNotTreatGrokModelNameAsWebSearch(t *testing.T) {
	event := Project(Input{
		TaskID:  "task",
		Action:  "remix.standard",
		Method:  "item.started",
		RawJSON: `{"model":"cursor-grok-4.6-xhigh-fast","grok_model":"cursor-grok-4.6-xhigh-fast"}`,
	})
	if event.DisplayText != "正在写二创文案" {
		t.Fatalf("event = %+v", event)
	}
}

func TestTimingProjectionUsesTheSharedObservableClassifier(t *testing.T) {
	got, ok := ProjectTiming(Input{Method: "item.started", RawJSON: `{"item":{"id":"search-1","command":"python grok_search.py --query private"}}`})
	if !ok || got.PhaseKey != "web_research" || got.Boundary != phasetiming.BoundaryStart || got.ExternalItemID != "search-1" {
		t.Fatalf("projection=%+v ok=%t", got, ok)
	}
	if _, ok := ProjectTiming(Input{Method: "item.started", RawJSON: `{"item":{"id":"outer","command":"python run_montage_job.py"}}`}); ok {
		t.Fatal("outer montage command must not be timed as draft_build")
	}
}
