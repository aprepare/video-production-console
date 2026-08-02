package assets

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"image"
	"image/color"
	"image/jpeg"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const testAccountID = "b605e068-6ada-4e34-a966-dc730746d49f"

func TestSaveAccountBackgroundUsesDetectedPNGAndSHA256(t *testing.T) {
	root := t.TempDir()
	data := encodeImage(t, "png")
	got, err := NewService(root).SaveAccountBackground(testAccountID, "misleading.jpg", bytes.NewReader(data))
	if err != nil {
		t.Fatalf("SaveAccountBackground() error = %v", err)
	}
	if got.MIMEType != "image/png" {
		t.Errorf("MIMEType = %q, want image/png", got.MIMEType)
	}
	if got.Size != int64(len(data)) {
		t.Errorf("Size = %d, want %d", got.Size, len(data))
	}
	wantHash := sha256.Sum256(data)
	if got.SHA256 != hex.EncodeToString(wantHash[:]) {
		t.Errorf("SHA256 = %q, want %q", got.SHA256, hex.EncodeToString(wantHash[:]))
	}
	if filepath.Ext(got.Path) != ".png" {
		t.Errorf("extension = %q, want .png", filepath.Ext(got.Path))
	}
	wantDir := filepath.Join(root, "accounts", testAccountID, "background")
	if filepath.Dir(got.Path) != wantDir {
		t.Errorf("directory = %q, want %q", filepath.Dir(got.Path), wantDir)
	}
	stored, err := os.ReadFile(got.Path)
	if err != nil {
		t.Fatalf("read stored file: %v", err)
	}
	if !bytes.Equal(stored, data) {
		t.Error("stored file differs from upload")
	}
}

func TestSaveAccountBackgroundAcceptsJPEG(t *testing.T) {
	data := encodeImage(t, "jpeg")
	got, err := NewService(t.TempDir()).SaveAccountBackground(testAccountID, "background.png", bytes.NewReader(data))
	if err != nil {
		t.Fatalf("SaveAccountBackground() error = %v", err)
	}
	if got.MIMEType != "image/jpeg" || filepath.Ext(got.Path) != ".jpg" {
		t.Errorf("saved as MIME %q path %q, want image/jpeg and .jpg", got.MIMEType, got.Path)
	}
}

func TestSaveAccountBackgroundRejectsInvalidInputsWithoutArtifacts(t *testing.T) {
	tests := []struct {
		name      string
		accountID string
		data      []byte
	}{
		{name: "text disguised as PNG", accountID: testAccountID, data: []byte("this is not a PNG")},
		{name: "path traversal account ID", accountID: "../outside", data: encodeImage(t, "png")},
		{name: "oversized", accountID: testAccountID, data: bytes.Repeat([]byte{'x'}, int(MaxBackgroundSize+1))},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			root := t.TempDir()
			_, err := NewService(root).SaveAccountBackground(tt.accountID, "background.png", bytes.NewReader(tt.data))
			if err == nil {
				t.Fatal("SaveAccountBackground() succeeded, want error")
			}
			if files := allFiles(t, root); len(files) != 0 {
				t.Fatalf("files remain after failed upload: %v", files)
			}
		})
	}
}

func encodeImage(t *testing.T, format string) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, 2, 2))
	img.Set(0, 0, color.RGBA{R: 0xe0, G: 0x80, B: 0x20, A: 0xff})
	var buffer bytes.Buffer
	var err error
	switch format {
	case "png":
		err = png.Encode(&buffer, img)
	case "jpeg":
		err = jpeg.Encode(&buffer, img, nil)
	default:
		t.Fatalf("unsupported test image format %q", format)
	}
	if err != nil {
		t.Fatalf("encode test image: %v", err)
	}
	return buffer.Bytes()
}

func allFiles(t *testing.T, root string) []string {
	t.Helper()
	var files []string
	err := filepath.WalkDir(root, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !entry.IsDir() {
			files = append(files, strings.TrimPrefix(path, root))
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk data root: %v", err)
	}
	return files
}
