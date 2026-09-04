package assets

import (
	"bufio"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"image"
	_ "image/jpeg"
	_ "image/png"
	"io"
	"log"
	"math"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/google/uuid"
	"golang.org/x/image/webp"

	"video-production-console/internal/domain"
	"video-production-console/internal/spokenlines"
)

const MaxBackgroundSize int64 = 20 << 20
const MaxProjectAssetSize int64 = 500 << 20
const MaxTextAssetSize int64 = 5 << 20
const MaxAudioAssetSize int64 = 200 << 20

func MaxSizeForType(assetType domain.AssetType) int64 {
	switch assetType {
	case domain.AssetSourceScript, domain.AssetContinuousScript, domain.AssetSpokenScript, domain.AssetCaptionKeywords, domain.AssetSubtitleSRT, domain.AssetSubtitle, domain.AssetWordTiming:
		return MaxTextAssetSize
	case domain.AssetNarration, domain.AssetAudio:
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
	scriptHashPattern      = regexp.MustCompile(`^sha256:[0-9a-fA-F]{64}$`)
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
	validationType := assetType
	if assetType == domain.AssetSourceScript || assetType == domain.AssetTopicCard {
		validationType = domain.AssetContinuousScript
	}
	return s.saveProjectAsset(projectID, assetType, validationType, filename, reader)
}

// DeleteProjectData removes only the managed directory for one validated
// project UUID. It never accepts a broad path, a relative traversal, or the
// projects root itself.
func (s *Service) DeleteProjectData(projectID string) error {
	parsed, err := uuid.Parse(projectID)
	if err != nil {
		return ErrInvalidProjectAsset
	}
	root, err := filepath.Abs(filepath.Join(s.dataRoot, "projects"))
	if err != nil {
		return err
	}
	target, err := filepath.Abs(filepath.Join(root, parsed.String()))
	if err != nil {
		return err
	}
	rel, err := filepath.Rel(root, target)
	if err != nil || rel != parsed.String() || filepath.IsAbs(rel) {
		return fmt.Errorf("project deletion target is outside the managed project root")
	}
	return os.RemoveAll(target)
}

func (s *Service) saveProjectAsset(projectID string, targetType, validationType domain.AssetType, filename string, reader io.Reader) (saved SavedAsset, err error) {
	parsedID, parseErr := uuid.Parse(projectID)
	if parseErr != nil {
		return SavedAsset{}, ErrInvalidProjectAsset
	}
	ext := strings.ToLower(filepath.Ext(filename))
	allowed := map[domain.AssetType]map[string]bool{
		domain.AssetSourceScript: {".txt": true, ".md": true}, domain.AssetTopicCard: {".txt": true, ".md": true}, domain.AssetContinuousScript: {".txt": true, ".md": true}, domain.AssetSpokenScript: {".txt": true, ".md": true}, domain.AssetCaptionKeywords: {".json": true}, domain.AssetWordTiming: {".json": true},
		domain.AssetSubtitleSRT: {".srt": true}, domain.AssetSubtitle: {".srt": true}, domain.AssetNarration: {".mp3": true, ".wav": true, ".m4a": true}, domain.AssetAudio: {".mp3": true, ".wav": true, ".m4a": true},
		domain.AssetMixDraft: {".mp4": true}, domain.AssetFinalVideo: {".mp4": true},
	}
	if !allowed[validationType][ext] {
		return SavedAsset{}, ErrInvalidProjectAsset
	}
	maxSize := MaxSizeForType(validationType)
	if maxSize == 0 {
		return SavedAsset{}, ErrInvalidProjectAsset
	}
	directory := filepath.Join(s.dataRoot, "projects", parsedID.String(), string(targetType))
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
	mimeType, err := validateProjectFile(temporaryPath, validationType, ext)
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

func (s *Service) SaveTextVersion(projectID string, typ domain.AssetType, filename, text string) (SavedAsset, error) {
	switch typ {
	case domain.AssetSourceScript, domain.AssetTopicCard, domain.AssetContinuousScript, domain.AssetSpokenScript, domain.AssetSubtitleSRT:
	default:
		return SavedAsset{}, ErrInvalidProjectAsset
	}
	// Source scripts and topic cards use the same managed text rules as scripts.
	validationType := typ
	if typ == domain.AssetSourceScript || typ == domain.AssetTopicCard {
		validationType = domain.AssetContinuousScript
	}
	return s.saveProjectAsset(projectID, typ, validationType, filename, strings.NewReader(text))
}

func (s *Service) ImportFile(projectID string, typ domain.AssetType, sourcePath string) (SavedAsset, error) {
	info, err := os.Lstat(sourcePath)
	if err != nil {
		return SavedAsset{}, err
	}
	if !info.Mode().IsRegular() {
		return SavedAsset{}, ErrInvalidProjectAsset
	}
	if s.beforeImportOpen != nil {
		s.beforeImportOpen(sourcePath)
	}
	file, err := openPathNoFollow(sourcePath, "", false)
	if err != nil {
		return SavedAsset{}, err
	}
	defer file.Close()
	return s.SaveProjectAsset(projectID, typ, filepath.Base(sourcePath), file)
}

func (s *Service) HashDirectory(path string) (string, int64, error) {
	root, err := filepath.Abs(s.dataRoot)
	if err != nil {
		return "", 0, ErrAssetPathInvalid
	}
	directory, err := filepath.Abs(path)
	if err != nil || !pathInside(root, directory) {
		return "", 0, ErrAssetPathInvalid
	}
	rootResolved, err := filepath.EvalSymlinks(root)
	if err != nil {
		return "", 0, ErrAssetPathInvalid
	}
	directoryResolved, err := filepath.EvalSymlinks(directory)
	if err != nil || filepath.Clean(directoryResolved) != filepath.Clean(directory) || !pathInside(rootResolved, directoryResolved) {
		return "", 0, ErrAssetPathInvalid
	}
	info, err := os.Stat(directory)
	if err != nil {
		return "", 0, err
	}
	if !info.IsDir() {
		return "", 0, ErrInvalidProjectAsset
	}
	directoryHandle, err := openPathNoFollow(directory, root, true)
	if err != nil {
		return "", 0, err
	}
	defer directoryHandle.Close()
	directoryIdentity, err := directoryHandle.Stat()
	if err != nil {
		return "", 0, err
	}
	type entryHash struct {
		relative string
		digest   [sha256.Size]byte
		size     int64
	}
	entries := []entryHash{}
	err = filepath.WalkDir(directory, func(current string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		currentDirectory, identityErr := os.Stat(directory)
		if identityErr != nil || !os.SameFile(directoryIdentity, currentDirectory) {
			return ErrAssetPathInvalid
		}
		if current == directory {
			return nil
		}
		if entry.Type()&os.ModeSymlink != 0 {
			return ErrAssetPathInvalid
		}
		resolved, resolveErr := filepath.EvalSymlinks(current)
		if resolveErr != nil || filepath.Clean(resolved) != filepath.Clean(current) || !pathInside(directory, resolved) {
			return ErrAssetPathInvalid
		}
		if entry.IsDir() {
			return nil
		}
		entryInfo, statErr := entry.Info()
		if statErr != nil {
			return statErr
		}
		if !entryInfo.Mode().IsRegular() {
			return ErrAssetPathInvalid
		}
		if s.beforeHashFileOpen != nil {
			s.beforeHashFileOpen(current)
		}
		file, openErr := openPathNoFollow(current, directory, false)
		if openErr != nil {
			return openErr
		}
		digest := sha256.New()
		n, copyErr := io.Copy(digest, file)
		closeErr := file.Close()
		if copyErr != nil {
			return copyErr
		}
		if closeErr != nil {
			return closeErr
		}
		relative, relErr := filepath.Rel(directory, current)
		if relErr != nil {
			return relErr
		}
		var sum [sha256.Size]byte
		copy(sum[:], digest.Sum(nil))
		entries = append(entries, entryHash{relative: filepath.ToSlash(relative), digest: sum, size: n})
		return nil
	})
	if err != nil {
		return "", 0, err
	}
	currentDirectory, err := os.Stat(directory)
	if err != nil || !os.SameFile(directoryIdentity, currentDirectory) {
		return "", 0, ErrAssetPathInvalid
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].relative < entries[j].relative })
	digest := sha256.New()
	var total int64
	var boundary [8]byte
	for _, entry := range entries {
		binary.BigEndian.PutUint64(boundary[:], uint64(len(entry.relative)))
		_, _ = digest.Write(boundary[:])
		_, _ = io.WriteString(digest, entry.relative)
		binary.BigEndian.PutUint64(boundary[:], uint64(len(entry.digest)))
		_, _ = digest.Write(boundary[:])
		_, _ = digest.Write(entry.digest[:])
		total += entry.size
	}
	return hex.EncodeToString(digest.Sum(nil)), total, nil
}

func pathInside(root, path string) bool {
	relative, err := filepath.Rel(root, path)
	return err == nil && relative != ".." && !strings.HasPrefix(relative, ".."+string(os.PathSeparator))
}

func validateProjectFile(path string, assetType domain.AssetType, ext string) (string, error) {
	if ext == ".m4a" || ext == ".mp4" {
		handlers, err := isoBMFFHandlers(path)
		if err != nil {
			return "", nil
		}
		if ext == ".m4a" && (assetType == domain.AssetNarration || assetType == domain.AssetAudio) && handlers["soun"] && !handlers["vide"] {
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
	if assetType == domain.AssetWordTiming {
		type wordTimingWord struct {
			Text       string  `json:"text"`
			Start      float64 `json:"start_time"`
			End        float64 `json:"end_time"`
			Confidence float64 `json:"confidence"`
		}
		var envelope struct {
			SchemaVersion int              `json:"schema_version"`
			Script        string           `json:"script"`
			Provider      string           `json:"provider"`
			ScriptHash    string           `json:"script_hash"`
			Hash          string           `json:"hash,omitempty"`
			Duration      float64          `json:"duration"`
			Words         []wordTimingWord `json:"words"`
		}
		dec := json.NewDecoder(io.LimitReader(file, MaxTextAssetSize+1))
		dec.DisallowUnknownFields()
		if err := dec.Decode(&envelope); err != nil || envelope.SchemaVersion != 1 || strings.TrimSpace(envelope.Script) == "" || strings.TrimSpace(envelope.Provider) == "" || len(envelope.Words) == 0 || !scriptHashPattern.MatchString(envelope.ScriptHash) || math.IsNaN(envelope.Duration) || math.IsInf(envelope.Duration, 0) || envelope.Duration <= 0 {
			return "", nil
		}
		scriptDigest := sha256.Sum256([]byte(envelope.Script))
		if envelope.ScriptHash != "sha256:"+hex.EncodeToString(scriptDigest[:]) {
			return "", nil
		}
		var extra any
		if dec.Decode(&extra) != io.EOF {
			return "", nil
		}
		prev := -1.0
		for _, word := range envelope.Words {
			if strings.TrimSpace(word.Text) == "" || math.IsNaN(word.Start) || math.IsInf(word.Start, 0) || math.IsNaN(word.End) || math.IsInf(word.End, 0) || word.Start < 0 || word.End <= word.Start || word.Start < prev {
				return "", nil
			}
			if math.IsNaN(word.Confidence) || math.IsInf(word.Confidence, 0) {
				return "", nil
			}
			prev = word.End
		}
		if math.Abs(envelope.Duration-prev) > 1e-9 {
			return "", nil
		}
		return "application/json", nil
	}
	if assetType == domain.AssetCaptionKeywords {
		payload, readErr := io.ReadAll(io.LimitReader(file, MaxTextAssetSize+1))
		if readErr != nil || int64(len(payload)) > MaxTextAssetSize {
			return "", nil
		}
		if _, err := spokenlines.ParseKeywordDoc(payload); err != nil {
			return "", nil
		}
		return "application/json; charset=utf-8", nil
	}
	if assetType == domain.AssetSourceScript || assetType == domain.AssetTopicCard || assetType == domain.AssetContinuousScript || assetType == domain.AssetSpokenScript || assetType == domain.AssetSubtitleSRT || assetType == domain.AssetSubtitle {
		reader := bufio.NewReader(file)
		hasContent := false
		for {
			r, size, readErr := reader.ReadRune()
			if readErr == io.EOF {
				break
			}
			if readErr != nil || r == '\x00' || r == utf8.RuneError && size == 1 {
				return "", nil
			}
			if !unicode.IsSpace(r) {
				hasContent = true
			}
		}
		if !hasContent {
			return "", nil
		}
		if ext == ".md" {
			return "text/markdown; charset=utf-8", nil
		}
		if ext == ".srt" {
			return "application/x-subrip; charset=utf-8", nil
		}
		return "text/plain; charset=utf-8", nil
	}
	if ext == ".wav" && (assetType == domain.AssetNarration || assetType == domain.AssetAudio) {
		ok, err := validWAVFile(file)
		if ok {
			return "audio/wav", nil
		}
		return "", err
	}
	if ext == ".mp3" && (assetType == domain.AssetNarration || assetType == domain.AssetAudio) {
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
	dataRoot           string
	beforeImportOpen   func(string)
	beforeHashFileOpen func(string)
}

func NewService(dataRoot string) *Service {
	return &Service{dataRoot: filepath.Clean(dataRoot)}
}

func (s *Service) ProjectDir(projectID string) (string, error) {
	parsed, err := uuid.Parse(projectID)
	if err != nil {
		return "", ErrInvalidProjectAsset
	}
	return filepath.Join(s.dataRoot, "projects", parsed.String()), nil
}

var ErrAssetPathInvalid = errors.New("asset path is outside the data root")

// OpenAsset opens a database-referenced asset only when its resolved path is
// inside the service data root and is not a symlink/reparse alias. The caller
// owns the returned file and must close it.
func (s *Service) OpenAsset(asset domain.Asset) (*os.File, os.FileInfo, error) {
	if _, err := uuid.Parse(asset.ID); err != nil || strings.TrimSpace(asset.Path) == "" {
		return nil, nil, ErrAssetPathInvalid
	}
	root, err := filepath.Abs(s.dataRoot)
	if err != nil {
		return nil, nil, ErrAssetPathInvalid
	}
	path, err := resolveStoredAssetPath(s.dataRoot, asset.Path)
	if err != nil {
		return nil, nil, ErrAssetPathInvalid
	}
	rel, err := filepath.Rel(root, path)
	if err != nil || rel == "." || rel == ".." || strings.HasPrefix(rel, ".."+string(os.PathSeparator)) {
		return nil, nil, ErrAssetPathInvalid
	}
	f, err := openPathNoFollow(path, root, false)
	if err != nil {
		return nil, nil, err
	}
	info, err := f.Stat()
	if err != nil || !info.Mode().IsRegular() {
		_ = f.Close()
		if err != nil {
			return nil, nil, err
		}
		return nil, nil, ErrAssetPathInvalid
	}
	return f, info, nil
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
	rows, err := db.QueryContext(ctx, `SELECT path FROM asset_versions WHERE type = 'account_background'`)
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
		absolute, err := resolveStoredAssetPath(s.dataRoot, path)
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

// resolveStoredAssetPath resolves paths written by both the current and
// legacy console versions. Current uploads use absolute paths; older records
// may contain either "accounts\\..." relative to dataRoot or
// "video-console-data\\accounts\\..." relative to dataRoot's parent.
func resolveStoredAssetPath(dataRoot, storedPath string) (string, error) {
	storedPath = strings.TrimSpace(storedPath)
	if storedPath == "" {
		return "", nil
	}
	if filepath.IsAbs(storedPath) {
		return filepath.Abs(filepath.Clean(storedPath))
	}
	root, err := filepath.Abs(filepath.Clean(dataRoot))
	if err != nil {
		return "", err
	}
	cleaned := filepath.Clean(storedPath)
	parts := strings.Split(cleaned, string(filepath.Separator))
	if len(parts) > 0 && strings.EqualFold(parts[0], filepath.Base(root)) {
		return filepath.Abs(filepath.Join(filepath.Dir(root), cleaned))
	}
	return filepath.Abs(filepath.Join(root, cleaned))
}

func (s *Service) ReconcileProjectAssets(ctx context.Context, db *sql.DB, logger *log.Logger) error {
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
	rows, err := db.QueryContext(ctx, `SELECT path,storage_kind,project_id,type FROM asset_versions WHERE project_id IS NOT NULL`)
	if err != nil {
		return fmt.Errorf("list project assets: %w", err)
	}
	refs := map[string]bool{}
	protectedDirectories := map[string]bool{}
	for rows.Next() {
		var p, projectID string
		var storageKind domain.StorageKind
		var typ domain.AssetType
		if err := rows.Scan(&p, &storageKind, &projectID, &typ); err != nil {
			rows.Close()
			return err
		}
		abs, valid := validManagedProjectAssetPath(root, projectID, typ, p, storageKind == domain.StorageDirectory)
		if !valid {
			continue
		}
		if storageKind == domain.StorageDirectory {
			protectedDirectories[abs] = true
		} else {
			refs[abs] = true
		}
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return err
	}
	if err := rows.Close(); err != nil {
		return err
	}
	taskRows, err := db.QueryContext(ctx, `SELECT id,project_id FROM codex_tasks`)
	if err != nil {
		return fmt.Errorf("list managed task directories: %w", err)
	}
	for taskRows.Next() {
		var taskID string
		var projectID sql.NullString
		if err := taskRows.Scan(&taskID, &projectID); err != nil {
			taskRows.Close()
			return fmt.Errorf("scan managed task directory: %w", err)
		}
		ownerID := taskID
		if projectID.Valid && strings.TrimSpace(projectID.String) != "" {
			ownerID = projectID.String
		}
		if taskDir, valid := validManagedTaskDirectory(root, ownerID, taskID); valid {
			protectedDirectories[taskDir] = true
		}
	}
	if err := taskRows.Err(); err != nil {
		taskRows.Close()
		return fmt.Errorf("iterate managed task directories: %w", err)
	}
	if err := taskRows.Close(); err != nil {
		return fmt.Errorf("close managed task directories: %w", err)
	}
	var errs []error
	_ = filepath.WalkDir(root, func(path string, e os.DirEntry, walkErr error) error {
		if walkErr != nil {
			errs = append(errs, walkErr)
			return nil
		}
		if e.IsDir() {
			if path != root && protectedDirectories[filepath.Clean(path)] {
				return filepath.SkipDir
			}
			return nil
		}
		abs := filepath.Clean(path)
		// projects/{id}/publishing_package.json 是导入发布包的旁挂文件，
		// 不入资产表；混剪板题靠它取主/副标题，不能当孤儿清掉。
		if e.Name() == "publishing_package.json" {
			parent := filepath.Dir(abs)
			if filepath.Clean(filepath.Dir(parent)) == root {
				if _, parseErr := uuid.Parse(filepath.Base(parent)); parseErr == nil {
					return nil
				}
			}
		}
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

func validManagedTaskDirectory(root, ownerID, taskID string) (string, bool) {
	if _, err := uuid.Parse(ownerID); err != nil {
		return "", false
	}
	if _, err := uuid.Parse(taskID); err != nil {
		return "", false
	}
	taskDir := filepath.Clean(filepath.Join(root, ownerID, "tasks", taskID))
	if !pathInside(filepath.Join(root, ownerID, "tasks"), taskDir) {
		return "", false
	}
	resolved, err := filepath.EvalSymlinks(taskDir)
	if err != nil || filepath.Clean(resolved) != taskDir {
		return "", false
	}
	info, err := os.Stat(taskDir)
	if err != nil || !info.IsDir() {
		return "", false
	}
	return taskDir, true
}

func validManagedProjectAssetPath(root, projectID string, typ domain.AssetType, path string, directory bool) (string, bool) {
	if _, err := uuid.Parse(projectID); err != nil || strings.TrimSpace(path) == "" {
		return "", false
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return "", false
	}
	abs = filepath.Clean(abs)
	projectRoot := filepath.Join(root, projectID)
	requiredRoot := projectRoot
	if directory {
		requiredRoot = filepath.Join(projectRoot, string(typ))
	}
	if !pathInside(requiredRoot, abs) {
		return "", false
	}
	if directory {
		relative, err := filepath.Rel(requiredRoot, abs)
		if err != nil || relative == "." {
			return "", false
		}
	}
	resolved, err := filepath.EvalSymlinks(abs)
	if err != nil || filepath.Clean(resolved) != abs {
		return "", false
	}
	info, err := os.Stat(abs)
	if err != nil || directory != info.IsDir() || !directory && !info.Mode().IsRegular() {
		return "", false
	}
	return abs, true
}
