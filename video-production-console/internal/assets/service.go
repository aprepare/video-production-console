package assets

import (
	"bufio"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"image"
	_ "image/jpeg"
	_ "image/png"
	"io"
	"net/http"
	"os"
	"path/filepath"

	"github.com/google/uuid"
	"golang.org/x/image/webp"
)

const MaxBackgroundSize int64 = 20 << 20

var (
	ErrInvalidImage     = errors.New("invalid background image")
	ErrBackgroundTooBig = errors.New("background image exceeds 20 MiB")
	ErrInvalidAccountID = errors.New("invalid account ID")
)

type SavedAsset struct {
	Path     string
	MIMEType string
	Size     int64
	SHA256   string
}

type Service struct {
	dataRoot string
}

func NewService(dataRoot string) *Service {
	return &Service{dataRoot: filepath.Clean(dataRoot)}
}

func (s *Service) SaveAccountBackground(accountID, _ string, reader io.Reader) (saved SavedAsset, err error) {
	parsedID, err := uuid.Parse(accountID)
	if err != nil {
		return SavedAsset{}, ErrInvalidAccountID
	}

	buffered := bufio.NewReader(reader)
	header, peekErr := buffered.Peek(512)
	if peekErr != nil && !errors.Is(peekErr, io.EOF) && !errors.Is(peekErr, bufio.ErrBufferFull) {
		return SavedAsset{}, fmt.Errorf("read image header: %w", peekErr)
	}
	mimeType, extension := detectedImageType(header)
	if mimeType == "" {
		return SavedAsset{}, ErrInvalidImage
	}

	directory := filepath.Join(s.dataRoot, "accounts", parsedID.String(), "background")
	if err := os.MkdirAll(directory, 0o755); err != nil {
		return SavedAsset{}, fmt.Errorf("create background directory: %w", err)
	}
	temporary, err := os.CreateTemp(directory, ".upload-*")
	if err != nil {
		return SavedAsset{}, fmt.Errorf("create temporary background: %w", err)
	}
	temporaryPath := temporary.Name()
	defer func() {
		_ = temporary.Close()
		if err != nil {
			_ = os.Remove(temporaryPath)
		}
	}()

	hash := sha256.New()
	size, err := io.Copy(io.MultiWriter(temporary, hash), io.LimitReader(buffered, MaxBackgroundSize+1))
	if err != nil {
		return SavedAsset{}, fmt.Errorf("write background: %w", err)
	}
	if size > MaxBackgroundSize {
		return SavedAsset{}, ErrBackgroundTooBig
	}
	if err := temporary.Sync(); err != nil {
		return SavedAsset{}, fmt.Errorf("sync background: %w", err)
	}
	if err := temporary.Close(); err != nil {
		return SavedAsset{}, fmt.Errorf("close background: %w", err)
	}
	if !validImageFile(temporaryPath, mimeType) {
		return SavedAsset{}, ErrInvalidImage
	}

	finalPath := filepath.Join(directory, uuid.NewString()+extension)
	if err := os.Rename(temporaryPath, finalPath); err != nil {
		return SavedAsset{}, fmt.Errorf("publish background: %w", err)
	}
	return SavedAsset{
		Path:     finalPath,
		MIMEType: mimeType,
		Size:     size,
		SHA256:   hex.EncodeToString(hash.Sum(nil)),
	}, nil
}

func detectedImageType(header []byte) (mimeType, extension string) {
	switch http.DetectContentType(header) {
	case "image/png":
		return "image/png", ".png"
	case "image/jpeg":
		return "image/jpeg", ".jpg"
	case "image/webp":
		return "image/webp", ".webp"
	default:
		return "", ""
	}
}

func validImageFile(path, mimeType string) bool {
	file, err := os.Open(path)
	if err != nil {
		return false
	}
	defer file.Close()
	switch mimeType {
	case "image/png", "image/jpeg":
		_, _, err := image.DecodeConfig(file)
		return err == nil
	case "image/webp":
		_, err := webp.DecodeConfig(file)
		return err == nil
	default:
		return false
	}
}
