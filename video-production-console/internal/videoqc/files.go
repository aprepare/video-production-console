package videoqc

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"image/jpeg"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"unicode"

	"github.com/google/uuid"
	"video-production-console/internal/store"
)

const MaxUploadBytes = 300 << 20
const maxJPEGBytes = 4 << 20

var idPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_-]{0,127}$`)

func validID(id string) bool         { return idPattern.MatchString(id) }
func frameFilename(index int) string { return fmt.Sprintf("frame_%02d.jpg", index) }

// All generated files, temporary uploads and removals stay under this Root.
func openFiles(dataRoot string, create bool) (*os.Root, error) {
	if !filepath.IsAbs(dataRoot) {
		return nil, ErrUnsafePath
	}
	dr, err := os.OpenRoot(dataRoot)
	if err != nil {
		return nil, errors.New("审核存储目录不可用")
	}
	defer dr.Close()
	if create {
		if err := dr.MkdirAll("video-qc", 0700); err != nil {
			return nil, ErrUnsafePath
		}
	}
	info, err := dr.Lstat("video-qc")
	if err != nil {
		return nil, err
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
		return nil, ErrUnsafePath
	}
	root, err := dr.OpenRoot("video-qc")
	if err != nil {
		return nil, ErrUnsafePath
	}
	return root, nil
}

func openDirectory(root *os.Root, id string) (*os.Root, error) {
	if !validID(id) {
		return nil, ErrUnsafePath
	}
	info, err := root.Lstat(id)
	if err != nil {
		return nil, ErrMediaNotFound
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
		return nil, ErrUnsafePath
	}
	dir, err := root.OpenRoot(id)
	if err != nil {
		return nil, ErrUnsafePath
	}
	return dir, nil
}

func sanitizeTitle(value string) string {
	value = filepath.Base(strings.ReplaceAll(value, `\`, "/"))
	runes := []rune{}
	for _, r := range value {
		if !unicode.IsControl(r) && !unicode.Is(unicode.Cf, r) {
			runes = append(runes, r)
		}
		if len(runes) == 120 {
			break
		}
	}
	return strings.TrimSpace(string(runes))
}

func uploadExtension(filename string, header []byte) (string, error) {
	ext := strings.ToLower(filepath.Ext(strings.ReplaceAll(filename, `\`, "/")))
	switch ext {
	case ".mp4", ".mov":
		if len(header) < 16 {
			return "", ErrUnsupportedFormat
		}
		size := binary.BigEndian.Uint32(header[:4])
		atom := string(header[4:8])
		if atom == "ftyp" && size >= 16 {
			return ext, nil
		}
		// Older QuickTime containers can start with a movie/media atom.
		if ext == ".mov" && (atom == "moov" || atom == "mdat" || atom == "wide") && (size == 1 || size >= 8) {
			return ext, nil
		}
	case ".webm", ".mkv":
		if len(header) >= 4 && bytes.Equal(header[:4], []byte{0x1a, 0x45, 0xdf, 0xa3}) {
			if ext == ".webm" && bytes.Contains(header, []byte("webm")) {
				return ext, nil
			}
			if ext == ".mkv" && bytes.Contains(header, []byte("matroska")) {
				return ext, nil
			}
		}
	}
	return "", ErrUnsupportedFormat
}

type contextReader struct {
	ctx    context.Context
	reader io.Reader
}

func (r contextReader) Read(p []byte) (int, error) {
	if err := r.ctx.Err(); err != nil {
		return 0, err
	}
	return r.reader.Read(p)
}

func saveUpload(ctx context.Context, root *os.Root, id, filename string, reader io.Reader) (string, error) {
	if reader == nil {
		return "", ErrInvalidInput
	}
	limited := io.LimitReader(contextReader{ctx, reader}, MaxUploadBytes+1)
	header := make([]byte, 4096)
	n, err := io.ReadFull(limited, header)
	if err != nil && err != io.EOF && err != io.ErrUnexpectedEOF {
		return "", err
	}
	header = header[:n]
	ext, err := uploadExtension(filename, header)
	if err != nil {
		return "", err
	}
	name := ".upload-" + uuid.NewString()
	f, err := root.OpenFile(name, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return "", errors.New("上传临时文件创建失败")
	}
	defer root.Remove(name)
	count, copyErr := io.Copy(f, io.MultiReader(bytes.NewReader(header), limited))
	closeErr := f.Close()
	if count > MaxUploadBytes {
		return "", ErrTooLarge
	}
	if copyErr != nil {
		return "", copyErr
	}
	if closeErr != nil {
		return "", errors.New("上传文件保存失败")
	}
	if err := ctx.Err(); err != nil {
		return "", err
	}
	if err := root.Mkdir(id, 0700); err != nil {
		return "", ErrUnsafePath
	}
	if err := root.Rename(name, filepath.Join(id, "source"+ext)); err != nil {
		_ = root.RemoveAll(id)
		return "", errors.New("上传文件移入审核目录失败")
	}
	return filepath.Join(root.Name(), id, "source"+ext), nil
}

func validJPEG(raw []byte) bool {
	if len(raw) == 0 || len(raw) > maxJPEGBytes {
		return false
	}
	cfg, err := jpeg.DecodeConfig(bytes.NewReader(raw))
	return err == nil && cfg.Width > 0 && cfg.Height > 0 && cfg.Width <= 768 && cfg.Height <= 768
}

func writeExclusive(dir *os.Root, name string, data []byte) error {
	f, err := dir.OpenFile(name, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return errors.New("审核文件创建失败")
	}
	_, writeErr := f.Write(data)
	return errors.Join(writeErr, f.Close())
}

func readBounded(dir *os.Root, name string, limit int64) ([]byte, error) {
	f, err := openRegular(dir, name)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	raw, err := io.ReadAll(io.LimitReader(f, limit+1))
	if err != nil || int64(len(raw)) > limit {
		return nil, ErrMediaNotFound
	}
	return raw, nil
}

func openRegular(dir *os.Root, name string) (*os.File, error) {
	info, err := dir.Lstat(name)
	if err != nil {
		return nil, ErrMediaNotFound
	}
	if !info.Mode().IsRegular() {
		return nil, ErrUnsafePath
	}
	f, err := dir.Open(name)
	if err != nil {
		return nil, ErrUnsafePath
	}
	info, err = f.Stat()
	if err != nil || !info.Mode().IsRegular() {
		f.Close()
		return nil, ErrMediaNotFound
	}
	return f, nil
}

func loadFrames(dir *os.Root, a store.VideoQCCheck) ([]Frame, error) {
	raw, err := readBounded(dir, "frames.json", 64<<10)
	if err != nil {
		return nil, err
	}
	var frames []Frame
	if json.Unmarshal(raw, &frames) != nil || len(frames) != a.FrameCount || !validFrames(frames, a.DurationS) {
		return nil, errors.New("审核帧清单无效")
	}
	for i := range frames {
		frames[i].URL = fmt.Sprintf("/api/video-qc/%s/frames/%d", a.ID, frames[i].Index)
	}
	return frames, nil
}
