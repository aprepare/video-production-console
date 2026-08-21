package imagevideo

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// ResolveManagedPath resolves a job artifact below DataRoot/image-projects/projectID/jobs.
// The returned relative path is normalized with forward slashes.
func ResolveManagedPath(dataRoot, projectID, candidate string) (string, string, error) {
	if strings.TrimSpace(dataRoot) == "" || strings.TrimSpace(projectID) == "" || strings.TrimSpace(candidate) == "" {
		return "", "", fmt.Errorf("managed path components must not be empty")
	}
	if projectID == "." || projectID == ".." || strings.ContainsAny(projectID, `/\\:`) {
		return "", "", fmt.Errorf("invalid project id")
	}
	normalizedCandidate := strings.ReplaceAll(candidate, `\`, "/")
	if filepath.IsAbs(candidate) || filepath.VolumeName(candidate) != "" || strings.HasPrefix(normalizedCandidate, "/") || strings.Contains(normalizedCandidate, ":") {
		return "", "", fmt.Errorf("managed path must be relative")
	}
	for _, segment := range strings.Split(normalizedCandidate, "/") {
		if segment == ".." {
			return "", "", fmt.Errorf("managed path must not contain parent traversal")
		}
	}
	root, err := filepath.Abs(dataRoot)
	if err != nil {
		return "", "", err
	}
	base := filepath.Join(root, "image-projects", projectID, "jobs")
	full := filepath.Clean(filepath.Join(base, filepath.FromSlash(normalizedCandidate)))
	rel, err := filepath.Rel(base, full)
	if err != nil || rel == "." || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", "", fmt.Errorf("managed path escapes project jobs directory")
	}

	realRoot, err := resolveExistingPath(root)
	if err != nil {
		return "", "", err
	}
	realBase, err := resolveExistingPath(base)
	if err != nil {
		return "", "", err
	}
	if _, ok := relativeWithin(realRoot, realBase, true); !ok {
		return "", "", fmt.Errorf("managed project jobs directory escapes data root")
	}
	realFull, err := resolveExistingPath(full)
	if err != nil {
		return "", "", err
	}
	realRel, ok := relativeWithin(realBase, realFull, false)
	if !ok {
		return "", "", fmt.Errorf("managed symlink escapes project jobs directory")
	}
	return filepath.ToSlash(realRel), realFull, nil
}

// ResolveProjectPath resolves an input image kept below the project root. It
// is deliberately separate from ResolveManagedPath so generated job outputs
// cannot be mistaken for user-provided source assets.
func ResolveProjectPath(dataRoot, projectID, candidate string) (string, string, error) {
	if strings.TrimSpace(dataRoot) == "" || strings.TrimSpace(projectID) == "" || strings.TrimSpace(candidate) == "" {
		return "", "", fmt.Errorf("project path components must not be empty")
	}
	if projectID == "." || projectID == ".." || strings.ContainsAny(projectID, `/\\:`) {
		return "", "", fmt.Errorf("invalid project id")
	}
	normalized := strings.ReplaceAll(candidate, `\`, "/")
	if filepath.IsAbs(candidate) || filepath.VolumeName(candidate) != "" || strings.HasPrefix(normalized, "/") || strings.Contains(normalized, ":") {
		return "", "", fmt.Errorf("project path must be relative")
	}
	for _, segment := range strings.Split(normalized, "/") {
		if segment == ".." {
			return "", "", fmt.Errorf("project path must not contain parent traversal")
		}
	}
	root, err := filepath.Abs(dataRoot)
	if err != nil {
		return "", "", err
	}
	base := filepath.Join(root, "image-projects", projectID)
	full := filepath.Clean(filepath.Join(base, filepath.FromSlash(normalized)))
	rel, err := filepath.Rel(base, full)
	if err != nil || rel == "." || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", "", fmt.Errorf("project path escapes project directory")
	}
	realRoot, err := resolveExistingPath(root)
	if err != nil {
		return "", "", err
	}
	realBase, err := resolveExistingPath(base)
	if err != nil {
		return "", "", err
	}
	if _, ok := relativeWithin(realRoot, realBase, true); !ok {
		return "", "", fmt.Errorf("project directory escapes data root")
	}
	realFull, err := resolveExistingPath(full)
	if err != nil {
		return "", "", err
	}
	realRel, ok := relativeWithin(realBase, realFull, false)
	if !ok {
		return "", "", fmt.Errorf("project symlink escapes project directory")
	}
	return filepath.ToSlash(realRel), realFull, nil
}

// resolveExistingPath resolves every symlink in the longest existing prefix,
// then appends the not-yet-created suffix. This catches a symlink in any parent
// directory even when the final artifact does not exist yet.
func resolveExistingPath(path string) (string, error) {
	current := filepath.Clean(path)
	missing := make([]string, 0, 4)
	for {
		_, err := os.Lstat(current)
		if err == nil {
			real, evalErr := filepath.EvalSymlinks(current)
			if evalErr != nil {
				return "", evalErr
			}
			for i := len(missing) - 1; i >= 0; i-- {
				real = filepath.Join(real, missing[i])
			}
			return filepath.Clean(real), nil
		}
		if !errors.Is(err, os.ErrNotExist) {
			return "", err
		}
		parent := filepath.Dir(current)
		if parent == current {
			return "", err
		}
		missing = append(missing, filepath.Base(current))
		current = parent
	}
}

func relativeWithin(root, candidate string, allowRoot bool) (string, bool) {
	rel, err := filepath.Rel(root, candidate)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", false
	}
	if rel == "." && !allowRoot {
		return "", false
	}
	return rel, true
}
