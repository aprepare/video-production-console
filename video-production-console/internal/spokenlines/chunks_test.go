package spokenlines

import (
	"strings"
	"testing"
)

func TestSplitForParallelKeepsShortScriptWhole(t *testing.T) {
	short := strings.Repeat("今天这条内容关乎你的钱。", 20) // 240 字
	got := SplitForParallel(short)
	if len(got) != 1 || got[0] != short {
		t.Fatalf("short script must stay one chunk, got %d", len(got))
	}
}

func TestSplitForParallelCutsAtSentenceEndsAndPreservesText(t *testing.T) {
	sentence := "你存在银行里的钱一分不会少，还是你的，但有一件事正在静悄悄发生。"
	script := strings.Repeat(sentence, 80) // ≈2500 字
	chunks := SplitForParallel(script)
	if len(chunks) < 2 || len(chunks) > MaxChunks {
		t.Fatalf("chunks = %d", len(chunks))
	}
	for i, chunk := range chunks {
		if !strings.HasSuffix(chunk, "。") {
			t.Fatalf("chunk %d does not end at a sentence boundary: …%s", i, chunk[len(chunk)-12:])
		}
	}
	if strings.Join(chunks, "") != script {
		t.Fatal("joined chunks must reproduce the script exactly")
	}
}

func TestSplitForParallelKeepsClosingQuoteWithSentence(t *testing.T) {
	part := strings.Repeat("他说过一句话。", 60) + "他最后说：“上车要趁早。”" + strings.Repeat("后面继续讲。", 60)
	chunks := SplitForParallel(part)
	for _, chunk := range chunks {
		if strings.HasPrefix(chunk, "”") {
			t.Fatal("closing quote must not start a chunk")
		}
	}
	if strings.Join(chunks, "") != strings.TrimSpace(part) {
		t.Fatal("joined chunks must reproduce the script exactly")
	}
}
