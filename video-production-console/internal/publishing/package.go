package publishing

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"os"
	"strings"
)

const maxPackageBytes = 512 * 1024

type TitleRecommendation struct {
	Rank   int    `json:"rank"`
	Title  string `json:"title"`
	Reason string `json:"reason"`
}

type Package struct {
	Titles       []string              `json:"titles"`
	TopTitles    []TitleRecommendation `json:"top_titles"`
	ShortTitles  []string              `json:"short_titles"`
	Descriptions []string              `json:"descriptions"`
	Description  string                `json:"description"`
	Topics       []string              `json:"topics"`
	CTA          string                `json:"cta"`
}

// Reader verifies a persisted publishing package before decoding it.
type Reader struct{}

func (Reader) Read(path, expectedSHA256 string) (Package, error) {
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
		return Package{}, errors.New("publishing package is unavailable")
	}
	file, err := os.Open(path)
	if err != nil {
		return Package{}, err
	}
	data, readErr := io.ReadAll(io.LimitReader(file, maxPackageBytes+1))
	closeErr := file.Close()
	if readErr != nil || closeErr != nil || len(data) > maxPackageBytes {
		return Package{}, errors.New("publishing package could not be read")
	}
	digest := sha256.Sum256(data)
	if expectedSHA256 != "" && !strings.EqualFold(hex.EncodeToString(digest[:]), expectedSHA256) {
		return Package{}, errors.New("publishing package changed after validation")
	}
	var result Package
	if json.Unmarshal(data, &result) != nil {
		return Package{}, errors.New("publishing package is invalid")
	}
	return result, nil
}
