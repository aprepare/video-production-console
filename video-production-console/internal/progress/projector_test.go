package progress

import "testing"

func TestProjectShowsGrokResearchInChinese(t *testing.T) {
	event := Project(Input{TaskID: "task", Method: "item.started", RawJSON: `{"command":"grok_search.py"}`})
	if !event.Visible || event.DisplayText != "正在使用 Grok 联网核对最新信息" {
		t.Fatalf("event = %+v", event)
	}
}
