package montage

import (
	"strings"
	"testing"
)

func TestEnvironmentWithoutPythonPathRemovesInheritedPythonOverrides(t *testing.T) {
	got := environmentWithoutPythonPath([]string{
		"PATH=C:\\Windows",
		"PYTHONPATH=F:\\Hermes\\venv\\Lib\\site-packages",
		"pythonhome=C:\\bad-python",
		"TEMP=C:\\Temp",
	})
	joined := strings.Join(got, "\n")
	if strings.Contains(strings.ToUpper(joined), "PYTHONPATH=") || strings.Contains(strings.ToUpper(joined), "PYTHONHOME=") {
		t.Fatalf("Python overrides leaked into subprocess environment: %q", joined)
	}
	for _, required := range []string{"PATH=C:\\Windows", "TEMP=C:\\Temp", "PYTHONIOENCODING=utf-8", "PYTHONUTF8=1"} {
		if !strings.Contains(joined, required) {
			t.Fatalf("missing %q in %q", required, joined)
		}
	}
}
