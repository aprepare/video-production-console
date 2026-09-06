package openaicompat

import (
	"strings"
	"testing"
)

func TestCourseTailPurchaseMayBeFollowedByNaturalFollow(t *testing.T) {
	warnings := strings.Join(structureAdvisories("评论区留个一帆风顺。课程在主页橱窗，点开看看。也可以关注，后续继续聊这些变化。"), "\n")
	if strings.Contains(warnings, "橱窗") {
		t.Fatalf("natural follow after purchase was flagged: %s", warnings)
	}
}
