// Command editor-acceptance runs an isolated montage with the configured
// service credentials in memory. It never prints keys or changes settings.
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"time"
	"video-production-console/internal/agentruntime/montageplan"
	"video-production-console/internal/security"
	"video-production-console/internal/settings"
	"video-production-console/internal/store"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
func run() error {
	manifest := flag.String("manifest", "", "isolated manifest")
	exe := flag.String("exe", "", "verified console executable")
	model := flag.String("model", "", "configured model override")
	probe := flag.Bool("probe", false, "one short model selection probe")
	flag.Parse()
	root, e := os.Getwd()
	if e != nil {
		return e
	}
	db, e := store.Open(filepath.Join(root, "video-console-data", "console.db"))
	if e != nil {
		return e
	}
	defer db.Close()
	svc := settings.NewService(store.NewSettingsRepository(db), security.NewSecretProtector())
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Minute)
	defer cancel()
	rt, e := svc.Runtime(ctx)
	if e != nil {
		return e
	}
	name := *model
	if name == "" {
		name = rt.RemixModel
	}
	if rt.RemixBaseURL == "" || rt.RemixAPIKey == "" {
		return fmt.Errorf("configured montage text service is unavailable")
	}
	if *probe {
		analyzer := montageplan.NewHTTPIntentAnalyzer(montageplan.IntentAnalyzerConfig{BaseURL: rt.RemixBaseURL, Model: name, APIKey: rt.RemixAPIKey})
		selector, ok := analyzer.(montageplan.ShotSelector)
		if !ok {
			return fmt.Errorf("selector not configured")
		}
		picks, e := selector.SelectShots(ctx, []montageplan.ShotSelectJob{{Intent: montageplan.NarrativeIntent{SegmentID: "probe-001", Text: "回村盖房之前，先把一家人的账算清楚。"}, Candidates: []montageplan.ShotSelectCandidate{{ShotID: "village", Summary: "A village and homes in China", Setting: "Rural_Life"}, {ShotID: "lightshow", Summary: "Decorative lightshow in a Chinese city", Setting: "City_Business"}}}})
		if e != nil {
			return e
		}
		return json.NewEncoder(os.Stdout).Encode(map[string]any{"requested_model": name, "validated_picks": picks})
	}
	if *manifest == "" || *exe == "" {
		return fmt.Errorf("--manifest and --exe required")
	}
	raw, e := os.ReadFile(*manifest)
	if e != nil {
		return e
	}
	var m struct {
		OutputDir string `json:"output_dir"`
	}
	if e = json.Unmarshal(raw, &m); e != nil {
		return e
	}
	profileRaw, e := os.ReadFile(rt.MachineProfilePath)
	if e != nil {
		return e
	}
	var profile struct {
		Python string `json:"python_binary"`
	}
	if e = json.Unmarshal(profileRaw, &profile); e != nil {
		return e
	}
	pythonBinary := profile.Python
	if pythonBinary == "" {
		pythonBinary = "python"
	}
	cmd := exec.CommandContext(ctx, *exe, "montage-script-run", "--manifest", *manifest, "--skill-root", filepath.Join(os.Getenv("USERPROFILE"), ".codex", "skills", "jianying-montage-draft"), "--output-last-message", filepath.Join(m.OutputDir, "last-message.json"), "--python-binary", pythonBinary)
	cmd.Env = append(os.Environ(), "PYTHONIOENCODING=utf-8", "PYTHONUTF8=1", "VIDEO_CONSOLE_INTENT_API_KEY="+rt.RemixAPIKey, "VIDEO_CONSOLE_INTENT_BASE_URL="+rt.RemixBaseURL, "VIDEO_CONSOLE_INTENT_MODEL="+name)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	fmt.Println("Running isolated editorial montage; requested model:", name)
	return cmd.Run()
}
