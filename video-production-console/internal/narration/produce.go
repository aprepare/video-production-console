package narration

import (
	"context"
	"fmt"
	"strings"
)

// MaxScriptRunes bounds one synthesis call. The vendor bills per character and
// a runaway script is far more likely to be a wrong input than a real one, so
// the console refuses it rather than spending the quota.
const MaxScriptRunes = 10000

// Delivery is the pair of files the mixing stage consumes, together with the
// evidence used to accept them.
type Delivery struct {
	Audio       []byte
	AudioFormat string
	SRT         string
	Captions    []Caption
	Report      QCReport
	// Script is the text actually spoken, which is the caption source and may
	// differ from the raw asset by removed markdown decoration.
	Script string
	// BilledWords is the vendor's own character count for the call.
	BilledWords int
	// Duration is the end of the last caption. It is the only length available
	// without decoding the audio, and a compressed stream has no computable one.
	Duration float64
}

// ProduceRequest describes one narration job.
type ProduceRequest struct {
	Script    string
	SpeakerID string
	// Format defaults to mp3, the only format the console stores as a narration
	// asset.
	Format     string
	SampleRate int
	SpeechRate int
	Captions   Options
}

// QualityGateError reports captions that cannot be delivered. It carries the
// full report so the operator sees which cue failed and why, because the fix is
// almost always an edit to the script rather than a retry.
type QualityGateError struct {
	Report QCReport
}

func (e *QualityGateError) Error() string {
	return fmt.Sprintf("captions failed the delivery gate: %s", strings.Join(e.Report.Failures, "; "))
}

// Produce synthesizes narration and builds its subtitle track in one pass, so
// the two always describe the same audio. It returns the delivery even when the
// gate fails, so a caller that wants the audio for inspection still has it.
func Produce(ctx context.Context, client *Client, req ProduceRequest) (Delivery, error) {
	script := NormalizeScript(req.Script)
	if script == "" {
		return Delivery{}, fmt.Errorf("narration script is empty")
	}
	if n := len([]rune(script)); n > MaxScriptRunes {
		return Delivery{}, fmt.Errorf("narration script is %d characters, above the %d limit", n, MaxScriptRunes)
	}
	format := strings.TrimSpace(req.Format)
	if format == "" {
		format = "mp3"
	}
	result, err := client.Synthesize(ctx, Request{
		Text:       script,
		SpeakerID:  req.SpeakerID,
		Format:     format,
		SampleRate: req.SampleRate,
		SpeechRate: req.SpeechRate,
	})
	if err != nil {
		return Delivery{}, err
	}
	if len(result.Words) == 0 {
		return Delivery{}, fmt.Errorf("the voice returned no subtitle timings; check that the configured resource id selects a 2.0 voice family")
	}
	captions, report, err := Compose(script, result.Words, req.Captions)
	if err != nil {
		return Delivery{}, err
	}
	delivery := Delivery{
		Audio:       result.Audio,
		AudioFormat: format,
		SRT:         RenderSRT(captions),
		Captions:    captions,
		Report:      report,
		Script:      script,
		BilledWords: result.BilledWords,
	}
	if len(captions) > 0 {
		delivery.Duration = captions[len(captions)-1].End
	}
	if !report.Pass {
		return delivery, &QualityGateError{Report: report}
	}
	return delivery, nil
}

// NormalizeScript removes the markdown decoration a written script carries but
// a voice must not read aloud, and collapses the blank runs that would
// otherwise be spoken as pauses of unpredictable length. Everything else is
// left verbatim: the same text is sent for synthesis and used as caption
// source, so any edit here would show up as a coverage shortfall.
func NormalizeScript(script string) string {
	lines := strings.Split(strings.ReplaceAll(script, "\r\n", "\n"), "\n")
	kept := make([]string, 0, len(lines))
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if isHorizontalRule(line) {
			continue
		}
		line = strings.TrimSpace(strings.TrimLeft(line, "#>"))
		if bullet, ok := trimBullet(line); ok {
			line = bullet
		}
		if line == "" {
			continue
		}
		kept = append(kept, line)
	}
	return strings.Join(kept, "\n")
}

func isHorizontalRule(line string) bool {
	if len(line) < 3 {
		return false
	}
	return strings.Trim(line, "-") == "" || strings.Trim(line, "=") == "" || strings.Trim(line, "*") == ""
}

// trimBullet removes a list marker without touching a line that merely opens
// with an emphasis or a minus sign.
func trimBullet(line string) (string, bool) {
	for _, marker := range []string{"- ", "* ", "+ "} {
		if rest, ok := strings.CutPrefix(line, marker); ok {
			return strings.TrimSpace(rest), true
		}
	}
	return line, false
}
