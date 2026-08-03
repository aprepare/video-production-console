package codex

import (
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
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
	domain.AssetSpokenScript: true, domain.AssetNarration: true, domain.AssetSubtitleSRT: true,
	domain.AssetAccountBackground: true, domain.AssetMixDraft: true, domain.AssetFinalVideo: true,
}

func ValidateResultEnvelopeJSON(data []byte, expectedTaskID string, expectedAction domain.TaskAction, outputDir string) (ResultEnvelope, error) {
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
	if err := validateNestedResultFields(fields); err != nil {
		return ResultEnvelope{}, err
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
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
	if err := ValidateResultEnvelope(envelope, expectedTaskID, expectedAction, outputDir); err != nil {
		return ResultEnvelope{}, err
	}
	return envelope, nil
}

func validateNestedResultFields(fields map[string]json.RawMessage) error {
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
	var questions []map[string]json.RawMessage
	if err := json.Unmarshal(fields["questions"], &questions); err != nil {
		return fmt.Errorf("inspect question fields: %w", err)
	}
	for i, question := range questions {
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
	}
	var artifacts []map[string]json.RawMessage
	if err := json.Unmarshal(fields["artifacts"], &artifacts); err != nil {
		return fmt.Errorf("inspect artifact fields: %w", err)
	}
	for i, artifact := range artifacts {
		if err := validateExactJSONFields(artifact, []string{"type", "path", "description"}, []string{"type", "path", "description"}); err != nil {
			return fmt.Errorf("artifact %d fields: %w", i, err)
		}
		for _, field := range []string{"type", "path", "description"} {
			if err := requireJSONString(artifact[field]); err != nil {
				return fmt.Errorf("artifact %d field %q: %w", i, field, err)
			}
		}
	}
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

func ParseResultEnvelope(data []byte, expectedTaskID string, expectedAction domain.TaskAction, outputDir string) (ResultEnvelope, error) {
	return ValidateResultEnvelopeJSON(data, expectedTaskID, expectedAction, outputDir)
}

func ValidateResultEnvelope(envelope ResultEnvelope, expectedTaskID string, expectedAction domain.TaskAction, outputDir string) error {
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
		path, err := validateResultPath("artifact", artifact.Path, root)
		if err != nil {
			return fmt.Errorf("artifact %d: %w", i, err)
		}
		artifactPaths = append(artifactPaths, path)
	}
	assetPaths := make([]string, 0, len(envelope.AssetOutputs))
	for i, asset := range envelope.AssetOutputs {
		if !formalAssetTypes[asset.Type] {
			return fmt.Errorf("asset output %d has unknown formal asset type %q", i, asset.Type)
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
		if err := validateAssetMetadata(asset, path); err != nil {
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
	if filepath.Separator == '\\' {
		return strings.EqualFold(a, b)
	}
	return a == b
}

func pathsOverlap(a, b string) bool {
	return canonicalSamePath(a, b) || pathInside(a, b) || pathInside(b, a)
}

func validateAssetMetadata(asset AssetOutput, path string) error {
	info, err := os.Stat(path)
	if err != nil {
		return fmt.Errorf("inspect output: %w", err)
	}
	if asset.Filename != filepath.Base(path) {
		return fmt.Errorf("filename does not match path")
	}
	switch asset.StorageKind {
	case domain.StorageFile:
		if !info.Mode().IsRegular() {
			return fmt.Errorf("storage_kind=file does not name a regular file")
		}
		if asset.Size != info.Size() {
			return fmt.Errorf("size does not match file")
		}
		if !sha256Pattern.MatchString(asset.SHA256) {
			return fmt.Errorf("file sha256 must be 64 hexadecimal characters")
		}
		if strings.TrimSpace(asset.MIME) == "" || asset.MIME == "inode/directory" {
			return fmt.Errorf("file MIME metadata is inconsistent")
		}
		actual, err := hashResultFile(path)
		if err != nil {
			return fmt.Errorf("hash file output: %w", err)
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
	hash := sha256.New()
	if _, err := io.Copy(hash, file); err != nil {
		return "", err
	}
	return hex.EncodeToString(hash.Sum(nil)), nil
}
