package imagevideo

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"video-production-console/internal/domain"
	"video-production-console/internal/narration"
)

const defaultNarrationSynthesizerVersion = "narration-produce-v1"

type NarrationArtifact struct {
	InputFingerprint   string                       `json:"input_fingerprint"`
	VoiceProfile       string                       `json:"voice_profile"`
	SynthesizerVersion string                       `json:"synthesizer_version"`
	AudioRelativePath  string                       `json:"audio_relative_path"`
	AudioSHA256        string                       `json:"audio_sha256"`
	TimingRelativePath string                       `json:"timing_relative_path"`
	TimingSHA256       string                       `json:"timing_sha256"`
	DurationUS         int64                        `json:"duration_us"`
	TimingDocument     narration.WordTimingDocument `json:"timing_document"`
	Reused             bool                         `json:"reused"`
}

type NarrationAdapter struct {
	dataRoot           string
	provider           string
	voiceProfile       string
	synthesizerVersion string
	synthesizer        narration.Synthesizer
	request            narration.ProduceRequest
}

type narrationMetadata struct {
	InputFingerprint   string `json:"input_fingerprint"`
	VoiceProfile       string `json:"voice_profile"`
	SynthesizerVersion string `json:"synthesizer_version"`
	AudioRelativePath  string `json:"audio_relative_path"`
	AudioSHA256        string `json:"audio_sha256"`
	TimingRelativePath string `json:"timing_relative_path"`
	TimingSHA256       string `json:"timing_sha256"`
	DurationUS         int64  `json:"duration_us"`
}

// NewNarrationAdapterFromRuntime creates a process-only adapter. Credentials
// stay inside the synthesizer client and are never included in fingerprints,
// metadata, job snapshots, or API models.
func NewNarrationAdapterFromRuntime(runtime RuntimeSnapshot, synthesizerVersion string) (*NarrationAdapter, error) {
	dataRoot := strings.TrimSpace(runtime.DataRoot)
	if dataRoot == "" || !filepath.IsAbs(dataRoot) {
		return nil, fmt.Errorf("image video narration data root is not configured")
	}
	provider := strings.ToLower(strings.TrimSpace(runtime.TTSProvider))
	if provider == "" {
		provider = "volc"
	}
	if strings.TrimSpace(synthesizerVersion) == "" {
		synthesizerVersion = defaultNarrationSynthesizerVersion
	}
	adapter := &NarrationAdapter{dataRoot: dataRoot, provider: provider, synthesizerVersion: synthesizerVersion}
	switch provider {
	case "volc":
		apiKey := strings.TrimSpace(runtime.VolcSpeechAPIKey)
		speakerID := strings.TrimSpace(runtime.VolcSpeechSpeakerID)
		resourceID := strings.TrimSpace(runtime.VolcSpeechResourceID)
		if apiKey == "" || speakerID == "" {
			return nil, narration.ErrNotConfigured
		}
		adapter.synthesizer = &narration.Client{APIKey: apiKey, ResourceID: resourceID}
		adapter.request = narration.ProduceRequest{SpeakerID: speakerID, Format: "mp3", Provider: "volc"}
		adapter.voiceProfile = strings.Join([]string{"provider=volc", "speaker=" + speakerID, "resource=" + resourceID}, "|")
	case "aurastd":
		baseURL := strings.TrimSpace(runtime.AuraSTDBaseURL)
		apiKey := strings.TrimSpace(runtime.AuraSTDTTsAPIKey)
		voiceID := strings.TrimSpace(runtime.AuraSTDVoiceID)
		if baseURL == "" || apiKey == "" || voiceID == "" {
			return nil, narration.ErrNotConfigured
		}
		adapter.synthesizer = &narration.AuraSTDClient{BaseURL: baseURL, APIKey: apiKey}
		adapter.request = narration.ProduceRequest{
			SpeakerID:       voiceID,
			Format:          "mp3",
			Provider:        "aurastd",
			Model:           strings.TrimSpace(runtime.AuraSTDModel),
			Speed:           runtime.AuraSTDSpeed,
			Volume:          runtime.AuraSTDVolume,
			Pitch:           runtime.AuraSTDPitch,
			Emotion:         runtime.AuraSTDEmotion,
			LanguageBoost:   runtime.AuraSTDLanguageBoost,
			ModifyPitch:     runtime.AuraSTDModifyPitch,
			ModifyIntensity: runtime.AuraSTDModifyIntensity,
			ModifyTimbre:    runtime.AuraSTDModifyTimbre,
			SoundEffects:    runtime.AuraSTDSoundEffects,
		}
		adapter.voiceProfile = fmt.Sprintf("provider=aurastd|voice=%s|model=%s|speed=%g|volume=%g|pitch=%d|emotion=%s|language=%s|modify=%d,%d,%d|effects=%s",
			voiceID, adapter.request.Model, adapter.request.Speed, adapter.request.Volume, adapter.request.Pitch, adapter.request.Emotion, adapter.request.LanguageBoost,
			adapter.request.ModifyPitch, adapter.request.ModifyIntensity, adapter.request.ModifyTimbre, adapter.request.SoundEffects)
	default:
		return nil, fmt.Errorf("unsupported TTS provider %q", provider)
	}
	return adapter, nil
}

func NewNarrationAdapter(dataRoot, provider, voiceProfile, synthesizerVersion string, synthesizer narration.Synthesizer, request narration.ProduceRequest) (*NarrationAdapter, error) {
	if strings.TrimSpace(dataRoot) == "" || !filepath.IsAbs(dataRoot) || synthesizer == nil || strings.TrimSpace(voiceProfile) == "" {
		return nil, narration.ErrNotConfigured
	}
	if strings.TrimSpace(synthesizerVersion) == "" {
		synthesizerVersion = defaultNarrationSynthesizerVersion
	}
	return &NarrationAdapter{
		dataRoot:           filepath.Clean(dataRoot),
		provider:           strings.TrimSpace(provider),
		voiceProfile:       strings.TrimSpace(voiceProfile),
		synthesizerVersion: strings.TrimSpace(synthesizerVersion),
		synthesizer:        synthesizer,
		request:            request,
	}, nil
}

func NarrationInputFingerprint(script, voiceProfile, synthesizerVersion string) string {
	sum := sha256.Sum256([]byte(script + "\x00" + voiceProfile + "\x00" + synthesizerVersion))
	return hex.EncodeToString(sum[:])
}

// Produce creates or reuses narration and word timings for an ImageProject.
// Subtitle data may be produced internally by narration.Produce for alignment,
// but this adapter persists no SRT and exposes no caption-track instruction.
func (a *NarrationAdapter) Produce(ctx context.Context, project domain.ImageProject) (NarrationArtifact, error) {
	if a == nil || a.synthesizer == nil || strings.TrimSpace(project.ID) == "" || strings.TrimSpace(project.Script) == "" {
		return NarrationArtifact{}, fmt.Errorf("image video narration input is incomplete")
	}
	if project.ID == "." || project.ID == ".." || strings.ContainsAny(project.ID, `/\\:`) {
		return NarrationArtifact{}, fmt.Errorf("invalid image project id")
	}
	fingerprint := NarrationInputFingerprint(project.Script, a.voiceProfile, a.synthesizerVersion)
	directory, err := a.managedNarrationDirectory(project.ID)
	if err != nil {
		return NarrationArtifact{}, err
	}
	baseName := "narration-" + fingerprint[:20]
	audioName := baseName + ".mp3"
	timingName := baseName + ".word_timing.json"
	metadataName := baseName + ".meta.json"
	audioPath := filepath.Join(directory, audioName)
	timingPath := filepath.Join(directory, timingName)
	metadataPath := filepath.Join(directory, metadataName)
	audioRelativePath := filepath.ToSlash(filepath.Join("narration", audioName))
	timingRelativePath := filepath.ToSlash(filepath.Join("narration", timingName))
	if artifact, ok := loadReusableNarration(metadataPath, audioPath, timingPath, fingerprint, a.voiceProfile, a.synthesizerVersion, audioRelativePath, timingRelativePath); ok {
		artifact.Reused = true
		return artifact, nil
	}
	lock, err := acquireNarrationLock(ctx, filepath.Join(directory, baseName+".lock"))
	if err != nil {
		return NarrationArtifact{}, err
	}
	defer lock.Release()
	if artifact, ok := loadReusableNarration(metadataPath, audioPath, timingPath, fingerprint, a.voiceProfile, a.synthesizerVersion, audioRelativePath, timingRelativePath); ok {
		artifact.Reused = true
		return artifact, nil
	}

	request := a.request
	request.Script = project.Script
	request.Provider = a.provider
	delivery, produceErr := narration.Produce(ctx, a.synthesizer, request)
	if produceErr != nil {
		var qualityErr *narration.QualityGateError
		if !errors.As(produceErr, &qualityErr) {
			return NarrationArtifact{}, produceErr
		}
	}
	if len(delivery.Audio) == 0 || len(delivery.TimingDocument.Words) == 0 || delivery.TimingDocument.Duration <= 0 {
		return NarrationArtifact{}, fmt.Errorf("TTS returned incomplete narration evidence")
	}
	audioSHA := sha256Hex(delivery.Audio)
	delivery.TimingDocument.Hash = "sha256:" + audioSHA
	timingBytes, err := json.MarshalIndent(delivery.TimingDocument, "", "  ")
	if err != nil {
		return NarrationArtifact{}, err
	}
	timingBytes = append(timingBytes, '\n')
	timingSHA := sha256Hex(timingBytes)
	durationUS := secondsToUS(delivery.TimingDocument.Duration)
	metadata := narrationMetadata{
		InputFingerprint:   fingerprint,
		VoiceProfile:       a.voiceProfile,
		SynthesizerVersion: a.synthesizerVersion,
		AudioRelativePath:  audioRelativePath,
		AudioSHA256:        audioSHA,
		TimingRelativePath: timingRelativePath,
		TimingSHA256:       timingSHA,
		DurationUS:         durationUS,
	}
	metadataBytes, err := json.MarshalIndent(metadata, "", "  ")
	if err != nil {
		return NarrationArtifact{}, err
	}
	metadataBytes = append(metadataBytes, '\n')
	if err := writeContentAddressedFile(audioPath, delivery.Audio); err != nil {
		return NarrationArtifact{}, err
	}
	if err := writeContentAddressedFile(timingPath, timingBytes); err != nil {
		return NarrationArtifact{}, err
	}
	if err := writeContentAddressedFile(metadataPath, metadataBytes); err != nil {
		return NarrationArtifact{}, err
	}
	return NarrationArtifact{
		InputFingerprint:   fingerprint,
		VoiceProfile:       a.voiceProfile,
		SynthesizerVersion: a.synthesizerVersion,
		AudioRelativePath:  audioRelativePath,
		AudioSHA256:        audioSHA,
		TimingRelativePath: timingRelativePath,
		TimingSHA256:       timingSHA,
		DurationUS:         durationUS,
		TimingDocument:     delivery.TimingDocument,
	}, nil
}

func (a *NarrationAdapter) managedNarrationDirectory(projectID string) (string, error) {
	root, err := filepath.Abs(a.dataRoot)
	if err != nil {
		return "", err
	}
	realRoot, err := resolveExistingPath(root)
	if err != nil {
		return "", err
	}
	candidate := filepath.Join(root, "image-projects", projectID, "narration")
	realCandidate, err := resolveExistingPath(candidate)
	if err != nil {
		return "", err
	}
	if _, ok := relativeWithin(realRoot, realCandidate, false); !ok {
		return "", fmt.Errorf("image project narration directory escapes data root")
	}
	if err := os.MkdirAll(realCandidate, 0o700); err != nil {
		return "", err
	}
	verified, err := filepath.EvalSymlinks(realCandidate)
	if err != nil {
		return "", err
	}
	if _, ok := relativeWithin(realRoot, verified, false); !ok {
		return "", fmt.Errorf("image project narration directory escapes data root")
	}
	return verified, nil
}

func loadReusableNarration(metadataPath, audioPath, timingPath, fingerprint, voiceProfile, synthesizerVersion, audioRelativePath, timingRelativePath string) (NarrationArtifact, bool) {
	metadataBytes, err := os.ReadFile(metadataPath)
	if err != nil {
		return NarrationArtifact{}, false
	}
	var metadata narrationMetadata
	if json.Unmarshal(metadataBytes, &metadata) != nil || metadata.InputFingerprint != fingerprint || metadata.VoiceProfile != voiceProfile || metadata.SynthesizerVersion != synthesizerVersion || metadata.AudioRelativePath != audioRelativePath || metadata.TimingRelativePath != timingRelativePath {
		return NarrationArtifact{}, false
	}
	audioBytes, err := os.ReadFile(audioPath)
	if err != nil || !strings.EqualFold(sha256Hex(audioBytes), metadata.AudioSHA256) {
		return NarrationArtifact{}, false
	}
	timingBytes, err := os.ReadFile(timingPath)
	if err != nil || !strings.EqualFold(sha256Hex(timingBytes), metadata.TimingSHA256) {
		return NarrationArtifact{}, false
	}
	var document narration.WordTimingDocument
	if json.Unmarshal(timingBytes, &document) != nil || len(document.Words) == 0 || secondsToUS(document.Duration) != metadata.DurationUS {
		return NarrationArtifact{}, false
	}
	validated, err := narration.NewWordTimingDocument(document.Script, document.Provider, document.Hash, document.Words)
	if err != nil || validated.ScriptHash != document.ScriptHash || secondsToUS(validated.Duration) != metadata.DurationUS {
		return NarrationArtifact{}, false
	}
	return NarrationArtifact{
		InputFingerprint:   metadata.InputFingerprint,
		VoiceProfile:       metadata.VoiceProfile,
		SynthesizerVersion: metadata.SynthesizerVersion,
		AudioRelativePath:  metadata.AudioRelativePath,
		AudioSHA256:        metadata.AudioSHA256,
		TimingRelativePath: metadata.TimingRelativePath,
		TimingSHA256:       metadata.TimingSHA256,
		DurationUS:         metadata.DurationUS,
		TimingDocument:     document,
	}, true
}

func writeContentAddressedFile(path string, data []byte) error {
	if existing, err := os.ReadFile(path); err == nil {
		if sha256Hex(existing) == sha256Hex(data) {
			return nil
		}
		return fmt.Errorf("content-addressed narration file already exists with different content")
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	temp, err := os.CreateTemp(filepath.Dir(path), ".narration-*.tmp")
	if err != nil {
		return err
	}
	tempPath := temp.Name()
	defer os.Remove(tempPath)
	if err := temp.Chmod(0o600); err != nil {
		temp.Close()
		return err
	}
	if _, err := temp.Write(data); err != nil {
		temp.Close()
		return err
	}
	if err := temp.Sync(); err != nil {
		temp.Close()
		return err
	}
	if err := temp.Close(); err != nil {
		return err
	}
	if err := os.Rename(tempPath, path); err != nil {
		if existing, readErr := os.ReadFile(path); readErr == nil && sha256Hex(existing) == sha256Hex(data) {
			return nil
		}
		return err
	}
	return nil
}

func sha256Hex(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}
