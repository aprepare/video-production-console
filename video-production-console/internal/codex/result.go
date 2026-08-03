package codex

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"

	"video-production-console/internal/domain"
)

type ResultEnvelope struct {
	SchemaVersion string            `json:"schema_version"`
	TaskID        string            `json:"task_id"`
	Action        domain.TaskAction `json:"action"`
	Status        string            `json:"status"`
	Summary       string            `json:"summary"`
	Questions     []Question        `json:"questions"`
	Artifacts     []ArtifactOutput  `json:"artifacts"`
	AssetOutputs  []AssetOutput     `json:"asset_outputs"`
	Warnings      []string          `json:"warnings"`
}

type ArtifactOutput struct {
	Type         string `json:"type"`
	Path         string `json:"path"`
	Description  string `json:"description"`
	RelativePath string `json:"relative_path,omitempty"`
	SHA256       string `json:"sha256,omitempty"`
}

type AssetOutput struct {
	Type          domain.AssetType   `json:"type"`
	Path          string             `json:"path"`
	StorageKind   domain.StorageKind `json:"storage_kind"`
	Filename      string             `json:"filename"`
	MIME          string             `json:"mime"`
	Size          int64              `json:"size"`
	SHA256        string             `json:"sha256"`
	sha256Present bool
}

func (output *AssetOutput) UnmarshalJSON(data []byte) error {
	if err := rejectDuplicateJSONKeys(data); err != nil {
		return err
	}
	type plainAssetOutput AssetOutput
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	var decoded plainAssetOutput
	if err := decoder.Decode(&decoded); err != nil {
		return fmt.Errorf("decode asset output: %w", err)
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(data, &fields); err != nil {
		return fmt.Errorf("inspect asset output fields: %w", err)
	}
	if err := validateExactJSONFields(fields,
		[]string{"type", "path", "storage_kind", "filename", "mime", "size", "sha256"},
		[]string{"type", "path", "storage_kind", "filename", "mime", "size", "sha256"}); err != nil {
		return fmt.Errorf("asset output fields: %w", err)
	}
	for _, field := range []string{"type", "path", "storage_kind", "filename", "mime", "sha256"} {
		if err := requireJSONString(fields[field]); err != nil {
			return fmt.Errorf("asset output field %q: %w", field, err)
		}
	}
	if err := requireJSONInteger(fields["size"]); err != nil {
		return fmt.Errorf("asset output field size: %w", err)
	}
	*output = AssetOutput(decoded)
	_, output.sha256Present = fields["sha256"]
	return nil
}

func rejectDuplicateJSONKeys(data []byte) error {
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	if err := scanStrictJSONValue(decoder); err != nil {
		return err
	}
	if _, err := decoder.Token(); err != io.EOF {
		return fmt.Errorf("JSON contains trailing data")
	}
	return nil
}

func scanStrictJSONValue(decoder *json.Decoder) error {
	token, err := decoder.Token()
	if err != nil {
		return fmt.Errorf("read JSON token: %w", err)
	}
	delim, ok := token.(json.Delim)
	if !ok {
		return nil
	}
	switch delim {
	case '{':
		seen := map[string]bool{}
		for decoder.More() {
			keyToken, err := decoder.Token()
			if err != nil {
				return err
			}
			key, ok := keyToken.(string)
			if !ok {
				return fmt.Errorf("JSON object key is not a string")
			}
			if seen[key] {
				return fmt.Errorf("duplicate JSON key %q", key)
			}
			seen[key] = true
			if err := scanStrictJSONValue(decoder); err != nil {
				return err
			}
		}
		end, err := decoder.Token()
		if err != nil || end != json.Delim('}') {
			return fmt.Errorf("unterminated JSON object")
		}
	case '[':
		for decoder.More() {
			if err := scanStrictJSONValue(decoder); err != nil {
				return err
			}
		}
		end, err := decoder.Token()
		if err != nil || end != json.Delim(']') {
			return fmt.Errorf("unterminated JSON array")
		}
	default:
		return fmt.Errorf("unexpected JSON delimiter %q", delim)
	}
	return nil
}

func requireJSONString(raw json.RawMessage) error {
	if len(raw) == 0 || bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
		return fmt.Errorf("must be a non-null string")
	}
	var value string
	if err := json.Unmarshal(raw, &value); err != nil {
		return fmt.Errorf("must be a string")
	}
	return nil
}

func requireJSONInteger(raw json.RawMessage) error {
	if len(raw) == 0 || bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
		return fmt.Errorf("must be a non-null integer")
	}
	var value int64
	if err := json.Unmarshal(raw, &value); err != nil {
		return fmt.Errorf("must be an integer")
	}
	return nil
}

func requireJSONArray(raw json.RawMessage) error {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 || trimmed[0] != '[' {
		return fmt.Errorf("must be a non-null array")
	}
	return nil
}

func validateExactJSONFields(fields map[string]json.RawMessage, allowed, required []string) error {
	allowedSet := make(map[string]bool, len(allowed))
	for _, field := range allowed {
		allowedSet[field] = true
	}
	for field := range fields {
		if !allowedSet[field] {
			return fmt.Errorf("unknown field %q", field)
		}
	}
	for _, field := range required {
		if _, ok := fields[field]; !ok {
			return fmt.Errorf("missing required field %q", field)
		}
	}
	return nil
}
