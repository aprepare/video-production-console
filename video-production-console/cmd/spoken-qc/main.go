// Command spoken-qc verifies that subtitle line preparation preserved the
// approved script after the same numeral normalization used by narration.
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"strings"
	"unicode"

	"video-production-console/internal/spokenlines"
)

func canonical(text string) string {
	text = strings.ReplaceAll(text, "\ufeff", "")
	text = strings.Map(func(r rune) rune {
		if unicode.IsSpace(r) {
			return -1
		}
		return r
	}, text)
	text = spokenlines.SpeechFromScript(text)
	runes := []rune(text)
	var b strings.Builder
	for i, r := range runes {
		if unicode.IsLetter(r) || unicode.IsDigit(r) || r == '%' || r == '％' ||
			(r == '.' && i > 0 && i+1 < len(runes) && unicode.IsDigit(runes[i-1]) && unicode.IsDigit(runes[i+1])) {
			b.WriteRune(r)
		}
	}
	return b.String()
}

func main() {
	script := flag.String("script", "", "approved continuous-script path")
	spoken := flag.String("spoken", "", "spoken line path")
	flag.Parse()
	source, err := os.ReadFile(*script)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	lines, err := os.ReadFile(*spoken)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	expected, actual := canonical(string(source)), canonical(string(lines))
	report := map[string]any{"preserved": expected == actual, "expected_chars": len([]rune(expected)), "actual_chars": len([]rune(actual)), "expected": expected, "actual": actual}
	if err := json.NewEncoder(os.Stdout).Encode(report); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	if expected != actual {
		os.Exit(2)
	}
}
