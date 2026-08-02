package assets

import (
	"bufio"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/binary"
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
	"unicode/utf8"

	"github.com/google/uuid"
	"golang.org/x/image/webp"

	"video-production-console/internal/domain"
)

const MaxBackgroundSize int64 = 20 << 20
const MaxProjectAssetSize int64 = 500 << 20
const MaxTextAssetSize int64 = 5 << 20
const MaxAudioAssetSize int64 = 200 << 20

func MaxSizeForType(assetType domain.AssetType) int64 {
	switch assetType {
	case domain.AssetContinuousScript, domain.AssetSpokenScript, domain.AssetSubtitle:
		return MaxTextAssetSize
	case domain.AssetAudio:
		return MaxAudioAssetSize
	case domain.AssetMixDraft, domain.AssetFinalVideo:
		return MaxProjectAssetSize
	}
	return 0
}

var (
	ErrInvalidImage        = errors.New("invalid background image")
	ErrBackgroundTooBig    = errors.New("background image exceeds 20 MiB")
	ErrImageDimensions     = errors.New("background image dimensions exceed limits")
	ErrInvalidAccountID    = errors.New("invalid account ID")
	ErrInvalidProjectAsset = errors.New("invalid project asset")
	ErrProjectAssetTooBig  = errors.New("project asset exceeds 500 MiB")
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

func (s *Service) SaveProjectAsset(projectID string, assetType domain.AssetType, filename string, reader io.Reader) (saved SavedAsset, err error) {
	parsedID, parseErr := uuid.Parse(projectID)
	if parseErr != nil {
		return SavedAsset{}, ErrInvalidProjectAsset
	}
	ext := strings.ToLower(filepath.Ext(filename))
	allowed := map[domain.AssetType]map[string]bool{
		domain.AssetContinuousScript: {".txt": true, ".md": true}, domain.AssetSpokenScript: {".txt": true, ".md": true},
		domain.AssetSubtitle: {".srt": true}, domain.AssetAudio: {".mp3": true, ".wav": true, ".m4a": true},
		domain.AssetMixDraft: {".mp4": true}, domain.AssetFinalVideo: {".mp4": true},
	}
	if !allowed[assetType][ext] {
		return SavedAsset{}, ErrInvalidProjectAsset
	}
	maxSize := MaxSizeForType(assetType)
	if maxSize == 0 {
		return SavedAsset{}, ErrInvalidProjectAsset
	}
	directory := filepath.Join(s.dataRoot, "projects", parsedID.String(), string(assetType))
	if err = os.MkdirAll(directory, 0o755); err != nil {
		return SavedAsset{}, fmt.Errorf("create project asset directory: %w", err)
	}
	temporary, err := os.CreateTemp(directory, ".upload-*")
	if err != nil {
		return SavedAsset{}, fmt.Errorf("create project asset temp: %w", err)
	}
	temporaryPath := temporary.Name()
	defer func() {
		_ = temporary.Close()
		if err != nil {
			_ = os.Remove(temporaryPath)
		}
	}()
	hash := sha256.New()
	size, err := io.Copy(io.MultiWriter(temporary, hash), io.LimitReader(reader, maxSize+1))
	if err != nil {
		return SavedAsset{}, fmt.Errorf("write project asset: %w", err)
	}
	if size > maxSize {
		return SavedAsset{}, ErrProjectAssetTooBig
	}
	if err = temporary.Sync(); err != nil {
		return SavedAsset{}, err
	}
	if err = temporary.Close(); err != nil {
		return SavedAsset{}, err
	}
	mimeType, err := validateProjectFile(temporaryPath, assetType, ext)
	if err != nil {
		return SavedAsset{}, err
	}
	if mimeType == "" {
		return SavedAsset{}, ErrInvalidProjectAsset
	}
	finalPath := filepath.Join(directory, uuid.NewString()+ext)
	if err = os.Rename(temporaryPath, finalPath); err != nil {
		return SavedAsset{}, fmt.Errorf("publish project asset: %w", err)
	}
	return SavedAsset{Path: finalPath, MIMEType: mimeType, Size: size, SHA256: hex.EncodeToString(hash.Sum(nil))}, nil
}

func validateProjectFile(path string, assetType domain.AssetType, ext string) (string, error) {
	if ext == ".m4a" || ext == ".mp4" {
		handlers, err := isoBMFFHandlers(path)
		if err != nil {
			return "", nil
		}
		if ext == ".m4a" && assetType == domain.AssetAudio && handlers["soun"] && !handlers["vide"] {
			return "audio/mp4", nil
		}
		if ext == ".mp4" && (assetType == domain.AssetMixDraft || assetType == domain.AssetFinalVideo) && handlers["vide"] {
			return "video/mp4", nil
		}
		return "", nil
	}
	file, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer file.Close()
	if assetType == domain.AssetContinuousScript || assetType == domain.AssetSpokenScript || assetType == domain.AssetSubtitle {
		reader := bufio.NewReader(file)
		for {
			r, size, readErr := reader.ReadRune()
			if readErr == io.EOF {
				break
			}
			if readErr != nil || r == '\x00' || r == utf8.RuneError && size == 1 {
				return "", nil
			}
		}
		if ext == ".md" {
			return "text/markdown; charset=utf-8", nil
		}
		if ext == ".srt" {
			return "application/x-subrip; charset=utf-8", nil
		}
		return "text/plain; charset=utf-8", nil
	}
	if ext == ".wav" {
		ok, err := validWAVFile(file)
		if ok {
			return "audio/wav", nil
		}
		return "", err
	}
	if ext == ".mp3" {
		ok, err := validMP3File(file)
		if ok {
			return "audio/mpeg", nil
		}
		return "", err
	}
	return "", nil
}

func validWAVFile(file *os.File) (bool, error) {
	info, err := file.Stat()
	if err != nil {
		return false, err
	}
	if info.Size() < 44 {
		return false, nil
	}
	h := make([]byte, 12)
	if _, err = file.ReadAt(h, 0); err != nil {
		return false, err
	}
	if string(h[:4]) != "RIFF" || string(h[8:]) != "WAVE" || int64(binary.LittleEndian.Uint32(h[4:8]))+8 != info.Size() {
		return false, nil
	}
	hasFmt, hasData := false, false
	for off := int64(12); off < info.Size(); {
		if info.Size()-off < 8 {
			return false, nil
		}
		ch := make([]byte, 8)
		if _, err = file.ReadAt(ch, off); err != nil {
			return false, err
		}
		n := int64(binary.LittleEndian.Uint32(ch[4:]))
		end := off + 8 + n
		if end < off+8 || end > info.Size() {
			return false, nil
		}
		switch string(ch[:4]) {
		case "fmt ":
			hasFmt = n >= 16
		case "data":
			hasData = true
		}
		off = end + (n & 1)
		if off > info.Size() {
			return false, nil
		}
	}
	return hasFmt && hasData, nil
}
func validMP3File(file *os.File) (bool, error) {
	info, err := file.Stat()
	if err != nil {
		return false, err
	}
	off := int64(0)
	if info.Size() >= 10 {
		h := make([]byte, 10)
		_, _ = file.ReadAt(h, 0)
		if string(h[:3]) == "ID3" {
			for _, b := range h[6:10] {
				if b&0x80 != 0 {
					return false, nil
				}
			}
			size := int64(h[6])<<21 | int64(h[7])<<14 | int64(h[8])<<7 | int64(h[9])
			off = 10 + size
			if h[5]&0x10 != 0 {
				off += 10
			}
			if off > info.Size() {
				return false, nil
			}
		}
	}
	for i := 0; i < 2; i++ {
		if off+4 > info.Size() {
			return false, nil
		}
		h := make([]byte, 4)
		if _, err = file.ReadAt(h, off); err != nil {
			return false, err
		}
		length := mpegFrameLength(h)
		if length <= 4 || off+int64(length) > info.Size() {
			return false, nil
		}
		off += int64(length)
	}
	return true, nil
}
func mpegFrameLength(h []byte) int {
	if len(h) < 4 || h[0] != 0xff || h[1]&0xe0 != 0xe0 {
		return 0
	}
	version := (h[1] >> 3) & 3
	layer := (h[1] >> 1) & 3
	br := (h[2] >> 4) & 15
	sr := (h[2] >> 2) & 3
	if version == 1 || layer == 0 || br == 0 || br == 15 || sr == 3 {
		return 0
	}
	rates := []int{44100, 48000, 32000}
	sample := rates[sr]
	if version == 2 {
		sample /= 2
	} else if version == 0 {
		sample /= 4
	}
	mpeg1 := version == 3
	var kbps int
	if layer == 3 {
		table := []int{0, 32, 64, 96, 128, 160, 192, 224, 256, 288, 320, 352, 384, 416, 448}
		kbps = table[br]
		return (12*kbps*1000/sample + int(h[2]>>1&1)) * 4
	}
	if layer == 2 {
		if mpeg1 {
			table := []int{0, 32, 48, 56, 64, 80, 96, 112, 128, 160, 192, 224, 256, 320, 384}
			kbps = table[br]
		} else {
			table := []int{0, 8, 16, 24, 32, 40, 48, 56, 64, 80, 96, 112, 128, 144, 160}
			kbps = table[br]
		}
	} else {
		if mpeg1 {
			table := []int{0, 32, 40, 48, 56, 64, 80, 96, 112, 128, 160, 192, 224, 256, 320}
			kbps = table[br]
		} else {
			table := []int{0, 8, 16, 24, 32, 40, 48, 56, 64, 80, 96, 112, 128, 144, 160}
			kbps = table[br]
		}
	}
	coef := 144
	if layer == 1 && !mpeg1 {
		coef = 72
	}
	return coef*kbps*1000/sample + int(h[2]>>1&1)
}

func validateProjectData(assetType domain.AssetType, ext string, data []byte) string {
	if assetType == domain.AssetContinuousScript || assetType == domain.AssetSpokenScript || assetType == domain.AssetSubtitle {
		if !utf8.Valid(data) || bytesContainsNUL(data) {
			return ""
		}
		if ext == ".md" {
			return "text/markdown; charset=utf-8"
		}
		if ext == ".srt" {
			return "application/x-subrip; charset=utf-8"
		}
		return "text/plain; charset=utf-8"
	}
	if assetType == domain.AssetAudio {
		switch ext {
		case ".mp3":
			if len(data) >= 3 && string(data[:3]) == "ID3" || len(data) >= 2 && data[0] == 0xff && data[1]&0xe0 == 0xe0 {
				return "audio/mpeg"
			}
		case ".wav":
			if len(data) >= 12 && string(data[:4]) == "RIFF" && string(data[8:12]) == "WAVE" {
				return "audio/wav"
			}
		}
		return ""
	}
	return ""
}

func isoBMFFHandlers(path string) (map[string]bool, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return nil, err
	}
	handlers := map[string]bool{}
	seenFTYP := false
	seenMOOV := false
	err = walkISOBoxes(file, 0, info.Size(), "root", handlers, func(kind string) {
		if kind == "ftyp" {
			seenFTYP = true
		}
		if kind == "moov" {
			seenMOOV = true
		}
	})
	if err != nil || !seenFTYP || !seenMOOV {
		return nil, ErrInvalidProjectAsset
	}
	return handlers, nil
}

func walkISOBoxes(file *os.File, start, end int64, parent string, handlers map[string]bool, seen func(string)) error {
	for offset := start; offset < end; {
		if end-offset < 8 {
			return ErrInvalidProjectAsset
		}
		header := make([]byte, 8)
		if _, err := file.ReadAt(header, offset); err != nil {
			return ErrInvalidProjectAsset
		}
		size32 := binary.BigEndian.Uint32(header[:4])
		size := int64(size32)
		headerSize := int64(8)
		if size32 == 0 {
			size = end - offset
		} else if size32 == 1 {
			if end-offset < 16 {
				return ErrInvalidProjectAsset
			}
			large := make([]byte, 8)
			if _, err := file.ReadAt(large, offset+8); err != nil {
				return ErrInvalidProjectAsset
			}
			largeSize := binary.BigEndian.Uint64(large)
			if largeSize > uint64(^uint64(0)>>1) {
				return ErrInvalidProjectAsset
			}
			size = int64(largeSize)
			headerSize = 16
		}
		if size < headerSize || size > end-offset {
			return ErrInvalidProjectAsset
		}
		kind := string(header[4:8])
		if parent == "root" && kind == "ftyp" && size < 16 {
			return ErrInvalidProjectAsset
		}
		seen(kind)
		payloadStart := offset + headerSize
		boxEnd := offset + size
		switch {
		case parent == "root" && kind == "moov", parent == "moov" && kind == "trak", parent == "trak" && kind == "mdia":
			if err := walkISOBoxes(file, payloadStart, boxEnd, kind, handlers, func(string) {}); err != nil {
				return err
			}
		case parent == "mdia" && kind == "hdlr":
			if boxEnd-payloadStart < 24 {
				return ErrInvalidProjectAsset
			}
			payload := make([]byte, 12)
			if _, err := file.ReadAt(payload, payloadStart); err != nil {
				return ErrInvalidProjectAsset
			}
			handler := string(payload[8:12])
			if handler == "soun" || handler == "vide" {
				handlers[handler] = true
			}
		}
		offset = boxEnd
	}
	return nil
}
func bytesContainsNUL(data []byte) bool {
	for _, b := range data {
		if b == 0 {
			return true
		}
	}
	return false
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

func (s *Service) ReconcileProjectAssets(ctx context.Context, db *sql.DB, logger *log.Logger) error {
	rows, err := db.QueryContext(ctx, `SELECT path FROM assets WHERE project_id IS NOT NULL`)
	if err != nil {
		return fmt.Errorf("list project assets: %w", err)
	}
	refs := map[string]bool{}
	for rows.Next() {
		var p string
		if err := rows.Scan(&p); err != nil {
			rows.Close()
			return err
		}
		abs, _ := filepath.Abs(p)
		refs[filepath.Clean(abs)] = true
	}
	if err := rows.Close(); err != nil {
		return err
	}
	root, err := filepath.Abs(filepath.Join(s.dataRoot, "projects"))
	if err != nil {
		return err
	}
	info, err := os.Stat(root)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	if !info.IsDir() {
		return fmt.Errorf("projects data root is not a directory")
	}
	var errs []error
	_ = filepath.WalkDir(root, func(path string, e os.DirEntry, walkErr error) error {
		if walkErr != nil {
			errs = append(errs, walkErr)
			return nil
		}
		if e.IsDir() {
			return nil
		}
		abs := filepath.Clean(path)
		if refs[abs] && !strings.HasPrefix(e.Name(), ".upload-") {
			return nil
		}
		if err := os.Remove(path); err != nil {
			errs = append(errs, err)
			if logger != nil {
				logger.Printf("project asset reconciliation: %v", err)
			}
		}
		return nil
	})
	return errors.Join(errs...)
}
