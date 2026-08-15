package openaicompat

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"video-production-console/internal/codex"
	"video-production-console/internal/domain"
)

func TestWriteRemixDeliverablePassesValidator(t *testing.T) {
	outputDir := t.TempDir()
	script := "又一批人要发财了，人民币第三次换锚已经开始。前两波是美元外贸和土地房子，旧锚死了，利率下来，一百七十万亿存款在找出路。第三个锚先不说完，现在就上车。"
	if err := writeRemixDeliverable(outputDir, "11111111-1111-1111-1111-111111111111", string(domain.ActionRemixStandard), script); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(filepath.Join(outputDir, "result.json"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := codex.ValidateResultEnvelopeJSON(raw, "11111111-1111-1111-1111-111111111111", domain.ActionRemixStandard, outputDir); err != nil {
		t.Fatal(err)
	}
}

func TestPickHotTopicsKeepsOnlyAllowlist(t *testing.T) {
	got := pickHotTopics([]string{"#人民币", "#经济", "#干货分享", "#随便写"}, "seed")
	if len(got) < 3 || len(got) > 4 {
		t.Fatalf("count=%d %v", len(got), got)
	}
	if got[0] != "#经济" || got[1] != "#干货分享" {
		t.Fatalf("kept order = %v", got)
	}
	allow := map[string]bool{}
	for _, tag := range hotPublishingTopics {
		allow[tag] = true
	}
	for _, tag := range got {
		if !allow[tag] {
			t.Fatalf("unexpected %q", tag)
		}
	}
}

func TestPublishingPackageRewritesInventedHashtags(t *testing.T) {
	pkg := publishingPackageFromDraft(remixDraft{
		Descriptions: []string{"前两次换锚分别推高了外贸和房子。 #人民币 #财富趋势 #经济周期"},
		Topics:       []string{"#人民币", "#财富趋势", "#资金流向"},
	}, "又一批人要发财了。")
	topics, _ := pkg["topics"].([]string)
	if len(topics) < 3 || len(topics) > 4 {
		t.Fatalf("topics=%v", topics)
	}
	joined := strings.Join(topics, " ")
	for _, desc := range pkg["descriptions"].([]string) {
		if !strings.HasSuffix(desc, joined) {
			t.Fatalf("description=%q topics=%v", desc, topics)
		}
		if strings.Contains(desc, "#人民币") || strings.Contains(desc, "#财富趋势") {
			t.Fatalf("invented hashtag leaked: %q", desc)
		}
	}
}
