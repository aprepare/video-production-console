package narration

import (
	"fmt"
	"math"
	"strings"
)

// RenderSRT writes captions as SubRip, the format the mixing stage consumes.
func RenderSRT(captions []Caption) string {
	var b strings.Builder
	for i, c := range captions {
		fmt.Fprintf(&b, "%d\n%s --> %s\n%s\n\n",
			i+1, formatSRTTime(c.Start), formatSRTTime(c.End), c.Text)
	}
	return b.String()
}

func formatSRTTime(seconds float64) string {
	if seconds < 0 || math.IsNaN(seconds) {
		seconds = 0
	}
	total := int64(math.Round(seconds * 1000))
	ms := total % 1000
	total /= 1000
	s := total % 60
	total /= 60
	m := total % 60
	h := total / 60
	return fmt.Sprintf("%02d:%02d:%02d,%03d", h, m, s, ms)
}
