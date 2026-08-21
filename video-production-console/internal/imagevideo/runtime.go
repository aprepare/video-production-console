package imagevideo

// RuntimeSnapshot is the process-local view of console settings that image
// video needs. It lives here so this package does not import settings and
// create an import cycle through store.
type RuntimeSnapshot struct {
	DataRoot, MachineProfilePath, JianyingRoot string
	GrokBaseURL, GrokAPIKey                    string
	FFmpegPath, FFprobePath                    string
	TTSProvider                                string
	VolcSpeechAPIKey, VolcSpeechSpeakerID, VolcSpeechResourceID string
	AuraSTDBaseURL, AuraSTDTTsAPIKey, AuraSTDVoiceID, AuraSTDModel string
	AuraSTDSpeed                               float64
	AuraSTDVolume                              float64
	AuraSTDPitch                               int
	AuraSTDEmotion, AuraSTDLanguageBoost       string
	AuraSTDModifyPitch, AuraSTDModifyIntensity, AuraSTDModifyTimbre int
	AuraSTDSoundEffects                        string
	RemixAPIKey, PexelsAPIKey, ImageAPIKey, ImageTextAPIKey string
	VisionAPIKey, EmbeddingAPIKey, PixabayAPIKey            string
}
