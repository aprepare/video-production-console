package imagevideo

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

var ErrDraftBuild = errors.New("image_video_draft_build_failed")

type DraftCommandRunner interface {
	Run(context.Context, string, ...string) (stdout, stderr []byte, err error)
}

type execDraftCommandRunner struct{}

func (execDraftCommandRunner) Run(ctx context.Context, binary string, args ...string) ([]byte, []byte, error) {
	command := exec.CommandContext(ctx, binary, args...)
	var stdout, stderr bytes.Buffer
	command.Stdout = &stdout
	command.Stderr = &stderr
	command.WaitDelay = 5 * time.Second
	err := command.Run()
	if ctxErr := ctx.Err(); ctxErr != nil {
		err = ctxErr
	}
	return stdout.Bytes(), stderr.Bytes(), err
}

type DraftSceneInput struct {
	StartUS, DurationUS int64
	MediaPath           string
	Motion              string
}

type DraftBuildRequest struct {
	JobID, DisplayName, OutputRoot, PythonBinary, ScriptPath, NarrationPath string
	OutputMode                                                              OutputMode
	DurationUS                                                              int64
	Scenes                                                                  []DraftSceneInput
	Runner                                                                  DraftCommandRunner
}

type DraftArtifact struct {
	WorkspacePath, DraftRelativePath, Fingerprint string
	DurationUS, SceneCount                        int64
}

type draftPlan struct {
	JobID         string           `json:"job_id"`
	DisplayName   string           `json:"display_name"`
	OutputMode    OutputMode       `json:"output_mode"`
	NarrationPath string           `json:"narration_path"`
	DurationUS    int64            `json:"duration_us"`
	Scenes        []draftPlanScene `json:"scenes"`
}

type draftPlanScene struct {
	StartUS    int64  `json:"start_us"`
	DurationUS int64  `json:"duration_us"`
	MediaPath  string `json:"media_path"`
	Motion     string `json:"motion,omitempty"`
}

func BuildDraft(ctx context.Context, request DraftBuildRequest) (DraftArtifact, error) {
	if strings.TrimSpace(request.JobID) == "" || request.JobID == "." || request.JobID == ".." || strings.ContainsAny(request.JobID, `/\\:`) || strings.TrimSpace(request.DisplayName) == "" {
		return DraftArtifact{}, fmt.Errorf("%w: invalid job identity", ErrDraftBuild)
	}
	if request.OutputMode != ModeSlideshow && request.OutputMode != ModeImageToVideo {
		return DraftArtifact{}, fmt.Errorf("%w: invalid output mode", ErrDraftBuild)
	}
	if request.DurationUS <= 0 || strings.TrimSpace(request.OutputRoot) == "" || strings.TrimSpace(request.PythonBinary) == "" || strings.TrimSpace(request.ScriptPath) == "" || strings.TrimSpace(request.NarrationPath) == "" || len(request.Scenes) == 0 {
		return DraftArtifact{}, fmt.Errorf("%w: incomplete draft request", ErrDraftBuild)
	}
	if request.Runner == nil {
		request.Runner = execDraftCommandRunner{}
	}
	outputRoot, err := filepath.Abs(request.OutputRoot)
	if err != nil {
		return DraftArtifact{}, fmt.Errorf("%w: output root", ErrDraftBuild)
	}
	if err := os.MkdirAll(outputRoot, 0o700); err != nil {
		return DraftArtifact{}, fmt.Errorf("%w: create output root", ErrDraftBuild)
	}
	if _, err := os.Stat(request.NarrationPath); err != nil {
		return DraftArtifact{}, fmt.Errorf("%w: narration is unavailable", ErrDraftBuild)
	}
	plan := draftPlan{JobID: request.JobID, DisplayName: request.DisplayName, OutputMode: request.OutputMode, NarrationPath: request.NarrationPath, DurationUS: request.DurationUS, Scenes: make([]draftPlanScene, 0, len(request.Scenes))}
	previous := int64(0)
	for _, scene := range request.Scenes {
		mediaPath, err := filepath.Abs(scene.MediaPath)
		if err != nil {
			return DraftArtifact{}, fmt.Errorf("%w: scene media path", ErrDraftBuild)
		}
		if scene.StartUS != previous || scene.DurationUS <= 0 {
			return DraftArtifact{}, fmt.Errorf("%w: scene timeline is not continuous", ErrDraftBuild)
		}
		if _, err := os.Stat(mediaPath); err != nil {
			return DraftArtifact{}, fmt.Errorf("%w: scene media is unavailable", ErrDraftBuild)
		}
		if request.OutputMode == ModeSlideshow && !allowedSceneMotion(scene.Motion) {
			return DraftArtifact{}, fmt.Errorf("%w: scene motion is not allowed", ErrDraftBuild)
		}
		if request.OutputMode == ModeImageToVideo && scene.Motion != "" && scene.Motion != "none" {
			return DraftArtifact{}, fmt.Errorf("%w: generated-video scene has slideshow motion", ErrDraftBuild)
		}
		plan.Scenes = append(plan.Scenes, draftPlanScene{StartUS: scene.StartUS, DurationUS: scene.DurationUS, MediaPath: mediaPath, Motion: scene.Motion})
		previous = scene.StartUS + scene.DurationUS
	}
	if previous != request.DurationUS {
		return DraftArtifact{}, fmt.Errorf("%w: scenes do not cover narration", ErrDraftBuild)
	}
	planBytes, err := json.MarshalIndent(plan, "", "  ")
	if err != nil {
		return DraftArtifact{}, fmt.Errorf("%w: encode plan", ErrDraftBuild)
	}
	planPath := filepath.Join(outputRoot, "draft-plan.json")
	temporary, err := os.CreateTemp(outputRoot, ".draft-plan-*.json")
	if err != nil {
		return DraftArtifact{}, fmt.Errorf("%w: create plan", ErrDraftBuild)
	}
	temporaryPath := temporary.Name()
	defer os.Remove(temporaryPath)
	if err := temporary.Chmod(0o600); err != nil {
		_ = temporary.Close()
		return DraftArtifact{}, fmt.Errorf("%w: protect plan", ErrDraftBuild)
	}
	if _, err := temporary.Write(planBytes); err != nil {
		_ = temporary.Close()
		return DraftArtifact{}, fmt.Errorf("%w: write plan", ErrDraftBuild)
	}
	if err := temporary.Sync(); err != nil {
		_ = temporary.Close()
		return DraftArtifact{}, fmt.Errorf("%w: sync plan", ErrDraftBuild)
	}
	if err := temporary.Close(); err != nil {
		return DraftArtifact{}, fmt.Errorf("%w: close plan", ErrDraftBuild)
	}
	if err := os.Rename(temporaryPath, planPath); err != nil {
		return DraftArtifact{}, fmt.Errorf("%w: commit plan", ErrDraftBuild)
	}
	stdout, stderr, err := request.Runner.Run(ctx, request.PythonBinary, request.ScriptPath, "--plan", planPath, "--output-root", outputRoot)
	if err != nil {
		return DraftArtifact{}, fmt.Errorf("%w: python builder failed: %s", ErrDraftBuild, truncateDraftError(stderr, err))
	}
	var summary struct {
		Status           string `json:"status"`
		JobID            string `json:"job_id"`
		Workspace        string `json:"workspace"`
		DraftFingerprint string `json:"draft_fingerprint"`
		DurationUS       int64  `json:"duration_us"`
		Scenes           int    `json:"scenes"`
	}
	if err := json.Unmarshal(stdout, &summary); err != nil || summary.Status != "built" || summary.JobID != request.JobID || summary.DurationUS != request.DurationUS || summary.Scenes != len(request.Scenes) {
		return DraftArtifact{}, fmt.Errorf("%w: builder returned an invalid summary", ErrDraftBuild)
	}
	expectedWorkspace := filepath.Join(outputRoot, "workspace", request.JobID)
	if !sameDraftPath(summary.Workspace, expectedWorkspace) {
		return DraftArtifact{}, fmt.Errorf("%w: builder workspace is not task-bound", ErrDraftBuild)
	}
	contentPath := filepath.Join(expectedWorkspace, "draft_content.json")
	metaPath := filepath.Join(expectedWorkspace, "draft_meta_info.json")
	fingerprint, err := hashDraftFiles(contentPath, metaPath)
	if err != nil {
		return DraftArtifact{}, fmt.Errorf("%w: hash draft", ErrDraftBuild)
	}
	if summary.DraftFingerprint != "" && !strings.EqualFold(summary.DraftFingerprint, fingerprint) {
		return DraftArtifact{}, fmt.Errorf("%w: draft fingerprint mismatch", ErrDraftBuild)
	}
	return DraftArtifact{WorkspacePath: expectedWorkspace, DraftRelativePath: filepath.ToSlash(filepath.Join("output", "workspace", request.JobID)), Fingerprint: fingerprint, DurationUS: request.DurationUS, SceneCount: int64(len(request.Scenes))}, nil
}

func allowedSceneMotion(value string) bool {
	switch value {
	case "subtle_zoom_in", "subtle_zoom_out", "subtle_pan_left", "subtle_pan_right":
		return true
	default:
		return false
	}
}

func truncateDraftError(stderr []byte, commandErr error) string {
	value := strings.TrimSpace(string(stderr))
	if commandErr != nil {
		if value != "" {
			value += ": "
		}
		value += commandErr.Error()
	}
	runes := []rune(value)
	if len(runes) > 2000 {
		return string(runes[:2000]) + "…"
	}
	return value
}

func sameDraftPath(left, right string) bool {
	a, errA := filepath.Abs(left)
	b, errB := filepath.Abs(right)
	return errA == nil && errB == nil && strings.EqualFold(filepath.Clean(a), filepath.Clean(b))
}

func hashDraftFiles(paths ...string) (string, error) {
	hash := sha256.New()
	for _, path := range paths {
		data, err := os.ReadFile(path)
		if err != nil {
			return "", err
		}
		_, _ = hash.Write(data)
	}
	return hex.EncodeToString(hash.Sum(nil)), nil
}
