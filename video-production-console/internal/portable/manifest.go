package portable

import (
	"fmt"
	"strings"
	"unicode"
)

const SchemaVersion = 1

type Entry struct {
	Path   string `json:"path"`
	Size   int64  `json:"size"`
	SHA256 string `json:"sha256"`
}

type Manifest struct {
	SchemaVersion int     `json:"schema_version"`
	AppVersion    string  `json:"app_version"`
	PayloadSHA256 string  `json:"payload_sha256"`
	Entries       []Entry `json:"entries"`
}

func (m Manifest) Validate() error {
	if m.SchemaVersion != SchemaVersion {
		return fmt.Errorf("%w: schema_version %d", ErrInvalidManifest, m.SchemaVersion)
	}
	if !ValidSemver(m.AppVersion) {
		return fmt.Errorf("%w: app_version %q", ErrInvalidManifest, m.AppVersion)
	}
	if m.PayloadSHA256 != "" && !validSHA256(m.PayloadSHA256) {
		return fmt.Errorf("%w: payload_sha256", ErrInvalidManifest)
	}
	if len(m.Entries) == 0 {
		return fmt.Errorf("%w: empty entries", ErrInvalidManifest)
	}
	seen := make(map[string]struct{}, len(m.Entries))
	var prev string
	for i, entry := range m.Entries {
		if err := validateRelPath(entry.Path); err != nil {
			return err
		}
		if entry.Size < 0 {
			return fmt.Errorf("%w: negative size for %q", ErrInvalidManifest, entry.Path)
		}
		if !validSHA256(entry.SHA256) {
			return fmt.Errorf("%w: sha256 for %q", ErrInvalidManifest, entry.Path)
		}
		key := strings.ToLower(entry.Path)
		if _, ok := seen[key]; ok {
			return fmt.Errorf("%w: %q", ErrDuplicateEntry, entry.Path)
		}
		seen[key] = struct{}{}
		if i > 0 && entry.Path <= prev {
			return fmt.Errorf("%w: entries must be unique and sorted", ErrInvalidManifest)
		}
		prev = entry.Path
	}
	return nil
}

func ValidSemver(version string) bool {
	parts := strings.Split(version, ".")
	if len(parts) != 3 {
		return false
	}
	for _, part := range parts {
		if part == "" || (len(part) > 1 && part[0] == '0') {
			return false
		}
		for _, r := range part {
			if !unicode.IsDigit(r) {
				return false
			}
		}
	}
	return true
}

func validSHA256(value string) bool {
	if len(value) != 64 {
		return false
	}
	for _, r := range value {
		if (r >= '0' && r <= '9') || (r >= 'a' && r <= 'f') {
			continue
		}
		return false
	}
	return true
}

func validateRelPath(path string) error {
	if path == "" || strings.IndexByte(path, 0) >= 0 {
		return fmt.Errorf("%w: empty or NUL path", ErrUnsafePath)
	}
	if strings.Contains(path, `\`) {
		return fmt.Errorf("%w: backslash in %q", ErrUnsafePath, path)
	}
	if strings.HasPrefix(path, "/") || strings.HasPrefix(path, "~") {
		return fmt.Errorf("%w: absolute path %q", ErrUnsafePath, path)
	}
	if len(path) >= 2 && path[1] == ':' {
		return fmt.Errorf("%w: drive letter %q", ErrUnsafePath, path)
	}
	if strings.HasSuffix(path, "/") {
		return fmt.Errorf("%w: trailing slash %q", ErrUnsafePath, path)
	}
	for _, segment := range strings.Split(path, "/") {
		if segment == "" || segment == "." || segment == ".." {
			return fmt.Errorf("%w: empty or dotted segment in %q", ErrUnsafePath, path)
		}
	}
	return nil
}
