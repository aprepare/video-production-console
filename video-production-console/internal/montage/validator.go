package montage

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

type ValidationRequest struct{ TaskID, WorkspacePath, ReceiptPath, JianyingRoot string }

type registrationReceipt struct {
	Status                  string `json:"status"`
	RegisteredPath          string `json:"registered_path"`
	DraftID                 string `json:"draft_id"`
	SourceDraftID           string `json:"source_draft_id"`
	DraftIDRekeyed          *bool  `json:"draft_id_rekeyed"`
	DurationUS              int64  `json:"duration_us"`
	SourceContentSHA256     string `json:"source_content_sha256"`
	RegisteredContentSHA256 string `json:"registered_content_sha256"`
}

func ValidateRegisteredDraft(request ValidationRequest) (RegisterResult, error) {
	workspace, err := canonicalDirectory(request.WorkspacePath)
	if err != nil {
		return RegisterResult{}, invalidRegistration("workspace is unavailable")
	}
	root, err := canonicalDirectory(request.JianyingRoot)
	if err != nil {
		return RegisterResult{}, invalidRegistration("Jianying root is unavailable")
	}
	receiptPath, err := canonicalRegularFile(request.ReceiptPath)
	if err != nil {
		return RegisterResult{}, invalidRegistration("registration receipt is unavailable")
	}
	outputRoot := filepath.Dir(filepath.Dir(workspace))
	if !WithinJianyingRoot(outputRoot, receiptPath) {
		return RegisterResult{}, invalidRegistration("registration receipt is outside task output")
	}
	receiptBytes, err := readBounded(receiptPath, 1<<20)
	if err != nil {
		return RegisterResult{}, invalidRegistration("registration receipt could not be read")
	}
	var receipt registrationReceipt
	if json.Unmarshal(bytes.TrimPrefix(receiptBytes, []byte{0xef, 0xbb, 0xbf}), &receipt) != nil || receipt.Status != "completed" || receipt.DraftID == "" || receipt.DurationUS <= 0 {
		return RegisterResult{}, invalidRegistration("registration receipt fields are invalid")
	}
	registered, err := canonicalDirectory(receipt.RegisteredPath)
	if err != nil {
		return RegisterResult{}, invalidRegistration("registered draft is unavailable")
	}
	expected := filepath.Join(root, request.TaskID)
	expected, err = canonicalNoFollow(expected, true)
	if err != nil || !samePath(expected, registered) || samePath(workspace, registered) || !WithinJianyingRoot(root, registered) {
		return RegisterResult{}, invalidRegistration("registered draft path is not authoritative")
	}
	sourceContent, err := canonicalRegularFile(filepath.Join(workspace, "draft_content.json"))
	if err != nil {
		return RegisterResult{}, invalidRegistration("source draft content is missing")
	}
	registeredContent, err := canonicalRegularFile(filepath.Join(registered, "draft_content.json"))
	if err != nil {
		return RegisterResult{}, invalidRegistration("registered draft content is missing")
	}
	sourceHash, err := hashFile(sourceContent)
	if err != nil {
		return RegisterResult{}, err
	}
	registeredHash, err := hashFile(registeredContent)
	if err != nil {
		return RegisterResult{}, err
	}
	if !validHash(receipt.SourceContentSHA256) || !validHash(receipt.RegisteredContentSHA256) || !strings.EqualFold(sourceHash, receipt.SourceContentSHA256) || !strings.EqualFold(registeredHash, receipt.RegisteredContentSHA256) || !strings.EqualFold(sourceHash, registeredHash) {
		return RegisterResult{}, invalidRegistration("registered content fingerprint does not match source")
	}
	sourceDraftID, err := readDraftID(filepath.Join(workspace, "draft_meta_info.json"))
	if err != nil {
		return RegisterResult{}, invalidRegistration("source draft ID is unavailable")
	}
	registeredDraftID, err := readDraftID(filepath.Join(registered, "draft_meta_info.json"))
	if err != nil || registeredDraftID != receipt.DraftID {
		return RegisterResult{}, invalidRegistration("registered draft ID does not match registration receipt")
	}
	hasSourceDraftID := strings.TrimSpace(receipt.SourceDraftID) != ""
	hasRekeyFlag := receipt.DraftIDRekeyed != nil
	if hasSourceDraftID != hasRekeyFlag {
		return RegisterResult{}, invalidRegistration("draft ID rekey receipt fields are incomplete")
	}
	if hasSourceDraftID {
		if receipt.SourceDraftID != sourceDraftID {
			return RegisterResult{}, invalidRegistration("source draft ID does not match registration receipt")
		}
		actuallyRekeyed := sourceDraftID != registeredDraftID
		if *receipt.DraftIDRekeyed != actuallyRekeyed {
			return RegisterResult{}, invalidRegistration("draft ID rekey flag does not match registered draft")
		}
	}
	if err := validateRootIndex(filepath.Join(root, "root_meta_info.json"), receipt.DraftID, registered); err != nil {
		return RegisterResult{}, err
	}
	directoryHash, err := hashDirectory(registered)
	if err != nil {
		return RegisterResult{}, invalidRegistration("registered directory could not be hashed")
	}
	return RegisterResult{RegisteredPath: registered, ReceiptPath: receiptPath, DraftID: receipt.DraftID, SourceContentSHA256: sourceHash, RegisteredContentSHA256: registeredHash, DirectorySHA256: directoryHash, DurationUS: receipt.DurationUS}, nil
}

func readDraftID(path string) (string, error) {
	data, err := readBounded(path, 4<<20)
	if err != nil {
		return "", err
	}
	var meta struct {
		DraftID string `json:"draft_id"`
	}
	if err := json.Unmarshal(bytes.TrimPrefix(data, []byte{0xef, 0xbb, 0xbf}), &meta); err != nil || strings.TrimSpace(meta.DraftID) == "" {
		return "", errors.New("draft_meta_info.json has no draft_id")
	}
	return meta.DraftID, nil
}

func validateRootIndex(path, draftID, registered string) error {
	data, err := readBounded(path, 16<<20)
	if err != nil {
		return invalidRegistration("Jianying root index is unavailable")
	}
	var raw struct {
		Entries []map[string]any `json:"all_draft_store"`
	}
	if json.Unmarshal(bytes.TrimPrefix(data, []byte{0xef, 0xbb, 0xbf}), &raw) != nil {
		return invalidRegistration("Jianying root index is invalid")
	}
	for _, entry := range raw.Entries {
		id, _ := entry["draft_id"].(string)
		pathValue, _ := entry["draft_fold_path"].(string)
		// Jianying may persist draft paths with the Windows extended-length
		// prefix while the validator has already canonicalized the target.
		// Normalize both representations before comparing identities.
		pathValue = stripWindowsExtendedPath(strings.TrimSpace(pathValue))
		if id == draftID && samePath(filepath.Clean(pathValue), registered) {
			return nil
		}
	}
	return invalidRegistration("registered draft is absent from Jianying root index")
}

func canonicalDirectory(path string) (string, error) {
	return canonicalNoFollow(path, true)
}
func canonicalRegularFile(path string) (string, error) {
	return canonicalNoFollow(path, false)
}
func readBounded(path string, limit int64) ([]byte, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	data, err := io.ReadAll(io.LimitReader(file, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > limit {
		return nil, fmt.Errorf("file exceeds %d-byte limit", limit)
	}
	return data, nil
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
func hashDirectory(root string) (string, error) {
	var files []string
	err := filepath.WalkDir(root, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.Type()&os.ModeSymlink != 0 {
			return invalidRegistration("registered draft contains a symlink")
		}
		if !entry.IsDir() {
			files = append(files, path)
		}
		return nil
	})
	if err != nil {
		return "", err
	}
	sort.Strings(files)
	hash := sha256.New()
	for _, path := range files {
		rel, _ := filepath.Rel(root, path)
		fileHash, err := hashFile(path)
		if err != nil {
			return "", err
		}
		_, _ = fmt.Fprintf(hash, "%s\x00%s\n", filepath.ToSlash(rel), fileHash)
	}
	return hex.EncodeToString(hash.Sum(nil)), nil
}
func validHash(value string) bool {
	_, err := hex.DecodeString(value)
	return len(value) == 64 && err == nil
}
func samePath(left, right string) bool {
	if filepath.Separator == '\\' {
		return strings.EqualFold(filepath.Clean(left), filepath.Clean(right))
	}
	return filepath.Clean(left) == filepath.Clean(right)
}
func invalidRegistration(message string) error {
	return fmt.Errorf("%w: %s", ErrInvalidRegistration, message)
}
