package imagevideo

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/google/uuid"
)

var ErrInvalidRegistration = errors.New("invalid image video registration")

type RegistrationRequest struct {
	JobID, ProjectID, DisplayName, ManifestPath, WorkspacePath, OutputDir string
	TemplateVersion, TemplateFingerprint, DraftFingerprint, JianyingRoot  string
	MachineProfilePath, CoverPath                                         string
}

type RegistrationResult struct {
	RegisteredPath, ReceiptPath, DraftID, DisplayName             string
	SourceContentSHA256, RegisteredContentSHA256, DirectorySHA256 string
	DurationUS                                                    int64
}

type RegistrationBinding struct {
	JobID, ProjectID, ManifestPath, OutputDir, WorkspaceDir string
	TemplateVersion, TemplateFingerprint, DraftFingerprint  string
	MachineProfilePath                                      string
}

type Registrar interface {
	Register(context.Context, RegistrationRequest) (RegistrationResult, error)
}

type RegisteredDraftValidator func(RegistrationRequest, string) (RegistrationResult, error)

type HostRegistrar struct {
	dataRoot           string
	jianyingRoot       string
	machineProfilePath string
	validate           RegisteredDraftValidator
	now                func() time.Time
	lockRoot           string
}

func (r *HostRegistrar) WithLockRoot(path string) *HostRegistrar {
	if r != nil {
		r.lockRoot = path
	}
	return r
}

func NewHostRegistrar(dataRoot, jianyingRoot, machineProfilePath string, validate RegisteredDraftValidator) (*HostRegistrar, error) {
	if strings.TrimSpace(dataRoot) == "" || strings.TrimSpace(jianyingRoot) == "" || validate == nil {
		return nil, fmt.Errorf("%w: image video registrar dependencies are incomplete", ErrInvalidRegistration)
	}
	root, err := filepath.Abs(dataRoot)
	if err != nil {
		return nil, err
	}
	jianying, err := filepath.Abs(jianyingRoot)
	if err != nil {
		return nil, err
	}
	return &HostRegistrar{
		dataRoot:           root,
		jianyingRoot:       jianying,
		machineProfilePath: strings.TrimSpace(machineProfilePath),
		validate:           validate,
		now:                time.Now,
	}, nil
}

func BuiltInManifestSkillID() string {
	return uuid.NewSHA1(uuid.NameSpaceURL, []byte("video-production-console/"+BuiltInTemplateVersion)).String()
}

func ValidateRegistrationBinding(dataRoot string, job Job, request RegistrationRequest) (RegistrationBinding, error) {
	if strings.TrimSpace(job.ID) == "" || job.ID != strings.TrimSpace(request.JobID) {
		return RegistrationBinding{}, fmt.Errorf("%w: job identity does not match the registration request", ErrInvalidRegistration)
	}
	if _, err := uuid.Parse(job.ID); err != nil {
		return RegistrationBinding{}, fmt.Errorf("%w: job id is not a UUID", ErrInvalidRegistration)
	}
	if strings.TrimSpace(job.ProjectID) == "" || job.ProjectID != strings.TrimSpace(request.ProjectID) {
		return RegistrationBinding{}, fmt.Errorf("%w: project identity does not match the registration request", ErrInvalidRegistration)
	}
	if job.TemplateVersion != BuiltInTemplateVersion || request.TemplateVersion != BuiltInTemplateVersion {
		return RegistrationBinding{}, fmt.Errorf("%w: template version is not the built-in image video contract", ErrInvalidRegistration)
	}
	_, fingerprint, err := BuiltInTemplate()
	if err != nil {
		return RegistrationBinding{}, err
	}
	if !strings.EqualFold(job.TemplateFingerprint, fingerprint) || !strings.EqualFold(request.TemplateFingerprint, fingerprint) {
		return RegistrationBinding{}, fmt.Errorf("%w: template fingerprint does not match the built-in contract", ErrInvalidRegistration)
	}
	if strings.TrimSpace(job.DraftFingerprint) == "" || !strings.EqualFold(job.DraftFingerprint, request.DraftFingerprint) {
		return RegistrationBinding{}, fmt.Errorf("%w: draft fingerprint does not match the job record", ErrInvalidRegistration)
	}
	expectedOutput := filepath.ToSlash(filepath.Join(job.ID, "output"))
	expectedWorkspace := filepath.ToSlash(filepath.Join(job.ID, "output", "workspace", job.ID))
	expectedManifest := filepath.ToSlash(filepath.Join(job.ID, "output", "task_manifest.json"))
	if filepath.ToSlash(job.DraftRelativePath) != expectedWorkspace {
		return RegistrationBinding{}, fmt.Errorf("%w: job workspace is not task-bound", ErrInvalidRegistration)
	}
	if filepath.ToSlash(job.ManifestRelativePath) != expectedManifest {
		return RegistrationBinding{}, fmt.Errorf("%w: job manifest is not task-bound", ErrInvalidRegistration)
	}
	_, outputDir, err := ResolveManagedPath(dataRoot, job.ProjectID, expectedOutput)
	if err != nil {
		return RegistrationBinding{}, fmt.Errorf("%w: output directory is outside the job root", ErrInvalidRegistration)
	}
	_, workspace, err := ResolveManagedPath(dataRoot, job.ProjectID, expectedWorkspace)
	if err != nil {
		return RegistrationBinding{}, fmt.Errorf("%w: workspace is outside the job root", ErrInvalidRegistration)
	}
	_, manifest, err := ResolveManagedPath(dataRoot, job.ProjectID, expectedManifest)
	if err != nil {
		return RegistrationBinding{}, fmt.Errorf("%w: manifest is outside the job root", ErrInvalidRegistration)
	}
	if !sameCleanPath(outputDir, request.OutputDir) || !sameCleanPath(workspace, request.WorkspacePath) || !sameCleanPath(manifest, request.ManifestPath) {
		return RegistrationBinding{}, fmt.Errorf("%w: registration paths are not the job-bound locations", ErrInvalidRegistration)
	}
	if err := rejectSymlink(outputDir, true); err != nil {
		return RegistrationBinding{}, err
	}
	if err := rejectSymlink(workspace, true); err != nil {
		return RegistrationBinding{}, err
	}
	if err := rejectSymlink(manifest, false); err != nil {
		return RegistrationBinding{}, err
	}
	if err := verifyImageVideoManifest(manifest, job, request.DisplayName, outputDir, request.MachineProfilePath); err != nil {
		return RegistrationBinding{}, err
	}
	return RegistrationBinding{
		JobID: job.ID, ProjectID: job.ProjectID, ManifestPath: manifest, OutputDir: outputDir, WorkspaceDir: workspace,
		TemplateVersion: job.TemplateVersion, TemplateFingerprint: job.TemplateFingerprint, DraftFingerprint: job.DraftFingerprint,
		MachineProfilePath: request.MachineProfilePath,
	}, nil
}

func (r *HostRegistrar) Register(ctx context.Context, request RegistrationRequest) (RegistrationResult, error) {
	if r == nil || r.validate == nil {
		return RegistrationResult{}, fmt.Errorf("%w: image video registrar is not configured", ErrInvalidRegistration)
	}
	if err := ctx.Err(); err != nil {
		return RegistrationResult{}, err
	}
	if strings.TrimSpace(request.JobID) == "" || strings.TrimSpace(request.DisplayName) == "" || strings.TrimSpace(request.JianyingRoot) == "" {
		return RegistrationResult{}, fmt.Errorf("%w: missing registration identity", ErrInvalidRegistration)
	}
	if !sameCleanPath(request.JianyingRoot, r.jianyingRoot) {
		return RegistrationResult{}, fmt.Errorf("%w: Jianying root is not the trusted runtime root", ErrInvalidRegistration)
	}
	unlock, err := acquireRegistrationLock(r.lockRoot, request.JobID, r.now)
	if err != nil {
		return RegistrationResult{}, err
	}
	defer unlock()
	return r.publish(request)
}

func (r *HostRegistrar) publish(request RegistrationRequest) (RegistrationResult, error) {
	workspace, err := canonicalExisting(request.WorkspacePath, true)
	if err != nil {
		return RegistrationResult{}, fmt.Errorf("%w: workspace is unavailable", ErrInvalidRegistration)
	}
	outputDir, err := canonicalExisting(request.OutputDir, true)
	if err != nil {
		return RegistrationResult{}, fmt.Errorf("%w: output directory is unavailable", ErrInvalidRegistration)
	}
	if !sameCleanPath(workspace, filepath.Join(outputDir, "workspace", request.JobID)) {
		return RegistrationResult{}, fmt.Errorf("%w: workspace is not output/workspace/<jobID>", ErrInvalidRegistration)
	}
	sourceContent := filepath.Join(workspace, "draft_content.json")
	sourceMeta := filepath.Join(workspace, "draft_meta_info.json")
	if err := rejectSymlink(sourceContent, false); err != nil {
		return RegistrationResult{}, err
	}
	if err := rejectSymlink(sourceMeta, false); err != nil {
		return RegistrationResult{}, err
	}
	contentBytes, err := os.ReadFile(sourceContent)
	if err != nil {
		return RegistrationResult{}, fmt.Errorf("%w: source draft content is missing", ErrInvalidRegistration)
	}
	if err := rejectCaptionOrTitleTracks(contentBytes); err != nil {
		return RegistrationResult{}, err
	}
	var content struct {
		Duration int64 `json:"duration"`
	}
	if json.Unmarshal(contentBytes, &content) != nil || content.Duration <= 0 {
		return RegistrationResult{}, fmt.Errorf("%w: draft duration is missing", ErrInvalidRegistration)
	}
	sourceHash := sha256HexBytes(contentBytes)
	sourceDraftID, err := readDraftID(sourceMeta)
	if err != nil {
		return RegistrationResult{}, fmt.Errorf("%w: source draft ID is unavailable", ErrInvalidRegistration)
	}
	root, err := canonicalExisting(r.jianyingRoot, true)
	if err != nil {
		if !errors.Is(err, os.ErrNotExist) {
			return RegistrationResult{}, fmt.Errorf("%w: Jianying root is unavailable", ErrInvalidRegistration)
		}
		if mkErr := os.MkdirAll(r.jianyingRoot, 0o700); mkErr != nil {
			return RegistrationResult{}, fmt.Errorf("%w: create Jianying root", ErrInvalidRegistration)
		}
		root, err = canonicalExisting(r.jianyingRoot, true)
		if err != nil {
			return RegistrationResult{}, fmt.Errorf("%w: Jianying root is unavailable", ErrInvalidRegistration)
		}
	}
	target := filepath.Join(root, request.JobID)
	receiptDir := filepath.Join(outputDir, "registration")
	if err := os.MkdirAll(receiptDir, 0o700); err != nil {
		return RegistrationResult{}, err
	}
	receiptPath := filepath.Join(receiptDir, "registration-result.json")
	if _, err := os.Lstat(target); err == nil {
		return r.validateExisting(request, workspace, target, receiptPath, sourceHash, sourceDraftID, content.Duration)
	} else if !errors.Is(err, os.ErrNotExist) {
		return RegistrationResult{}, err
	}
	staging := filepath.Join(root, "."+request.JobID+".staging")
	_ = os.RemoveAll(staging)
	if err := copyDraftTree(workspace, staging); err != nil {
		_ = os.RemoveAll(staging)
		return RegistrationResult{}, err
	}
	draftID := sourceDraftID
	if conflict, err := draftIDConflicts(root, draftID, request.DisplayName, target); err != nil {
		_ = os.RemoveAll(staging)
		return RegistrationResult{}, err
	} else if conflict {
		draftID = uuid.NewString()
	}
	if err := writeStagedMeta(staging, target, root, draftID, request.DisplayName, content.Duration, r.now()); err != nil {
		_ = os.RemoveAll(staging)
		return RegistrationResult{}, err
	}
	if err := maybeCopyCover(request.CoverPath, filepath.Join(staging, "draft_cover.png")); err != nil {
		_ = os.RemoveAll(staging)
		return RegistrationResult{}, err
	}
	if err := os.Rename(staging, target); err != nil {
		_ = os.RemoveAll(staging)
		return RegistrationResult{}, fmt.Errorf("%w: publish registered draft: %v", ErrInvalidRegistration, err)
	}
	if err := upsertRootIndex(root, target, draftID, request.DisplayName, content.Duration, r.now()); err != nil {
		return RegistrationResult{}, err
	}
	registeredHash := sourceHash
	if hashed, hashErr := hashFile(filepath.Join(target, "draft_content.json")); hashErr == nil {
		registeredHash = hashed
	}
	if err := writeReceipt(receiptPath, request, target, draftID, sourceDraftID, content.Duration, sourceHash, registeredHash); err != nil {
		return RegistrationResult{}, err
	}
	return r.validate(request, receiptPath)
}

func (r *HostRegistrar) validateExisting(request RegistrationRequest, workspace, target, receiptPath, sourceHash, sourceDraftID string, durationUS int64) (RegistrationResult, error) {
	if err := rejectSymlink(target, true); err != nil {
		return RegistrationResult{}, err
	}
	registeredHash, err := hashFile(filepath.Join(target, "draft_content.json"))
	if err != nil || !strings.EqualFold(registeredHash, sourceHash) {
		return RegistrationResult{}, fmt.Errorf("%w: existing registered content does not match the job workspace", ErrInvalidRegistration)
	}
	registeredID, registeredName, err := readDraftMeta(filepath.Join(target, "draft_meta_info.json"))
	if err != nil || registeredName != request.DisplayName {
		return RegistrationResult{}, fmt.Errorf("%w: existing registered metadata does not match the job", ErrInvalidRegistration)
	}
	if _, err := os.Stat(receiptPath); errors.Is(err, os.ErrNotExist) {
		if err := writeReceipt(receiptPath, request, target, registeredID, sourceDraftID, durationUS, sourceHash, registeredHash); err != nil {
			return RegistrationResult{}, err
		}
	}
	_ = workspace
	return r.validate(request, receiptPath)
}

func verifyImageVideoManifest(path string, job Job, displayName, outputDir, machineProfilePath string) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("%w: task manifest is missing", ErrInvalidRegistration)
	}
	var manifest struct {
		SchemaVersion     string `json:"schema_version"`
		TaskID            string `json:"task_id"`
		JobID             string `json:"job_id"`
		Skill             string `json:"skill"`
		Action            string `json:"action"`
		OutputDir         string `json:"output_dir"`
		SkillSnapshotID   string `json:"skill_snapshot_id"`
		NonSecretSettings struct {
			MachineProfilePath string `json:"machine_profile_path"`
			DraftDisplayName   string `json:"draft_display_name"`
		} `json:"non_secret_settings"`
		Inputs            []any `json:"inputs"`
		EngineeringInputs []any `json:"engineering_inputs"`
	}
	if json.Unmarshal(data, &manifest) != nil {
		return fmt.Errorf("%w: task manifest is invalid", ErrInvalidRegistration)
	}
	if manifest.SchemaVersion != "2.0" || manifest.TaskID != job.ID || manifest.JobID != job.ID || manifest.TaskID != manifest.JobID {
		return fmt.Errorf("%w: task_id/job_id are not the job UUID", ErrInvalidRegistration)
	}
	if manifest.Skill != "jianying-image-video" || manifest.Action != "imagevideo.register" {
		return fmt.Errorf("%w: manifest is not the image-video registration contract", ErrInvalidRegistration)
	}
	if !sameCleanPath(manifest.OutputDir, outputDir) {
		return fmt.Errorf("%w: manifest output_dir is not the job output directory", ErrInvalidRegistration)
	}
	if manifest.SkillSnapshotID != BuiltInManifestSkillID() {
		return fmt.Errorf("%w: manifest skill snapshot is not the built-in image video contract", ErrInvalidRegistration)
	}
	if manifest.NonSecretSettings.DraftDisplayName != displayName {
		return fmt.Errorf("%w: manifest display name does not match the job", ErrInvalidRegistration)
	}
	if strings.TrimSpace(machineProfilePath) != "" && strings.TrimSpace(manifest.NonSecretSettings.MachineProfilePath) != "" && !sameCleanPath(manifest.NonSecretSettings.MachineProfilePath, machineProfilePath) {
		return fmt.Errorf("%w: machine profile does not match the trusted runtime", ErrInvalidRegistration)
	}
	if len(manifest.Inputs) != 0 || len(manifest.EngineeringInputs) != 0 {
		return fmt.Errorf("%w: image video registration must not reuse scenic montage inputs", ErrInvalidRegistration)
	}
	lower := strings.ToLower(string(data))
	for _, forbidden := range []string{"subtitle_srt", "account_background", "continuous_script", "authorization", "api_key", "api-key"} {
		if strings.Contains(lower, forbidden) {
			return fmt.Errorf("%w: manifest contains scenic or secret fields", ErrInvalidRegistration)
		}
	}
	return nil
}

func rejectCaptionOrTitleTracks(content []byte) error {
	var draft struct {
		Tracks []struct {
			Name string `json:"name"`
		} `json:"tracks"`
	}
	if json.Unmarshal(content, &draft) != nil {
		return fmt.Errorf("%w: draft content is invalid", ErrInvalidRegistration)
	}
	for _, track := range draft.Tracks {
		switch strings.TrimSpace(track.Name) {
		case "字幕", "字幕轨", "标题", "副标题", "重点字幕", "片头标题", "章节标签":
			return fmt.Errorf("%w: draft contains a forbidden caption or title track", ErrInvalidRegistration)
		}
	}
	return nil
}

func copyDraftTree(source, destination string) error {
	if err := os.MkdirAll(destination, 0o700); err != nil {
		return err
	}
	return filepath.WalkDir(source, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.Type()&os.ModeSymlink != 0 {
			return fmt.Errorf("%w: draft contains a symlink", ErrInvalidRegistration)
		}
		relative, err := filepath.Rel(source, path)
		if err != nil {
			return err
		}
		target := filepath.Join(destination, relative)
		if entry.IsDir() {
			return os.MkdirAll(target, 0o700)
		}
		return copyRegularFile(path, target)
	})
}

func copyRegularFile(source, destination string) error {
	if err := rejectSymlink(source, false); err != nil {
		return err
	}
	in, err := os.Open(source)
	if err != nil {
		return err
	}
	defer in.Close()
	if err := os.MkdirAll(filepath.Dir(destination), 0o700); err != nil {
		return err
	}
	out, err := os.OpenFile(destination, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
	if err != nil {
		return err
	}
	defer out.Close()
	if _, err := io.Copy(out, in); err != nil {
		return err
	}
	return out.Close()
}

func maybeCopyCover(source, destination string) error {
	if strings.TrimSpace(source) == "" {
		return nil
	}
	ext := strings.ToLower(filepath.Ext(source))
	switch ext {
	case ".png", ".jpg", ".jpeg", ".webp":
	default:
		return nil
	}
	if _, err := os.Stat(source); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return err
	}
	return copyRegularFile(source, destination)
}

func writeStagedMeta(staging, target, root, draftID, displayName string, durationUS int64, now time.Time) error {
	path := filepath.Join(staging, "draft_meta_info.json")
	data, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("%w: staged draft metadata is missing", ErrInvalidRegistration)
	}
	var meta map[string]any
	if json.Unmarshal(data, &meta) != nil {
		return fmt.Errorf("%w: staged draft metadata is invalid", ErrInvalidRegistration)
	}
	// Jianying reads tm_draft_* as microseconds; milliseconds render as 1970.
	nowUS := now.UTC().UnixMicro()
	meta["draft_id"] = draftID
	meta["draft_name"] = displayName
	meta["draft_root_path"] = filepath.ToSlash(root)
	meta["draft_fold_path"] = filepath.ToSlash(target)
	meta["draft_cover"] = filepath.ToSlash(filepath.Join(target, "draft_cover.png"))
	meta["tm_duration"] = durationUS
	meta["tm_draft_create"] = nowUS
	meta["tm_draft_modified"] = nowUS
	return writeJSONAtomic(path, meta)
}

func draftIDConflicts(root, draftID, displayName, target string) (bool, error) {
	path := filepath.Join(root, "root_meta_info.json")
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	var raw struct {
		Entries []map[string]any `json:"all_draft_store"`
	}
	if json.Unmarshal(data, &raw) != nil {
		return false, fmt.Errorf("%w: Jianying root index is invalid", ErrInvalidRegistration)
	}
	targetSlash := strings.ToLower(filepath.ToSlash(target))
	for _, entry := range raw.Entries {
		id, _ := entry["draft_id"].(string)
		name, _ := entry["draft_name"].(string)
		fold, _ := entry["draft_fold_path"].(string)
		if id == draftID || name == displayName || strings.ToLower(filepath.ToSlash(fold)) == targetSlash {
			return true, nil
		}
	}
	return false, nil
}

func upsertRootIndex(root, target, draftID, displayName string, durationUS int64, now time.Time) error {
	path := filepath.Join(root, "root_meta_info.json")
	raw := map[string]any{"all_draft_store": []any{}}
	if data, err := os.ReadFile(path); err == nil {
		if json.Unmarshal(data, &raw) != nil {
			return fmt.Errorf("%w: Jianying root index is invalid", ErrInvalidRegistration)
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	entries, _ := raw["all_draft_store"].([]any)
	if entries == nil {
		entries = []any{}
	}
	targetSlash := filepath.ToSlash(target)
	replaced := false
	for index, value := range entries {
		entry, _ := value.(map[string]any)
		if entry == nil {
			continue
		}
		id, _ := entry["draft_id"].(string)
		fold, _ := entry["draft_fold_path"].(string)
		name, _ := entry["draft_name"].(string)
		if id == draftID || name == displayName || strings.EqualFold(filepath.ToSlash(fold), targetSlash) {
			entries[index] = registrationIndexEntry(root, target, draftID, displayName, durationUS, now, entry)
			replaced = true
			break
		}
	}
	if !replaced {
		entries = append(entries, registrationIndexEntry(root, target, draftID, displayName, durationUS, now, nil))
	}
	raw["all_draft_store"] = entries
	raw["draft_ids"] = len(entries)
	raw["root_path"] = filepath.ToSlash(root)
	return writeJSONAtomic(path, raw)
}

func registrationIndexEntry(root, target, draftID, displayName string, durationUS int64, now time.Time, existing map[string]any) map[string]any {
	version := "179.0.0"
	if existing != nil {
		if value, _ := existing["draft_new_version"].(string); strings.TrimSpace(value) != "" {
			version = value
		}
	}
	nowUS := now.UTC().UnixMicro()
	return map[string]any{
		"draft_cover":                filepath.ToSlash(filepath.Join(target, "draft_cover.png")),
		"draft_fold_path":            filepath.ToSlash(target),
		"draft_id":                   draftID,
		"draft_is_ai_shorts":         false,
		"draft_json_file":            filepath.ToSlash(filepath.Join(target, "draft_content.json")),
		"draft_name":                 displayName,
		"draft_new_version":          version,
		"draft_root_path":            filepath.ToSlash(root),
		"draft_type":                 "",
		"tm_draft_create":            nowUS,
		"tm_draft_modified":          nowUS,
		"tm_duration":                durationUS,
		"streaming_edit_draft_ready": true,
	}
}

func writeReceipt(path string, request RegistrationRequest, registeredPath, draftID, sourceDraftID string, durationUS int64, sourceHash, registeredHash string) error {
	rekeyed := sourceDraftID != draftID
	receipt := map[string]any{
		"status":                    "completed",
		"task_id":                   request.JobID,
		"draft_display_name":        request.DisplayName,
		"registered_path":           registeredPath,
		"draft_id":                  draftID,
		"source_draft_id":           sourceDraftID,
		"draft_id_rekeyed":          rekeyed,
		"duration_us":               durationUS,
		"source_content_sha256":     sourceHash,
		"registered_content_sha256": registeredHash,
	}
	return writeJSONAtomic(path, receipt)
}

func writeJSONAtomic(path string, value any) error {
	encoded, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	encoded = append(encoded, '\n')
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	temp, err := os.CreateTemp(filepath.Dir(path), ".image-video-*.tmp")
	if err != nil {
		return err
	}
	tempPath := temp.Name()
	defer os.Remove(tempPath)
	if err := temp.Chmod(0o600); err != nil {
		temp.Close()
		return err
	}
	if _, err := temp.Write(encoded); err != nil {
		temp.Close()
		return err
	}
	if err := temp.Close(); err != nil {
		return err
	}
	return os.Rename(tempPath, path)
}

func acquireRegistrationLock(lockRoot, jobID string, now func() time.Time) (func(), error) {
	root := lockRoot
	if strings.TrimSpace(root) == "" {
		localAppData := os.Getenv("LOCALAPPDATA")
		if localAppData == "" {
			home := os.Getenv("HOME")
			if home == "" {
				home, _ = os.UserHomeDir()
			}
			localAppData = filepath.Join(home, "AppData", "Local")
		}
		root = filepath.Join(localAppData, "Codex", "jianying-montage-draft", "locks")
	}
	if err := os.MkdirAll(root, 0o700); err != nil {
		return nil, err
	}
	lockDir := filepath.Join(root, "jianying-registration.lock")
	deadline := now().Add(30 * time.Second)
	for {
		if err := os.Mkdir(lockDir, 0o700); err == nil {
			owner := map[string]any{"job_id": jobID, "owner_pid": os.Getpid(), "lease_expires_at_epoch": now().Add(2 * time.Minute).Unix()}
			_ = writeJSONAtomic(filepath.Join(lockDir, "owner.json"), owner)
			return func() { _ = os.RemoveAll(lockDir) }, nil
		}
		if now().After(deadline) {
			return nil, fmt.Errorf("%w: Jianying registration lock is busy", ErrInvalidRegistration)
		}
		time.Sleep(200 * time.Millisecond)
	}
}

func rejectSymlink(path string, directory bool) error {
	info, err := os.Lstat(path)
	if err != nil {
		return fmt.Errorf("%w: path is unavailable", ErrInvalidRegistration)
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("%w: path is a symlink", ErrInvalidRegistration)
	}
	if directory && !info.IsDir() {
		return fmt.Errorf("%w: path is not a directory", ErrInvalidRegistration)
	}
	if !directory && info.IsDir() {
		return fmt.Errorf("%w: path is not a file", ErrInvalidRegistration)
	}
	return nil
}

func canonicalExisting(path string, directory bool) (string, error) {
	absolute, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	if err := rejectSymlink(absolute, directory); err != nil {
		return "", err
	}
	return filepath.Clean(absolute), nil
}

func readDraftID(path string) (string, error) {
	id, _, err := readDraftMeta(path)
	return id, err
}

func readDraftMeta(path string) (string, string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", "", err
	}
	var meta struct {
		DraftID   string `json:"draft_id"`
		DraftName string `json:"draft_name"`
	}
	if json.Unmarshal(data, &meta) != nil || strings.TrimSpace(meta.DraftID) == "" {
		return "", "", errors.New("draft_meta_info.json has no draft_id")
	}
	return meta.DraftID, meta.DraftName, nil
}

func hashFile(path string) (string, error) {
	file, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer file.Close()
	hash := sha256.New()
	if _, err := io.Copy(hash, file); err != nil {
		return "", err
	}
	return hex.EncodeToString(hash.Sum(nil)), nil
}

func sha256HexBytes(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

func sameCleanPath(left, right string) bool {
	if strings.TrimSpace(left) == "" || strings.TrimSpace(right) == "" {
		return false
	}
	a, errA := filepath.Abs(left)
	b, errB := filepath.Abs(right)
	if errA != nil || errB != nil {
		return false
	}
	if filepath.Separator == '\\' {
		return strings.EqualFold(filepath.Clean(a), filepath.Clean(b))
	}
	return filepath.Clean(a) == filepath.Clean(b)
}
