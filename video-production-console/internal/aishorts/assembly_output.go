package aishorts

import (
	"bytes"
	"strings"
)

// Receives complete phase messages from the draft subprocess; stderr remains available on failure.
type assemblyOutput struct {
	output  *bytes.Buffer
	pending string
	notify  func(string)
}

func (w *assemblyOutput) Write(p []byte) (int, error) {
	w.output.Write(p)
	w.pending += string(p)
	for {
		i := strings.IndexByte(w.pending, '\n')
		if i < 0 {
			break
		}
		line := strings.TrimSpace(w.pending[:i])
		w.pending = w.pending[i+1:]
		if strings.HasPrefix(line, "AI_SHORT_STAGE:") {
			w.notify(strings.TrimPrefix(line, "AI_SHORT_STAGE:"))
		}
	}
	return len(p), nil
}
