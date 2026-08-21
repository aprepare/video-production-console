package imagevideoruntime

import (
	"context"

	"video-production-console/internal/imagevideo"
	"video-production-console/internal/settings"
)

func Snapshot(runtime settings.Runtime) imagevideo.RuntimeSnapshot {
	return imagevideo.RuntimeSnapshot{
		DataRoot: runtime.DataRoot, MachineProfilePath: runtime.MachineProfilePath, JianyingRoot: runtime.JianyingRoot,
		GrokBaseURL: runtime.GrokBaseURL, GrokAPIKey: runtime.GrokAPIKey,
		FFmpegPath: runtime.FFmpegPath, FFprobePath: runtime.FFprobePath,
		TTSProvider: runtime.TTSProvider,
		VolcSpeechAPIKey: runtime.VolcSpeechAPIKey, VolcSpeechSpeakerID: runtime.VolcSpeechSpeakerID, VolcSpeechResourceID: runtime.VolcSpeechResourceID,
		AuraSTDBaseURL: runtime.AuraSTDBaseURL, AuraSTDTTsAPIKey: runtime.AuraSTDTTsAPIKey, AuraSTDVoiceID: runtime.AuraSTDVoiceID, AuraSTDModel: runtime.AuraSTDModel,
		AuraSTDSpeed: runtime.AuraSTDSpeed, AuraSTDVolume: runtime.AuraSTDVolume, AuraSTDPitch: runtime.AuraSTDPitch,
		AuraSTDEmotion: runtime.AuraSTDEmotion, AuraSTDLanguageBoost: runtime.AuraSTDLanguageBoost,
		AuraSTDModifyPitch: runtime.AuraSTDModifyPitch, AuraSTDModifyIntensity: runtime.AuraSTDModifyIntensity, AuraSTDModifyTimbre: runtime.AuraSTDModifyTimbre,
		AuraSTDSoundEffects: runtime.AuraSTDSoundEffects,
		RemixAPIKey: runtime.RemixAPIKey, PexelsAPIKey: runtime.PexelsAPIKey, ImageAPIKey: runtime.ImageAPIKey, ImageTextAPIKey: runtime.ImageTextAPIKey,
		VisionAPIKey: runtime.VisionAPIKey, EmbeddingAPIKey: runtime.EmbeddingAPIKey, PixabayAPIKey: runtime.PixabayAPIKey,
	}
}

type Provider struct {
	Inner interface {
		Runtime(context.Context) (settings.Runtime, error)
	}
}

func (p Provider) Runtime(ctx context.Context) (imagevideo.RuntimeSnapshot, error) {
	runtime, err := p.Inner.Runtime(ctx)
	if err != nil {
		return imagevideo.RuntimeSnapshot{}, err
	}
	return Snapshot(runtime), nil
}
