package spokenlines

import (
	"strings"
	"testing"
)

func TestBuildKeywordDocValidatesAndTrims(t *testing.T) {
	lines := []string{"全国法拍房挂牌", "已经堆到40万套", "很多人还在观望"}
	raw := "```json\n" + `{"lines":[
		{"line":"全国法拍房挂牌","keywords":[{"text":"法拍房","kind":"warning"},{"text":"崩盘","kind":"warning"}]},
		{"line":"已经堆到40万套","keywords":[{"text":"40万套","kind":"number"},{"text":"堆到","kind":"warning"},{"text":"已经","kind":"warning"}]},
		{"line":"很多人还在观望","keywords":[]}
	]}` + "\n```"
	doc, err := BuildKeywordDoc(raw, lines)
	if err != nil {
		t.Fatal(err)
	}
	if len(doc.Lines) != 3 {
		t.Fatalf("lines = %d", len(doc.Lines))
	}
	// 崩盘 is not in the line and must be dropped.
	if len(doc.Lines[0].Keywords) != 1 || doc.Lines[0].Keywords[0].Text != "法拍房" {
		t.Fatalf("line 1 keywords = %#v", doc.Lines[0].Keywords)
	}
	// The third keyword exceeds MaxKeywordsPerLine and must be dropped.
	if len(doc.Lines[1].Keywords) != 2 {
		t.Fatalf("line 2 keywords = %#v", doc.Lines[1].Keywords)
	}
	if len(doc.Lines[2].Keywords) != 0 {
		t.Fatalf("line 3 keywords = %#v", doc.Lines[2].Keywords)
	}
}

func TestBuildKeywordDocMatchesRewordedLinesByPosition(t *testing.T) {
	lines := []string{"全国法拍房挂牌", "已经堆到40万套"}
	raw := `{"lines":[
		{"line":"全国法拍房挂牌。","keywords":[{"text":"法拍房","kind":"warning"}]},
		{"line":"已经堆到40万套。","keywords":[{"text":"40万套","kind":"number"}]}
	]}`
	doc, err := BuildKeywordDoc(raw, lines)
	if err != nil {
		t.Fatal(err)
	}
	if len(doc.Lines[0].Keywords) != 1 || len(doc.Lines[1].Keywords) != 1 {
		t.Fatalf("positional matching failed: %#v", doc.Lines)
	}
	// The stored line text stays the console's, not the model's.
	if doc.Lines[0].Line != "全国法拍房挂牌" {
		t.Fatalf("line text = %q", doc.Lines[0].Line)
	}
}

func TestBuildKeywordDocNormalizesSloppyKinds(t *testing.T) {
	lines := []string{"库存高达67%", "银行开始收紧"}
	raw := `[{"line":"库存高达67%","keywords":[{"text":"67%","kind":"数字"}]},
		{"line":"银行开始收紧","keywords":[{"text":"收紧","kind":"emphasis"}]}]`
	doc, err := BuildKeywordDoc(raw, lines)
	if err != nil {
		t.Fatal(err)
	}
	if doc.Lines[0].Keywords[0].Kind != KeywordKindNumber {
		t.Fatalf("67%% kind = %q", doc.Lines[0].Keywords[0].Kind)
	}
	if doc.Lines[1].Keywords[0].Kind != KeywordKindWarning {
		t.Fatalf("收紧 kind = %q", doc.Lines[1].Keywords[0].Kind)
	}
}

func TestBuildKeywordDocRejectsGarbage(t *testing.T) {
	if _, err := BuildKeywordDoc("我无法完成这个任务", []string{"一行"}); err == nil {
		t.Fatal("non-JSON reply must error")
	}
	if _, err := BuildKeywordDoc(`{"lines":[]}`, nil); err == nil {
		t.Fatal("empty spoken lines must error")
	}
}

func TestParseKeywordDocRoundTrip(t *testing.T) {
	doc := KeywordDoc{
		SchemaVersion: KeywordSchemaVersion,
		Lines: []KeywordLine{
			{Line: "全国法拍房挂牌", Keywords: []Keyword{{Text: "法拍房", Kind: KeywordKindWarning}}},
		},
	}
	payload, err := MarshalKeywordDoc(doc)
	if err != nil {
		t.Fatal(err)
	}
	parsed, err := ParseKeywordDoc(payload)
	if err != nil {
		t.Fatal(err)
	}
	if len(parsed.Lines) != 1 || parsed.Lines[0].Keywords[0].Text != "法拍房" {
		t.Fatalf("roundtrip = %#v", parsed)
	}
}

func TestParseKeywordDocRejectsBadDocs(t *testing.T) {
	cases := []string{
		`{"schema_version":2,"lines":[]}`,
		`{"schema_version":1,"lines":[{"line":"","keywords":[]}]}`,
		`{"schema_version":1,"lines":[{"line":"一行","keywords":[{"text":"不存在","kind":"warning"}]}]}`,
		`{"schema_version":1,"lines":[{"line":"一行","keywords":[{"text":"一行","kind":"loud"}]}]}`,
	}
	for _, raw := range cases {
		if _, err := ParseKeywordDoc([]byte(raw)); err == nil {
			t.Fatalf("bad doc accepted: %s", raw)
		}
	}
}

func TestKeywordUserPromptListsLines(t *testing.T) {
	prompt := KeywordUserPrompt([]string{"第一行", "第二行"})
	if !strings.Contains(prompt, "第一行\n第二行\n") {
		t.Fatalf("prompt = %q", prompt)
	}
}
