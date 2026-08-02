package assets

import (
	"bufio"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"image"
	_ "image/jpeg"
	_ "image/png"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/google/uuid"
	"golang.org/x/image/webp"
)

const MaxBackgroundSize int64 = 20 << 20

var (
	ErrInvalidImage     = errors.New("invalid background image")
	ErrBackgroundTooBig = errors.New("background image exceeds 20 MiB")
	ErrImageDimensions  = errors.New("background image dimensions exceed limits")
	ErrInvalidAccountID = errors.New("invalid account ID")
)

const (
	MaxImageDimension = 8192
	MaxImagePixels    = 40_000_000
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
	if err := validateImageFile(temporaryPath, mimeType); err != nil {
		return SavedAsset{}, err
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

func validateImageFile(path, mimeType string) error {
	file, err := os.Open(path)
	if err != nil {
		return fmt.Errorf("open saved background for validation: %w", err)
	}
	defer file.Close()
	var config image.Config
	switch mimeType {
	case "image/png", "image/jpeg":
		config, _, err = image.DecodeConfig(file)
	case "image/webp":
		config, err = webp.DecodeConfig(file)
	default:
		return ErrInvalidImage
	}
	if err != nil {
		return fmt.Errorf("%w: decode image config", ErrInvalidImage)
	}
	if config.Width <= 0 || config.Height <= 0 || config.Width > MaxImageDimension || config.Height > MaxImageDimension ||
		int64(config.Width)*int64(config.Height) > MaxImagePixels {
		return ErrImageDimensions
	}
	if _, err := file.Seek(0, io.SeekStart); err != nil {
		return fmt.Errorf("rewind background for validation: %w", err)
	}
	if mimeType == "image/webp" {
		_, err = webp.Decode(file)
	} else {
		_, _, err = image.Decode(file)
	}
	if err != nil {
		return fmt.Errorf("%w: decode complete image", ErrInvalidImage)
	}
	return nil
}

func (s *Service) ReconcileAccountBackgrounds(ctx context.Context, db *sql.DB, logger *log.Logger) error {
	rows, err := db.QueryContext(ctx, `SELECT path FROM assets WHERE type = 'account_background'`)
	if err != nil {
		return fmt.Errorf("list referenced account backgrounds: %w", err)
	}
	referenced := make(map[string]struct{})
	for rows.Next() {
		var path string
		if err := rows.Scan(&path); err != nil {
			rows.Close()
			return fmt.Errorf("scan referenced background: %w", err)
		}
		absolute, err := filepath.Abs(path)
		if err == nil {
			referenced[filepath.Clean(absolute)] = struct{}{}
		}
	}
	if err := rows.Close(); err != nil {
		return fmt.Errorf("close referenced backgrounds: %w", err)
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("iterate referenced backgrounds: %w", err)
	}

	accountsRoot, err := filepath.Abs(filepath.Join(s.dataRoot, "accounts"))
	if err != nil {
		return fmt.Errorf("resolve accounts data root: %w", err)
	}
	rootInfo, err := os.Stat(accountsRoot)
	if os.IsNotExist(err) {
		return nil
	} else if err != nil {
		return fmt.Errorf("stat accounts data root: %w", err)
	}
	if !rootInfo.IsDir() {
		return fmt.Errorf("accounts data root is not a directory")
	}
	var cleanupErrors []error
	err = filepath.WalkDir(accountsRoot, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			cleanupErr := fmt.Errorf("scan background path: %w", walkErr)
			cleanupErrors = append(cleanupErrors, cleanupErr)
			if logger != nil {
				logger.Printf("account background reconciliation: %v", cleanupErr)
			}
			return nil
		}
		if entry.IsDir() {
			return nil
		}
		relative, relErr := filepath.Rel(accountsRoot, path)
		if relErr != nil || relative == ".." || strings.HasPrefix(relative, ".."+string(os.PathSeparator)) {
			return nil
		}
		parts := strings.Split(relative, string(os.PathSeparator))
		if len(parts) != 3 || parts[1] != "background" {
			return nil
		}
		if _, parseErr := uuid.Parse(parts[0]); parseErr != nil {
			return nil
		}
		absolute := filepath.Clean(path)
		if _, keep := referenced[absolute]; keep && !strings.HasPrefix(entry.Name(), ".upload-") {
			return nil
		}
		if removeErr := os.Remove(path); removeErr != nil {
			cleanupErr := fmt.Errorf("remove orphan background %q: %w", path, removeErr)
			cleanupErrors = append(cleanupErrors, cleanupErr)
			if logger != nil {
				logger.Printf("account background reconciliation: %v", cleanupErr)
			}
		}
		return nil
	})
	if err != nil {
		cleanupErrors = append(cleanupErrors, err)
	}
	return errors.Join(cleanupErrors...)
}
