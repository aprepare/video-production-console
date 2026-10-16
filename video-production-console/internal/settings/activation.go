package settings

import (
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"video-production-console/internal/security"
)

const (
	activationPrefix          = "JYC1."
	maxActivationCodeSize     = 64 << 10
	maxActivationPayloadSize  = 256 << 10
	activationVersion         = 1
	activationCodeFileName    = "activation.jyc"
	activationAppliedFileName = "activation.applied"
	maxExportExpiryDays       = 3652500
)

var ErrInvalidActivationCode = errors.New("配置码无效")
var ErrInvalidExportRequest = errors.New("导出请求无效")

type ActivationResult struct {
	AppliedPublic   []string `json:"applied_public"`
	AppliedSecrets  []string `json:"applied_secrets"`
	Skipped         []string `json:"skipped"`
	IssuedTo        string   `json:"issued_to"`
	ExpiresAt       string   `json:"expires_at"`
	RestartRequired bool     `json:"restart_required"`
}

type ExportCodeRequest struct {
	IssuedTo       string   `json:"issued_to"`
	ExpiresDays    int      `json:"expires_days"`
	IncludeSecrets bool     `json:"include_secrets"`
	Keys           []string `json:"keys"`
}

type ExportCodeResult struct {
	Code       string   `json:"code"`
	PublicKeys []string `json:"public_keys"`
	SecretKeys []string `json:"secret_keys"`
}

type activationPayload struct {
	Version   int               `json:"v"`
	IssuedTo  string            `json:"issued_to"`
	IssuedAt  string            `json:"issued_at"`
	ExpiresAt string            `json:"expires_at"`
	Note      string            `json:"note"`
	Public    map[string]string `json:"public"`
	Secrets   map[string]string `json:"secrets"`
}

type invalidActivationError struct{ message string }

func (err invalidActivationError) Error() string { return err.message }
func (err invalidActivationError) Unwrap() error { return ErrInvalidActivationCode }

type invalidExportError struct{ message string }

func (err invalidExportError) Error() string { return err.message }
func (err invalidExportError) Unwrap() error { return ErrInvalidExportRequest }

var activationPublicKeys = map[string]struct{}{
	"remix_base_url": {}, "remix_model": {}, "remix_reasoning_effort": {},
	"remix_check_model": {}, "spoken_lines_model": {}, "model_options": {},
	"grok_base_url": {}, "grok_model": {}, "copy_base_url": {},
	"image_base_url": {}, "image_model": {}, "image_use_text_key": {}, "image_text_base_url": {},
	"image_text_model": {}, "image_text_reasoning_effort": {},
	"vision_base_url": {}, "vision_model": {}, "embedding_base_url": {},
	"embedding_model": {}, "platform_api_base_url": {}, "tts_provider": {},
	"aurastd_base_url": {}, "aurastd_model": {}, "aurastd_voice_id": {},
	"volc_speech_speaker_id": {}, "volc_speech_resource_id": {},
	"pexels_api_base_url": {}, "pixabay_api_base_url": {},
	"analysis_base_url": {}, "analysis_model": {},
}

const reservedAnalysisSecretKey = "analysis_api_key"

func (s *Service) ActivateCode(ctx context.Context, code string) (ActivationResult, error) {
	payload, err := decodeActivationCode(code, s.now().UTC())
	if err != nil {
		return ActivationResult{}, err
	}

	rejected := rejectedActivationKeys(payload)
	if len(rejected) != 0 {
		return ActivationResult{}, invalidActivationError{message: "配置码包含不允许的键：" + strings.Join(rejected, "、")}
	}

	view, err := s.Get(ctx)
	if err != nil {
		return ActivationResult{}, errors.New("读取当前设置失败")
	}
	applicablePublic := make(map[string]string, len(payload.Public))
	applicableSecrets := make(map[string]string, len(payload.Secrets))
	result := ActivationResult{
		AppliedPublic:  []string{},
		AppliedSecrets: []string{},
		Skipped:        []string{},
		IssuedTo:       payload.IssuedTo,
		ExpiresAt:      payload.ExpiresAt,
	}
	for key, value := range payload.Public {
		if _, known := publicJSONKeys[key]; !known {
			result.Skipped = append(result.Skipped, key)
			continue
		}
		applicablePublic[key] = value
		result.AppliedPublic = append(result.AppliedPublic, key)
	}
	for key, value := range payload.Secrets {
		if !containsActivationSecretKey(key) {
			result.Skipped = append(result.Skipped, key)
			continue
		}
		if value == "" {
			result.Skipped = append(result.Skipped, key)
			continue
		}
		applicableSecrets[key] = value
		result.AppliedSecrets = append(result.AppliedSecrets, key)
	}
	sort.Strings(result.AppliedPublic)
	sort.Strings(result.AppliedSecrets)
	sort.Strings(result.Skipped)

	rawPublic, err := marshalActivationPublic(applicablePublic)
	if err != nil {
		return ActivationResult{}, invalidActivationError{message: "配置码设置内容无效"}
	}
	merged, err := OverlayPublic(view.Public, rawPublic)
	if err != nil {
		return ActivationResult{}, invalidActivationError{message: "配置码设置内容无效"}
	}
	updated, err := s.Update(ctx, merged, applicableSecrets)
	if err != nil {
		if errors.Is(err, ErrInvalidSettings) || errors.Is(err, ErrUnknownSecret) || errors.Is(err, security.ErrSecretTooLarge) {
			return ActivationResult{}, invalidActivationError{message: "配置码设置内容无效"}
		}
		return ActivationResult{}, errors.New("配置码应用失败")
	}
	result.RestartRequired = updated.RestartRequired
	return result, nil
}

func marshalActivationPublic(values map[string]string) ([]byte, error) {
	encoded := make(map[string]json.RawMessage, len(values))
	for key, value := range values {
		if key == "image_use_text_key" {
			parsed, err := strconv.ParseBool(value)
			if err != nil {
				return nil, err
			}
			encoded[key] = json.RawMessage(strconv.FormatBool(parsed))
			continue
		}
		raw, err := json.Marshal(value)
		if err != nil {
			return nil, err
		}
		encoded[key] = raw
	}
	return json.Marshal(encoded)
}

func (s *Service) ExportCode(ctx context.Context, input ExportCodeRequest) (ExportCodeResult, error) {
	if input.ExpiresDays < 0 {
		return ExportCodeResult{}, invalidExportError{message: "有效天数不能小于零"}
	}
	if input.ExpiresDays > maxExportExpiryDays {
		return ExportCodeResult{}, invalidExportError{message: "有效天数超出支持范围"}
	}
	selected, err := selectedActivationKeys(input.Keys)
	if err != nil {
		return ExportCodeResult{}, err
	}
	values, _, err := s.repo.Public(ctx)
	if err != nil {
		return ExportCodeResult{}, errors.New("读取当前设置失败")
	}
	now := s.now().UTC().Truncate(time.Second)
	payload := activationPayload{
		Version:  activationVersion,
		IssuedTo: strings.TrimSpace(input.IssuedTo),
		IssuedAt: now.Format(time.RFC3339),
		Public:   map[string]string{},
		Secrets:  map[string]string{},
	}
	if input.ExpiresDays > 0 {
		expiresAt := now.AddDate(0, 0, input.ExpiresDays)
		formatted := expiresAt.Format(time.RFC3339)
		parsed, parseErr := time.Parse(time.RFC3339, formatted)
		if parseErr != nil || !expiresAt.After(now) || !parsed.Equal(expiresAt) {
			return ExportCodeResult{}, invalidExportError{message: "有效天数超出支持范围"}
		}
		payload.ExpiresAt = formatted
	}
	result := ExportCodeResult{PublicKeys: []string{}, SecretKeys: []string{}}
	for key := range activationPublicKeys {
		if _, known := publicJSONKeys[key]; !known || !keySelected(selected, key) {
			continue
		}
		if value := values[key]; strings.TrimSpace(value) != "" {
			payload.Public[key] = value
			result.PublicKeys = append(result.PublicKeys, key)
		}
	}
	if input.IncludeSecrets {
		for _, key := range secretKeys {
			if !keySelected(selected, key) {
				continue
			}
			value, _, configured, readErr := s.secretValue(ctx, key)
			if readErr != nil {
				return ExportCodeResult{}, errors.New("读取已保存密钥失败")
			}
			if configured && strings.TrimSpace(value) != "" {
				payload.Secrets[key] = value
				result.SecretKeys = append(result.SecretKeys, key)
			}
		}
	}
	sort.Strings(result.PublicKeys)
	sort.Strings(result.SecretKeys)
	result.Code, err = encodeActivationCode(payload)
	if err != nil {
		return ExportCodeResult{}, err
	}
	return result, nil
}

func (s *Service) AutoActivate(ctx context.Context, dataRoot string) (bool, error) {
	if strings.TrimSpace(dataRoot) == "" {
		return false, errors.New("自动激活目录无效")
	}
	provisionDir := filepath.Join(dataRoot, "provision")
	activationPath := filepath.Join(provisionDir, activationCodeFileName)
	raw, err := readFileLimited(activationPath, maxActivationCodeSize)
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, errors.New("读取自动激活文件失败")
	}
	digest := sha256.Sum256(raw)
	digestText := hex.EncodeToString(digest[:])
	appliedPath := filepath.Join(provisionDir, activationAppliedFileName)
	previous, readErr := readFileLimited(appliedPath, 1024)
	if readErr == nil && strings.TrimSpace(string(previous)) == digestText {
		return false, nil
	}
	if readErr != nil && !errors.Is(readErr, os.ErrNotExist) {
		return false, errors.New("读取自动激活状态失败")
	}
	if _, err := s.ActivateCode(ctx, strings.TrimSpace(string(raw))); err != nil {
		return false, err
	}
	if err := writeAppliedDigestAtomic(appliedPath, []byte(digestText+"\n")); err != nil {
		return false, errors.New("保存自动激活状态失败")
	}
	return true, nil
}

func decodeActivationCode(code string, now time.Time) (activationPayload, error) {
	if len(code) > maxActivationCodeSize || !strings.HasPrefix(code, activationPrefix) {
		return activationPayload{}, invalidActivationError{message: "配置码格式无效"}
	}
	encoded := strings.TrimPrefix(code, activationPrefix)
	compressed, err := base64.RawURLEncoding.DecodeString(encoded)
	if err != nil || len(compressed) == 0 {
		return activationPayload{}, invalidActivationError{message: "配置码编码格式无效"}
	}
	reader, err := gzip.NewReader(bytes.NewReader(compressed))
	if err != nil {
		clear(compressed)
		return activationPayload{}, invalidActivationError{message: "配置码压缩格式无效"}
	}
	raw, readErr := io.ReadAll(io.LimitReader(reader, maxActivationPayloadSize+1))
	closeErr := reader.Close()
	clear(compressed)
	if readErr != nil || closeErr != nil || len(raw) > maxActivationPayloadSize {
		clear(raw)
		return activationPayload{}, invalidActivationError{message: "配置码内容过大或损坏"}
	}
	defer clear(raw)
	var payload activationPayload
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&payload); err != nil {
		return activationPayload{}, invalidActivationError{message: "配置码内容格式无效"}
	}
	if err := ensureJSONEnd(decoder); err != nil {
		return activationPayload{}, invalidActivationError{message: "配置码内容格式无效"}
	}
	if payload.Version != activationVersion {
		return activationPayload{}, invalidActivationError{message: "配置码版本不受支持"}
	}
	if _, err := time.Parse(time.RFC3339, payload.IssuedAt); err != nil {
		return activationPayload{}, invalidActivationError{message: "配置码签发时间无效"}
	}
	if payload.ExpiresAt != "" {
		expiresAt, err := time.Parse(time.RFC3339, payload.ExpiresAt)
		if err != nil {
			return activationPayload{}, invalidActivationError{message: "配置码有效期无效"}
		}
		if !now.Before(expiresAt) {
			return activationPayload{}, invalidActivationError{message: "配置码已过期，请联系服务商"}
		}
	}
	return payload, nil
}

func encodeActivationCode(payload activationPayload) (string, error) {
	raw, err := json.Marshal(payload)
	if err != nil {
		return "", errors.New("生成配置码失败")
	}
	defer clear(raw)
	if len(raw) > maxActivationPayloadSize {
		return "", errors.New("可导出的设置过多")
	}
	var compressed bytes.Buffer
	writer := gzip.NewWriter(&compressed)
	if _, err := writer.Write(raw); err != nil {
		return "", errors.New("生成配置码失败")
	}
	if err := writer.Close(); err != nil {
		return "", errors.New("生成配置码失败")
	}
	code := activationPrefix + base64.RawURLEncoding.EncodeToString(compressed.Bytes())
	if len(code) > maxActivationCodeSize {
		return "", errors.New("可导出的设置过多")
	}
	return code, nil
}

func rejectedActivationKeys(payload activationPayload) []string {
	rejected := make([]string, 0)
	for key := range payload.Public {
		if _, allowed := activationPublicKeys[key]; !allowed {
			rejected = append(rejected, key)
		}
	}
	for key := range payload.Secrets {
		if key != reservedAnalysisSecretKey && !containsActivationSecretKey(key) {
			rejected = append(rejected, key)
		}
	}
	return safeRejectedKeyNames(rejected)
}

func containsActivationSecretKey(key string) bool {
	for _, allowed := range secretKeys {
		if key == allowed {
			return true
		}
	}
	return false
}

func selectedActivationKeys(keys []string) (map[string]struct{}, error) {
	if len(keys) == 0 {
		return nil, nil
	}
	selected := make(map[string]struct{}, len(keys))
	rejected := make([]string, 0)
	for _, key := range keys {
		key = strings.TrimSpace(key)
		if key == "" {
			rejected = append(rejected, key)
			continue
		}
		_, publicAllowed := activationPublicKeys[key]
		secretAllowed := key == reservedAnalysisSecretKey || containsActivationSecretKey(key)
		if !publicAllowed && !secretAllowed {
			rejected = append(rejected, key)
			continue
		}
		selected[key] = struct{}{}
	}
	if len(rejected) != 0 {
		rejected = safeRejectedKeyNames(rejected)
		return nil, invalidExportError{message: fmt.Sprintf("导出请求包含不允许的键：%s", strings.Join(rejected, "、"))}
	}
	return selected, nil
}

func safeRejectedKeyNames(keys []string) []string {
	unique := make(map[string]struct{}, len(keys))
	for _, key := range keys {
		name := key
		if !isSettingIdentifier(key) {
			name = "非法键名"
		}
		unique[name] = struct{}{}
	}
	result := make([]string, 0, len(unique))
	for key := range unique {
		result = append(result, key)
	}
	sort.Strings(result)
	return result
}

func isSettingIdentifier(key string) bool {
	if len(key) == 0 || len(key) > 128 {
		return false
	}
	for index := 0; index < len(key); index++ {
		character := key[index]
		if index == 0 {
			if (character >= 'a' && character <= 'z') || (character >= 'A' && character <= 'Z') || character == '_' {
				continue
			}
			return false
		}
		if (character >= 'a' && character <= 'z') || (character >= 'A' && character <= 'Z') || (character >= '0' && character <= '9') || character == '_' {
			continue
		}
		return false
	}
	return true
}

func keySelected(selected map[string]struct{}, key string) bool {
	if selected == nil {
		return true
	}
	_, ok := selected[key]
	return ok
}

func ensureJSONEnd(decoder *json.Decoder) error {
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		return errors.New("trailing JSON data")
	}
	return nil
}

func readFileLimited(path string, limit int64) ([]byte, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	raw, err := io.ReadAll(io.LimitReader(file, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(raw)) > limit {
		clear(raw)
		return nil, errors.New("file is too large")
	}
	return raw, nil
}

func writeAppliedDigestAtomic(path string, value []byte) error {
	temporary, err := os.CreateTemp(filepath.Dir(path), ".activation-applied-*.tmp")
	if err != nil {
		return err
	}
	temporaryPath := temporary.Name()
	defer os.Remove(temporaryPath)
	if err := temporary.Chmod(0o600); err != nil {
		_ = temporary.Close()
		return err
	}
	if _, err := temporary.Write(value); err != nil {
		_ = temporary.Close()
		return err
	}
	if err := temporary.Sync(); err != nil {
		_ = temporary.Close()
		return err
	}
	if err := temporary.Close(); err != nil {
		return err
	}
	return replaceFileAtomic(temporaryPath, path)
}
