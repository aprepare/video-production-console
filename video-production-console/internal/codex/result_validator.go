package codex

import (
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"mime"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/google/uuid"
	"video-production-console/internal/domain"
)

var sha256Pattern = regexp.MustCompile(`^[a-fA-F0-9]{64}$`)

var formalAssetTypes = map[domain.AssetType]bool{
	domain.AssetSourceScript: true, domain.AssetTopicCard: true, domain.AssetContinuousScript: true,
	domain.AssetSpokenScript: true, domain.AssetCaptionKeywords: true,
	domain.AssetNarration: true, domain.AssetSubtitleSRT: true,
	domain.AssetWordTiming: true,
	domain.AssetAccountBackground: true, domain.AssetMixDraft: true, domain.AssetFinalVideo: true,
}

func ValidateResultEnvelopeJSON(data []byte, expectedTaskID string, expectedAction domain.TaskAction, outputDir string) (ResultEnvelope, error) {
	return ValidateResultEnvelopeJSONWithRoots(data, expectedTaskID, expectedAction, outputDir, ManifestRoots{})
}

func ValidateResultEnvelopeJSONWithRoots(data []byte, expectedTaskID string, expectedAction domain.TaskAction, outputDir string, roots ManifestRoots) (ResultEnvelope, error) {
	if err := rejectDuplicateJSONKeys(data); err != nil {
		return ResultEnvelope{}, err
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(data, &fields); err != nil {
		return ResultEnvelope{}, fmt.Errorf("decode result envelope: %w", err)
	}
	required := []string{"schema_version", "task_id", "action", "status", "summary", "questions", "artifacts", "asset_outputs", "warnings"}
	if err := validateExactJSONFields(fields, required, required); err != nil {
		return ResultEnvelope{}, fmt.Errorf("result envelope fields: %w", err)
	}
	if err := normalizeNestedResultFields(fields); err != nil {
		return ResultEnvelope{}, err
	}
	normalizedData, err := json.Marshal(fields)
	if err != nil {
		return ResultEnvelope{}, fmt.Errorf("encode normalized result envelope: %w", err)
	}
	decoder := json.NewDecoder(bytes.NewReader(normalizedData))
	decoder.DisallowUnknownFields()
	var envelope ResultEnvelope
	if err := decoder.Decode(&envelope); err != nil {
		return ResultEnvelope{}, fmt.Errorf("decode result envelope: %w", err)
	}
	if err := ensureJSONEOF(decoder); err != nil {
		return ResultEnvelope{}, err
	}
	if envelope.Status == "needs_input" {
		envelope.Status = "awaiting_input"
	}
	if err := ValidateResultEnvelopeWithRoots(envelope, expectedTaskID, expectedAction, outputDir, roots); err != nil {
		return ResultEnvelope{}, err
	}
	return envelope, nil
}

// ValidateResultEnvelopeAgainstManifest uses the same trusted roots that
// published the manifest and enforces every required declared output.
func ValidateResultEnvelopeAgainstManifest(envelope ResultEnvelope, manifest TaskManifest, roots ManifestRoots) error {
	if err := ValidateResultEnvelopeWithRoots(envelope, manifest.TaskID, manifest.Action, manifest.OutputDir, roots); err != nil {
		return err
	}
	if envelope.Status != "completed" {
		return nil
	}
	delivered := map[string]bool{}
	for _, artifact := range envelope.Artifacts {
		delivered[artifact.Type] = true
	}
	for _, asset := range envelope.AssetOutputs {
		delivered[string(asset.Type)] = true
	}
	for _, output := range manifest.ExpectedOutputs {
		if output.Required && !delivered[output.Type] {
			return fmt.Errorf("completed result did not deliver required output %q", output.Type)
		}
	}
	return nil
}

func normalizeNestedResultFields(fields map[string]json.RawMessage) error {
	for _, field := range []string{"schema_version", "task_id", "action", "status", "summary"} {
		if err := requireJSONString(fields[field]); err != nil {
			return fmt.Errorf("result field %q: %w", field, err)
		}
	}
	for _, field := range []string{"questions", "artifacts", "asset_outputs", "warnings"} {
		if err := requireJSONArray(fields[field]); err != nil {
			return fmt.Errorf("result field %q: %w", field, err)
		}
	}
	var rawQuestions []json.RawMessage
	if err := json.Unmarshal(fields["questions"], &rawQuestions); err != nil {
		return fmt.Errorf("inspect question fields: %w", err)
	}
	questions := make([]Question, 0, len(rawQuestions))
	for i, raw := range rawQuestions {
		if err := requireJSONString(raw); err == nil {
			var text string
			_ = json.Unmarshal(raw, &text)
			questions = append(questions, Question{Text: text, Options: []string{text}})
			continue
		}
		var question map[string]json.RawMessage
		if err := json.Unmarshal(raw, &question); err != nil {
			return fmt.Errorf("question %d must be a string or object", i)
		}
		if err := validateExactJSONFields(question, []string{"text", "options"}, []string{"text", "options"}); err != nil {
			return fmt.Errorf("question %d fields: %w", i, err)
		}
		if err := requireJSONString(question["text"]); err != nil {
			return fmt.Errorf("question %d text: %w", i, err)
		}
		if err := requireJSONArray(question["options"]); err != nil {
			return fmt.Errorf("question %d options: %w", i, err)
		}
		var options []json.RawMessage
		if err := json.Unmarshal(question["options"], &options); err != nil {
			return err
		}
		for j, option := range options {
			if err := requireJSONString(option); err != nil {
				return fmt.Errorf("question %d option %d: %w", i, j, err)
			}
		}
		var decoded Question
		if err := json.Unmarshal(raw, &decoded); err != nil {
			return err
		}
		questions = append(questions, decoded)
	}
	fields["questions"], _ = json.Marshal(questions)
	var rawArtifacts []json.RawMessage
	if err := json.Unmarshal(fields["artifacts"], &rawArtifacts); err != nil {
		return fmt.Errorf("inspect artifact fields: %w", err)
	}
	artifacts := make([]ArtifactOutput, 0, len(rawArtifacts))
	for i, raw := range rawArtifacts {
		var artifact map[string]json.RawMessage
		if err := json.Unmarshal(raw, &artifact); err != nil {
			return fmt.Errorf("artifact %d must be an object", i)
		}
		allowed := []string{"type", "path", "description", "relative_path", "sha256", "kind", "metadata"}
		if err := validateExactJSONFields(artifact, allowed, []string{"type", "path"}); err != nil {
			return fmt.Errorf("artifact %d fields: %w", i, err)
		}
		for _, field := range []string{"type", "path"} {
			if err := requireJSONString(artifact[field]); err != nil {
				return fmt.Errorf("artifact %d field %q: %w", i, field, err)
			}
		}
		if rawMetadata, ok := artifact["metadata"]; ok {
			var artifactType, kind string
			_ = json.Unmarshal(artifact["type"], &artifactType)
			if rawKind, hasKind := artifact["kind"]; hasKind {
				_ = json.Unmarshal(rawKind, &kind)
			}
			if artifactType != "plaintext_workspace" || kind != "directory" {
				return fmt.Errorf("artifact %d metadata is only allowed for a plaintext_workspace directory", i)
			}
			var metadata map[string]json.RawMessage
			if err := json.Unmarshal(rawMetadata, &metadata); err != nil {
				return fmt.Errorf("artifact %d metadata: must be an object", i)
			}
			presenceFields := []string{"narration_present", "bgm_present", "sfx_present", "transitions_present"}
			if err := validateExactJSONFields(metadata, presenceFields, presenceFields); err != nil {
				return fmt.Errorf("artifact %d metadata: %w", i, err)
			}
			for _, name := range presenceFields {
				var present bool
				if err := json.Unmarshal(metadata[name], &present); err != nil {
					return fmt.Errorf("artifact %d metadata %s must be boolean", i, name)
				}
				if !present {
					return fmt.Errorf("artifact %d metadata %s must be true for a completed workspace", i, name)
				}
			}
		}
		var decoded ArtifactOutput
		if err := json.Unmarshal(raw, &decoded); err != nil {
			return err
		}
		if kind, ok := artifact["kind"]; ok {
			if _, has := artifact["description"]; has {
				return fmt.Errorf("artifact %d mixes kind and description", i)
			}
			if err := requireJSONString(kind); err != nil {
				return fmt.Errorf("artifact %d kind: %w", i, err)
			}
			_ = json.Unmarshal(kind, &decoded.Description)
		} else if err := requireJSONString(artifact["description"]); err != nil {
			return fmt.Errorf("artifact %d description: %w", i, err)
		}
		for _, field := range []string{"relative_path", "sha256"} {
			if value, ok := artifact[field]; ok {
				if err := requireJSONString(value); err != nil {
					return fmt.Errorf("artifact %d %s: %w", i, field, err)
				}
			}
		}
		artifacts = append(artifacts, decoded)
	}
	fields["artifacts"], _ = json.Marshal(artifacts)
	var rawAssets []json.RawMessage
	if err := json.Unmarshal(fields["asset_outputs"], &rawAssets); err != nil {
		return fmt.Errorf("inspect asset output fields: %w", err)
	}
	assets := make([]AssetOutput, 0, len(rawAssets))
	for i, raw := range rawAssets {
		var object map[string]json.RawMessage
		if err := json.Unmarshal(raw, &object); err != nil {
			return fmt.Errorf("asset output %d must be an object", i)
		}
		if _, compatKind := object["kind"]; compatKind {
			if err := validateExactJSONFields(object, []string{"type", "kind", "path", "metadata"}, []string{"type", "kind", "path", "metadata"}); err != nil {
				return fmt.Errorf("asset output %d fields: %w", i, err)
			}
			for _, name := range []string{"type", "kind", "path"} {
				if err := requireJSONString(object[name]); err != nil {
					return fmt.Errorf("asset output %d %s: %w", i, name, err)
				}
			}
			var metadata map[string]json.RawMessage
			if err := json.Unmarshal(object["metadata"], &metadata); err != nil {
				return fmt.Errorf("asset output %d metadata: %w", i, err)
			}
			if err := validateExactJSONFields(metadata, []string{"registered_path", "narration_present", "bgm_present", "sfx_present", "transitions_present"}, nil); err != nil {
				return fmt.Errorf("asset output %d metadata: %w", i, err)
			}
			if value, ok := metadata["registered_path"]; ok {
				trimmed := bytes.TrimSpace(value)
				if !bytes.Equal(trimmed, []byte("null")) && requireJSONString(value) != nil {
					return fmt.Errorf("asset output %d registered_path must be string or null", i)
				}
			}
			for _, name := range []string{"narration_present", "bgm_present", "sfx_present", "transitions_present"} {
				if value, ok := metadata[name]; ok {
					var flag bool
					if err := json.Unmarshal(value, &flag); err != nil {
						return fmt.Errorf("asset output %d metadata %s must be boolean", i, name)
					}
				}
			}
			var typ, kind, path string
			_ = json.Unmarshal(object["type"], &typ)
			_ = json.Unmarshal(object["kind"], &kind)
			_ = json.Unmarshal(object["path"], &path)
			asset, err := normalizeCompatibilityAsset(domain.AssetType(typ), kind, path)
			if err != nil {
				return fmt.Errorf("asset output %d: %w", i, err)
			}
			assets = append(assets, asset)
			continue
		}
		var asset AssetOutput
		if err := json.Unmarshal(raw, &asset); err != nil {
			return fmt.Errorf("asset output %d: %w", i, err)
		}
		assets = append(assets, asset)
	}
	fields["asset_outputs"], _ = json.Marshal(assets)
	var warnings []json.RawMessage
	if err := json.Unmarshal(fields["warnings"], &warnings); err != nil {
		return err
	}
	for i, warning := range warnings {
		if err := requireJSONString(warning); err != nil {
			return fmt.Errorf("warning %d: %w", i, err)
		}
	}
	return nil
}

func normalizeCompatibilityAsset(typ domain.AssetType, kind, path string) (AssetOutput, error) {
	info, err := os.Stat(path)
	if err != nil {
		return AssetOutput{}, fmt.Errorf("inspect compatibility output: %w", err)
	}
	asset := AssetOutput{Type: typ, Path: path, Filename: filepath.Base(path), Size: info.Size()}
	switch kind {
	case "directory":
		if !info.IsDir() {
			return AssetOutput{}, fmt.Errorf("kind=directory does not name a directory")
		}
		asset.StorageKind, asset.MIME, asset.Size = domain.StorageDirectory, "inode/directory", 0
		asset.SHA256, err = HashResultDirectory(path)
	case "file":
		if !info.Mode().IsRegular() {
			return AssetOutput{}, fmt.Errorf("kind=file does not name a regular file")
		}
		asset.StorageKind = domain.StorageFile
		asset.MIME = mime.TypeByExtension(filepath.Ext(path))
		if asset.MIME == "" {
			asset.MIME = "application/octet-stream"
		}
		asset.SHA256, err = hashResultFile(path)
	default:
		return AssetOutput{}, fmt.Errorf("unsupported compatibility kind %q", kind)
	}
	return asset, err
}

func ParseResultEnvelope(data []byte, expectedTaskID string, expectedAction domain.TaskAction, outputDir string) (ResultEnvelope, error) {
	return ValidateResultEnvelopeJSON(data, expectedTaskID, expectedAction, outputDir)
}

func ParseResultEnvelopeWithRoots(data []byte, expectedTaskID string, expectedAction domain.TaskAction, outputDir string, roots ManifestRoots) (ResultEnvelope, error) {
	return ValidateResultEnvelopeJSONWithRoots(data, expectedTaskID, expectedAction, outputDir, roots)
}

func ValidateResultEnvelope(envelope ResultEnvelope, expectedTaskID string, expectedAction domain.TaskAction, outputDir string) error {
	return ValidateResultEnvelopeWithRoots(envelope, expectedTaskID, expectedAction, outputDir, ManifestRoots{})
}

func ValidateResultEnvelopeWithRoots(envelope ResultEnvelope, expectedTaskID string, expectedAction domain.TaskAction, outputDir string, roots ManifestRoots) error {
	if envelope.Status == "needs_input" {
		envelope.Status = "awaiting_input"
	}
	if envelope.SchemaVersion != ProtocolSchemaVersion {
		return fmt.Errorf("schema_version must equal %q", ProtocolSchemaVersion)
	}
	if _, err := uuid.Parse(envelope.TaskID); err != nil {
		return fmt.Errorf("task_id must be a UUID")
	}
	if envelope.TaskID != expectedTaskID {
		return fmt.Errorf("task_id mismatch")
	}
	if envelope.Action != expectedAction {
		return fmt.Errorf("action mismatch")
	}
	if _, err := ResolveAction(envelope.Action); err != nil {
		return err
	}
	switch envelope.Status {
	case "completed", "awaiting_input", "failed":
	default:
		return fmt.Errorf("unsupported result status %q", envelope.Status)
	}
	if envelope.Questions == nil || envelope.Artifacts == nil || envelope.AssetOutputs == nil || envelope.Warnings == nil {
		return fmt.Errorf("result array fields must be present")
	}
	if envelope.Status == "awaiting_input" && len(envelope.Questions) == 0 {
		return fmt.Errorf("awaiting_input requires a structured question")
	}
	if envelope.Status != "awaiting_input" && len(envelope.Questions) != 0 {
		return fmt.Errorf("questions are only allowed while awaiting_input")
	}
	if envelope.Status != "completed" && len(envelope.AssetOutputs) != 0 {
		return fmt.Errorf("formal assets are only allowed for completed results")
	}
	for i, question := range envelope.Questions {
		if strings.TrimSpace(question.Text) == "" || len(question.Options) == 0 {
			return fmt.Errorf("question %d is not structured", i)
		}
		for _, option := range question.Options {
			if strings.TrimSpace(option) == "" {
				return fmt.Errorf("question %d contains an empty option", i)
			}
		}
	}
	root, err := resolvePath(outputDir)
	if err != nil {
		return fmt.Errorf("canonicalize output_dir: %w", err)
	}
	artifactPaths := make([]string, 0, len(envelope.Artifacts))
	for i, artifact := range envelope.Artifacts {
		if strings.TrimSpace(artifact.Type) == "" || strings.TrimSpace(artifact.Description) == "" {
			return fmt.Errorf("artifact %d metadata is incomplete", i)
		}
		if !allowedArtifactTypes[envelope.Action][artifact.Type] {
			return fmt.Errorf("artifact %d type %q is not allowed for action %q", i, artifact.Type, envelope.Action)
		}
		artifactRoot := root
		receiptRoot := artifactRoot
		if artifact.Type == "topic_card" {
			artifactRoot, err = requiredDirectoryRoot("topic cards", roots.TopicCards)
			if err == nil {
				vault, vaultErr := requiredDirectoryRoot("Obsidian", roots.Obsidian)
				if vaultErr != nil || !pathInside(vault, artifactRoot) {
					err = fmt.Errorf("topic cards root is not inside trusted Obsidian Vault")
				}
				if vaultErr == nil {
					receiptRoot = vault
				}
			}
			if err != nil {
				return fmt.Errorf("artifact %d: %w", i, err)
			}
		}
		path, err := validateResultPath("artifact", artifact.Path, artifactRoot)
		if err != nil {
			return fmt.Errorf("artifact %d: %w", i, err)
		}
		info, err := os.Lstat(path)
		if err != nil || info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("artifact %d must name a no-follow filesystem entry", i)
		}
		directoryArtifact := envelope.Action == domain.ActionMontageExecute && artifact.Type == "plaintext_workspace"
		if directoryArtifact {
			if !info.IsDir() {
				return fmt.Errorf("artifact %d plaintext_workspace must name a directory", i)
			}
			actual, hashErr := HashResultDirectory(path)
			if hashErr != nil {
				return fmt.Errorf("artifact %d hash plaintext workspace: %w", i, hashErr)
			}
			if artifact.SHA256 != "" && (!sha256Pattern.MatchString(artifact.SHA256) || !strings.EqualFold(actual, artifact.SHA256)) {
				return fmt.Errorf("artifact %d sha256 does not match directory content", i)
			}
		} else if !info.Mode().IsRegular() {
			return fmt.Errorf("artifact %d must name a regular file", i)
		}
		if artifact.Type == "topic_card" {
			rel, err := filepath.Rel(receiptRoot, path)
			if err != nil || filepath.IsAbs(artifact.RelativePath) || filepath.Clean(artifact.RelativePath) != rel || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
				return fmt.Errorf("artifact %d relative_path does not match Vault path", i)
			}
			file, _, _, err := openVerifiedArtifact(path, artifactRoot)
			if err != nil {
				return fmt.Errorf("artifact %d open receipt: %w", i, err)
			}
			actual, hashErr := hashOpenFile(file)
			_ = file.Close()
			if hashErr != nil {
				return fmt.Errorf("artifact %d hash receipt: %w", i, hashErr)
			}
			if !sha256Pattern.MatchString(artifact.SHA256) || !strings.EqualFold(actual, artifact.SHA256) {
				return fmt.Errorf("artifact %d sha256 does not match file content", i)
			}
		} else if artifact.Type == "execution_timings" {
			rel, relErr := filepath.Rel(root, path)
			if relErr != nil || filepath.IsAbs(artifact.RelativePath) || filepath.Clean(filepath.FromSlash(artifact.RelativePath)) != rel || rel == "." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
				return fmt.Errorf("artifact %d execution timing relative_path does not match output path", i)
			}
			file, _, _, openErr := openVerifiedArtifact(path, root)
			if openErr != nil {
				return fmt.Errorf("artifact %d open execution timings: %w", i, openErr)
			}
			actual, hashErr := hashOpenFile(file)
			_ = file.Close()
			if hashErr != nil {
				return fmt.Errorf("artifact %d hash execution timings: %w", i, hashErr)
			}
			if !sha256Pattern.MatchString(artifact.SHA256) || !strings.EqualFold(actual, artifact.SHA256) {
				return fmt.Errorf("artifact %d sha256 does not match execution timings", i)
			}
		} else if artifact.RelativePath != "" || (artifact.SHA256 != "" && !directoryArtifact) {
			return fmt.Errorf("artifact %d receipt fields are only allowed for topic_card or execution_timings", i)
		}
		artifactPaths = append(artifactPaths, path)
	}
	assetPaths := make([]string, 0, len(envelope.AssetOutputs))
	for i, asset := range envelope.AssetOutputs {
		if !formalAssetTypes[asset.Type] {
			return fmt.Errorf("asset output %d has unknown formal asset type %q", i, asset.Type)
		}
		if !allowedAssetTypes[envelope.Action][asset.Type] {
			return fmt.Errorf("asset output %d type %q is not allowed for action %q", i, asset.Type, envelope.Action)
		}
		path, err := validateResultPath("asset output", asset.Path, root)
		if err != nil {
			return fmt.Errorf("asset output %d: %w", i, err)
		}
		for _, artifactPath := range artifactPaths {
			if pathsOverlap(path, artifactPath) {
				return fmt.Errorf("asset output overlaps engineering artifact path")
			}
		}
		for _, previous := range assetPaths {
			if pathsOverlap(path, previous) {
				return fmt.Errorf("asset outputs overlap")
			}
		}
		if err := validateAssetMetadata(asset, path, root); err != nil {
			return fmt.Errorf("asset output %d: %w", i, err)
		}
		assetPaths = append(assetPaths, path)
	}
	return nil
}

func ensureJSONEOF(decoder *json.Decoder) error {
	var extra any
	if err := decoder.Decode(&extra); err == io.EOF {
		return nil
	}
	return fmt.Errorf("result must contain exactly one JSON object")
}

func validateResultPath(label, path, root string) (string, error) {
	path = normalizeWindowsExtendedPath(path)
	if !filepath.IsAbs(path) {
		return "", fmt.Errorf("%s path must be absolute", label)
	}
	resolved, err := resolvePath(path)
	if err != nil {
		return "", fmt.Errorf("canonicalize path: %w", err)
	}
	if !pathInside(root, resolved) {
		return "", fmt.Errorf("path is outside output_dir")
	}
	cleanAbs, err := filepath.Abs(filepath.Clean(path))
	if err != nil || !canonicalSamePath(cleanAbs, resolved) {
		return "", fmt.Errorf("path is not canonical")
	}
	return resolved, nil
}

func canonicalSamePath(a, b string) bool {
	a = normalizeWindowsExtendedPath(a)
	b = normalizeWindowsExtendedPath(b)
	if filepath.Separator == '\\' {
		return strings.EqualFold(a, b)
	}
	return a == b
}

func pathsOverlap(a, b string) bool {
	a = normalizeWindowsExtendedPath(a)
	b = normalizeWindowsExtendedPath(b)
	return canonicalSamePath(a, b) || pathInside(a, b) || pathInside(b, a)
}

func validateAssetMetadata(asset AssetOutput, path, root string) error {
	info, err := os.Stat(path)
	if err != nil {
		return fmt.Errorf("inspect output: %w", err)
	}
	if asset.Filename != filepath.Base(path) {
		return fmt.Errorf("filename does not match path")
	}
	switch asset.StorageKind {
	case domain.StorageFile:
		file, _, openedInfo, err := openVerifiedArtifact(path, root)
		if err != nil || !openedInfo.Mode().IsRegular() {
			return fmt.Errorf("storage_kind=file does not name a regular file")
		}
		defer file.Close()
		if asset.Size != openedInfo.Size() {
			return fmt.Errorf("size does not match file")
		}
		if !sha256Pattern.MatchString(asset.SHA256) {
			return fmt.Errorf("file sha256 must be 64 hexadecimal characters")
		}
		mediaType, _, parseErr := mime.ParseMediaType(asset.MIME)
		if parseErr != nil || !strings.Contains(mediaType, "/") || mediaType == "inode/directory" {
			return fmt.Errorf("file MIME metadata is inconsistent")
		}
		actual, hashErr := hashOpenFile(file)
		if hashErr != nil {
			return fmt.Errorf("hash file output: %w", hashErr)
		}
		if !strings.EqualFold(actual, asset.SHA256) {
			return fmt.Errorf("sha256 does not match file content")
		}
	case domain.StorageDirectory:
		if !info.IsDir() {
			return fmt.Errorf("storage_kind=directory does not name a directory")
		}
		if asset.Size != 0 || !sha256Pattern.MatchString(asset.SHA256) || asset.MIME != "inode/directory" {
			return fmt.Errorf("directory metadata is inconsistent")
		}
		actual, err := HashResultDirectory(path)
		if err != nil {
			return fmt.Errorf("hash directory output: %w", err)
		}
		if !strings.EqualFold(actual, asset.SHA256) {
			return fmt.Errorf("sha256 does not match directory content")
		}
	default:
		return fmt.Errorf("unsupported storage_kind %q", asset.StorageKind)
	}
	return nil
}

var allowedArtifactTypes = map[domain.TaskAction]map[string]bool{
	domain.ActionTopicBrainstorm: {"topic_candidates": true},
	domain.ActionTopicCommit:     {"topic_card": true}, domain.ActionTopicDeepen: {"topic_card": true},
	domain.ActionRemixStandard:  {"viral_analysis": true, "structure_design": true, "publishing_package": true, "self_check": true},
	domain.ActionRemixEnhanced:  {"viral_analysis": true, "structure_design": true, "publishing_package": true, "self_check": true},
	domain.ActionRemixFromTopic: {"viral_analysis": true, "structure_design": true, "publishing_package": true, "self_check": true},
	domain.ActionRemixSpokenLines: {},
	domain.ActionRemixReview:    {"viral_analysis": true, "structure_design": true, "publishing_package": true, "self_check": true},
	domain.ActionMontagePlan:    montagePlanArtifactTypes(), domain.ActionMontageExecute: montageExecuteArtifactTypes(),
}

func montagePlanArtifactTypes() map[string]bool {
	return map[string]bool{"production_plan": true, "production_plan_readable": true, "production_plan_validation": true, "selected_media_summary": true, "qc_report": true, "events": true, "stderr_log": true, "execution_timings": true}
}

func montageExecuteArtifactTypes() map[string]bool {
	types := montagePlanArtifactTypes()
	types["draft_validation"] = true
	types["registration_result"] = true
	types["plaintext_workspace"] = true
	return types
}

var allowedAssetTypes = map[domain.TaskAction]map[domain.AssetType]bool{
	domain.ActionTopicBrainstorm: {}, domain.ActionTopicCommit: {}, domain.ActionTopicDeepen: {},
	domain.ActionRemixStandard:  {domain.AssetContinuousScript: true},
	domain.ActionRemixEnhanced:  {domain.AssetContinuousScript: true},
	domain.ActionRemixFromTopic: {domain.AssetContinuousScript: true},
	domain.ActionRemixSpokenLines: {domain.AssetSpokenScript: true},
	domain.ActionCaptionKeywords:  {domain.AssetCaptionKeywords: true},
	domain.ActionRemixReview:    {domain.AssetContinuousScript: true},
	domain.ActionMontagePlan:    {domain.AssetWordTiming: true}, domain.ActionMontageExecute: {domain.AssetWordTiming: true},
}

type resultDirectoryEntry struct {
	rel    string
	isDir  bool
	size   int64
	digest [sha256.Size]byte
}

// HashResultDirectory returns a stable digest over sorted relative paths,
// entry kinds, file sizes, and file content hashes. Timestamps and OS modes
// are intentionally excluded so identical portable drafts hash identically.
func HashResultDirectory(root string) (string, error) {
	var entries []resultDirectoryEntry
	err := filepath.WalkDir(root, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if path == root {
			return nil
		}
		if entry.Type()&os.ModeSymlink != 0 {
			return fmt.Errorf("directory asset contains symlink %q", path)
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		item := resultDirectoryEntry{rel: filepath.ToSlash(rel), isDir: entry.IsDir()}
		if !entry.IsDir() {
			info, err := entry.Info()
			if err != nil {
				return err
			}
			if !info.Mode().IsRegular() {
				return fmt.Errorf("directory asset contains non-regular entry %q", path)
			}
			item.size = info.Size()
			digest, err := hashResultFile(path)
			if err != nil {
				return err
			}
			decoded, _ := hex.DecodeString(digest)
			copy(item.digest[:], decoded)
		}
		entries = append(entries, item)
		return nil
	})
	if err != nil {
		return "", err
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].rel < entries[j].rel })
	hash := sha256.New()
	var frame [8]byte
	for _, entry := range entries {
		kind := byte('f')
		if entry.isDir {
			kind = 'd'
		}
		_, _ = hash.Write([]byte{kind})
		binary.BigEndian.PutUint64(frame[:], uint64(len(entry.rel)))
		_, _ = hash.Write(frame[:])
		_, _ = hash.Write([]byte(entry.rel))
		binary.BigEndian.PutUint64(frame[:], uint64(entry.size))
		_, _ = hash.Write(frame[:])
		if !entry.isDir {
			_, _ = hash.Write(entry.digest[:])
		}
	}
	return hex.EncodeToString(hash.Sum(nil)), nil
}

func hashResultFile(path string) (string, error) {
	file, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer file.Close()
	return hashOpenFile(file)
}

func hashOpenFile(file *os.File) (string, error) {
	if _, err := file.Seek(0, io.SeekStart); err != nil {
		return "", err
	}
	hash := sha256.New()
	if _, err := io.Copy(hash, file); err != nil {
		return "", err
	}
	return hex.EncodeToString(hash.Sum(nil)), nil
}
