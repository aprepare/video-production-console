package montage

import "video-production-console/internal/imagevideo"

func ValidateImageVideoRegistration(request imagevideo.RegistrationRequest, receiptPath string) (imagevideo.RegistrationResult, error) {
	result, err := ValidateRegisteredDraft(ValidationRequest{
		TaskID:        request.JobID,
		DisplayName:   request.DisplayName,
		WorkspacePath: request.WorkspacePath,
		ReceiptPath:   receiptPath,
		JianyingRoot:  request.JianyingRoot,
	})
	if err != nil {
		return imagevideo.RegistrationResult{}, err
	}
	return imagevideo.RegistrationResult{
		RegisteredPath: result.RegisteredPath, ReceiptPath: result.ReceiptPath,
		DraftID: result.DraftID, DisplayName: result.DisplayName,
		SourceContentSHA256:     result.SourceContentSHA256,
		RegisteredContentSHA256: result.RegisteredContentSHA256,
		DirectorySHA256:         result.DirectorySHA256, DurationUS: result.DurationUS,
	}, nil
}
