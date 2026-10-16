package runninghub

import "testing"

func TestBuildH3NodesUpdatesOnlyRequestedFieldsAndDeepCopies(t *testing.T) {
	nodes, err := BuildH3Nodes(H3Request{Prompt: "镜头缓慢推进", DurationS: 7, Ratio: "9:16", PromptOptimize: false}, "uploaded.png")
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]string{"141/image": "uploaded.png", "150/value": "镜头缓慢推进", "186/value": "7", "115/aspect_ratio": "9:16 (Portrait Widescreen)", "185/value": "false", "171/value": "false", "147/value": "0.5", "192/value": "8"}
	for _, node := range nodes {
		key := node.NodeID + "/" + node.FieldName
		if expected, ok := want[key]; ok {
			if node.FieldValue != expected {
				t.Errorf("%s=%q want %q", key, node.FieldValue, expected)
			}
			delete(want, key)
		}
	}
	if len(want) != 0 {
		t.Fatalf("missing nodes: %v", want)
	}
	nodes[0].FieldValue = "mutated"
	again, err := BuildH3Nodes(H3Request{Prompt: "x", DurationS: 1, Ratio: "1:1", PromptOptimize: true}, "second.png")
	if err != nil || again[0].FieldValue != "second.png" {
		t.Fatalf("template was mutated: %+v %v", again[0], err)
	}
}

func TestBuildH3NodesValidatesInputsAndRatios(t *testing.T) {
	for _, ratio := range []string{"1:1", "3:4", "4:3", "9:16", "16:9"} {
		if _, err := BuildH3Nodes(H3Request{Prompt: "x", DurationS: 10, Ratio: ratio}, "a.png"); err != nil {
			t.Errorf("ratio %s: %v", ratio, err)
		}
	}
	for _, request := range []H3Request{{Prompt: "", DurationS: 1, Ratio: "9:16"}, {Prompt: "x", DurationS: 0, Ratio: "9:16"}, {Prompt: "x", DurationS: 11, Ratio: "9:16"}, {Prompt: "x", DurationS: 1, Ratio: "2:1"}} {
		if _, err := BuildH3Nodes(request, "a.png"); err == nil {
			t.Fatalf("accepted %+v", request)
		}
	}
}
