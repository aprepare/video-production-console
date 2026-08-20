package portable

import (
	"archive/zip"
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"os"
)

const (
	trailerMagic = "VPCPARTNERPAY01!"
	trailerSize  = 16 + 8 + 8
)

type Overlay struct {
	Manifest    Manifest
	PayloadSize int64
	Zip         *zip.Reader
}

func ReadOverlay(r io.ReaderAt, size int64) (*Overlay, error) {
	if r == nil || size < trailerSize {
		return nil, fmt.Errorf("%w: truncated", ErrInvalidOverlay)
	}
	trailer := make([]byte, trailerSize)
	if _, err := r.ReadAt(trailer, size-trailerSize); err != nil {
		return nil, fmt.Errorf("%w: read trailer: %v", ErrInvalidOverlay, err)
	}
	if string(trailer[:16]) != trailerMagic {
		return nil, fmt.Errorf("%w: magic", ErrInvalidOverlay)
	}
	zipLen := binary.LittleEndian.Uint64(trailer[16:24])
	manifestLen := binary.LittleEndian.Uint64(trailer[24:32])
	zipOff, err := overlayOffsets(uint64(size), zipLen, manifestLen)
	if err != nil {
		return nil, err
	}

	zipBytes := make([]byte, zipLen)
	if _, err := r.ReadAt(zipBytes, zipOff); err != nil {
		return nil, fmt.Errorf("%w: read payload: %v", ErrInvalidOverlay, err)
	}
	manifestBytes := make([]byte, manifestLen)
	if _, err := r.ReadAt(manifestBytes, zipOff+int64(zipLen)); err != nil {
		return nil, fmt.Errorf("%w: read manifest: %v", ErrInvalidOverlay, err)
	}

	dec := json.NewDecoder(bytes.NewReader(manifestBytes))
	dec.DisallowUnknownFields()
	var manifest Manifest
	if err := dec.Decode(&manifest); err != nil {
		return nil, fmt.Errorf("%w: manifest json: %v", ErrInvalidOverlay, err)
	}
	if dec.More() {
		return nil, fmt.Errorf("%w: trailing manifest json", ErrInvalidOverlay)
	}
	if err := manifest.Validate(); err != nil {
		return nil, err
	}
	if !validSHA256(manifest.PayloadSHA256) {
		return nil, fmt.Errorf("%w: payload_sha256", ErrInvalidManifest)
	}
	if sha256Hex(zipBytes) != manifest.PayloadSHA256 {
		return nil, fmt.Errorf("%w: payload", ErrEntryHash)
	}

	reader, err := zip.NewReader(bytes.NewReader(zipBytes), int64(len(zipBytes)))
	if err != nil {
		return nil, fmt.Errorf("%w: zip: %v", ErrInvalidOverlay, err)
	}
	for _, file := range reader.File {
		if err := validateZipHeader(file); err != nil {
			return nil, err
		}
	}
	return &Overlay{Manifest: manifest, PayloadSize: int64(zipLen), Zip: reader}, nil
}

func overlayOffsets(size, zipLen, manifestLen uint64) (int64, error) {
	if zipLen == 0 || manifestLen == 0 {
		return 0, fmt.Errorf("%w: non-positive lengths", ErrInvalidOverlay)
	}
	if zipLen > math.MaxInt64 || manifestLen > math.MaxInt64 {
		return 0, fmt.Errorf("%w: length overflow", ErrInvalidOverlay)
	}
	if size < trailerSize {
		return 0, fmt.Errorf("%w: truncated", ErrInvalidOverlay)
	}
	remaining := size - trailerSize
	if zipLen > remaining || manifestLen > remaining-zipLen {
		return 0, fmt.Errorf("%w: bounds", ErrInvalidOverlay)
	}
	return int64(remaining - zipLen - manifestLen), nil
}

func validateZipHeader(file *zip.File) error {
	if file == nil {
		return fmt.Errorf("%w: nil zip entry", ErrInvalidOverlay)
	}
	mode := file.Mode()
	if mode&os.ModeSymlink != 0 {
		return fmt.Errorf("%w: %q", ErrSymlink, file.Name)
	}
	if mode&(os.ModeType) != 0 || !mode.IsRegular() {
		return fmt.Errorf("%w: non-regular %q", ErrUnsafePath, file.Name)
	}
	if err := validateRelPath(file.Name); err != nil {
		return err
	}
	return nil
}

func sha256Hex(body []byte) string {
	sum := sha256.Sum256(body)
	return hex.EncodeToString(sum[:])
}
