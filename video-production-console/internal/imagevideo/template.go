package imagevideo

import (
	"crypto/sha256"
	"embed"
	"encoding/hex"
)

//go:embed templates/jianying-image-video-v1.json
var templateFS embed.FS

const BuiltInTemplateVersion = "jianying-image-video-v1"

func BuiltInTemplate() ([]byte, string, error) {
	b, err := templateFS.ReadFile("templates/jianying-image-video-v1.json")
	if err != nil {
		return nil, "", err
	}
	h := sha256.Sum256(b)
	return append([]byte(nil), b...), hex.EncodeToString(h[:]), nil
}
