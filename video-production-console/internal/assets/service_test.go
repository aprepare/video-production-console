package assets

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"hash/crc32"
	"image"
	"image/color"
	"image/jpeg"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"video-production-console/internal/domain"
	"video-production-console/internal/store"
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
		{name: "malformed WebP header", accountID: testAccountID, data: malformedWebP()},
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

func TestSaveAccountBackgroundRejectsTruncatedAndOversizedDimensions(t *testing.T) {
	valid := encodeImage(t, "png")
	tests := []struct {
		name    string
		data    []byte
		wantErr error
	}{
		{name: "truncated after header", data: valid[:len(valid)-8], wantErr: ErrInvalidImage},
		{name: "width over limit", data: encodeSizedPNG(t, 8193, 1), wantErr: ErrImageDimensions},
		{name: "pixel count over limit", data: encodeSizedPNG(t, 7000, 7000), wantErr: ErrImageDimensions},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			root := t.TempDir()
			_, err := NewService(root).SaveAccountBackground(testAccountID, "image.png", bytes.NewReader(tt.data))
			if !errors.Is(err, tt.wantErr) {
				t.Fatalf("error = %v, want %v", err, tt.wantErr)
			}
			if files := allFiles(t, root); len(files) != 0 {
				t.Fatalf("files remain after rejected image: %v", files)
			}
		})
	}
}

func TestReconcileAccountBackgroundsRemovesOnlyOrphansAndTemps(t *testing.T) {
	root := t.TempDir()
	db, err := store.Open(filepath.Join(root, "console.db"))
	if err != nil {
		t.Fatalf("open database: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	now := time.Now().UTC()
	if _, err := db.Exec(`INSERT INTO accounts (id, name, color, status, created_at, updated_at)
        VALUES (?, 'account', '#fff', 'active', ?, ?)`, testAccountID, now, now); err != nil {
		t.Fatalf("insert account: %v", err)
	}
	directory := filepath.Join(root, "accounts", testAccountID, "background")
	if err := os.MkdirAll(directory, 0o755); err != nil {
		t.Fatalf("mkdir background: %v", err)
	}
	referencedOld := filepath.Join(directory, "old.png")
	referencedCurrent := filepath.Join(directory, "current.png")
	orphan := filepath.Join(directory, "orphan.png")
	temporary := filepath.Join(directory, ".upload-crash")
	outside := filepath.Join(root, "outside.png")
	for _, path := range []string{referencedOld, referencedCurrent, orphan, temporary, outside} {
		if err := os.WriteFile(path, []byte("fixture"), 0o644); err != nil {
			t.Fatalf("write fixture %q: %v", path, err)
		}
	}
	for version, path := range []string{referencedOld, referencedCurrent} {
		assetID := []string{"6dc72973-372d-42f2-9102-a40133829d40", "8e95eab4-520f-4296-b4d0-97882125f063"}[version]
		if _, err := db.Exec(`INSERT INTO assets
            (id, account_id, type, path, filename, mime_type, size, sha256, version, status, created_at)
            VALUES (?, ?, 'account_background', ?, 'file.png', 'image/png', 1, 'hash', ?, 'active', ?)`,
			assetID, testAccountID, path, version+1, now); err != nil {
			t.Fatalf("insert asset: %v", err)
		}
	}
	if err := NewService(root).ReconcileAccountBackgrounds(context.Background(), db, nil); err != nil {
		t.Fatalf("ReconcileAccountBackgrounds() error = %v", err)
	}
	for _, path := range []string{referencedOld, referencedCurrent, outside} {
		if _, err := os.Stat(path); err != nil {
			t.Errorf("preserved file %q: %v", path, err)
		}
	}
	for _, path := range []string{orphan, temporary} {
		if _, err := os.Stat(path); !os.IsNotExist(err) {
			t.Errorf("orphan %q still exists, stat error = %v", path, err)
		}
	}
}

func TestReconcileAccountBackgroundsReportsInvalidAccountsRoot(t *testing.T) {
	root := t.TempDir()
	db, err := store.Open(filepath.Join(root, "console.db"))
	if err != nil {
		t.Fatalf("open database: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := os.WriteFile(filepath.Join(root, "accounts"), []byte("not a directory"), 0o644); err != nil {
		t.Fatalf("write invalid accounts root: %v", err)
	}
	if err := NewService(root).ReconcileAccountBackgrounds(context.Background(), db, nil); err == nil {
		t.Fatal("ReconcileAccountBackgrounds() succeeded for non-directory accounts root")
	}
}

func TestReconcileProjectAssetsKeepsAllVersionsAndRemovesOrphans(t *testing.T) {
	root := t.TempDir()
	db, err := store.Open(filepath.Join(t.TempDir(), "db.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	now := time.Now().UTC()
	aid := uuid.NewString()
	pid := uuid.NewString()
	_, _ = db.Exec(`INSERT INTO accounts(id,name,color,status,created_at,updated_at) VALUES(?,?,'#fff','active',?,?)`, aid, "a", now, now)
	_, _ = db.Exec(`INSERT INTO projects(id,account_id,title,stage,created_at,updated_at) VALUES(?,?,'p','topic',?,?)`, pid, aid, now, now)
	dir := filepath.Join(root, "projects", pid, "audio")
	_ = os.MkdirAll(dir, 0755)
	keep1 := filepath.Join(dir, "v1.mp3")
	keep2 := filepath.Join(dir, "v2.mp3")
	orphan := filepath.Join(dir, "orphan.mp3")
	temp := filepath.Join(dir, ".upload-x")
	for _, p := range []string{keep1, keep2, orphan, temp} {
		_ = os.WriteFile(p, []byte("x"), 0600)
	}
	for i, p := range []string{keep1, keep2} {
		_, err = db.Exec(`INSERT INTO assets(id,project_id,account_id,type,path,filename,mime_type,size,sha256,version,status,created_at) VALUES(?,?,?,?,?,'x','audio/mpeg',1,'x',?,'active',?)`, uuid.NewString(), pid, aid, "audio", p, i+1, now)
		if err != nil {
			t.Fatal(err)
		}
	}
	if err := NewService(root).ReconcileProjectAssets(context.Background(), db, nil); err != nil {
		t.Fatal(err)
	}
	for _, p := range []string{keep1, keep2} {
		if _, err := os.Stat(p); err != nil {
			t.Fatalf("removed referenced %s", p)
		}
	}
	for _, p := range []string{orphan, temp} {
		if _, err := os.Stat(p); !os.IsNotExist(err) {
			t.Fatalf("kept orphan %s", p)
		}
	}
}

func TestSaveProjectAssetValidatesContentAndUsesControlledPath(t *testing.T) {
	root := t.TempDir()
	projectID := "f02addf5-275c-4456-a51f-3ebeb9c730ef"
	svc := NewService(root)
	text, err := svc.SaveProjectAsset(projectID, domain.AssetContinuousScript, "script.md", bytes.NewReader([]byte("# 文案\n")))
	if err != nil {
		t.Fatalf("text upload: %v", err)
	}
	if text.MIMEType != "text/markdown; charset=utf-8" {
		t.Fatalf("mime = %q", text.MIMEType)
	}
	wantDir := filepath.Join(root, "projects", projectID, string(domain.AssetContinuousScript))
	if filepath.Dir(text.Path) != wantDir {
		t.Fatalf("path = %q, want directory %q", text.Path, wantDir)
	}
	if _, err := svc.SaveProjectAsset(projectID, domain.AssetSubtitle, "bad.srt", bytes.NewReader([]byte{0xff, 0xfe})); !errors.Is(err, ErrInvalidProjectAsset) {
		t.Fatalf("invalid UTF-8 error = %v", err)
	}
	if _, err := svc.SaveProjectAsset(projectID, domain.AssetAudio, "fake.mp3", bytes.NewReader([]byte("not mp3"))); !errors.Is(err, ErrInvalidProjectAsset) {
		t.Fatalf("fake MP3 error = %v", err)
	}
	if _, err := svc.SaveProjectAsset(projectID, domain.AssetFinalVideo, "video.txt", bytes.NewReader(minimalMP4())); !errors.Is(err, ErrInvalidProjectAsset) {
		t.Fatalf("wrong extension error = %v", err)
	}
}

func minimalMP4() []byte {
	return isoFile("vide")
}

func TestSaveProjectAssetParsesISOBaseMediaHandlers(t *testing.T) {
	svc := NewService(t.TempDir())
	id := uuid.NewString()
	for _, tt := range []struct {
		name     string
		typ      domain.AssetType
		filename string
		data     []byte
		ok       bool
	}{
		{"audio m4a", domain.AssetAudio, "voice.m4a", isoFile("soun"), true},
		{"video mp4", domain.AssetFinalVideo, "video.mp4", isoFile("vide"), true},
		{"av mp4", domain.AssetMixDraft, "mix.mp4", isoFile("soun", "vide"), true},
		{"video disguised m4a", domain.AssetAudio, "fake.m4a", isoFile("vide"), false},
		{"av disguised m4a", domain.AssetAudio, "fake-av.m4a", isoFile("soun", "vide"), false},
		{"audio disguised mp4", domain.AssetFinalVideo, "fake.mp4", isoFile("soun"), false},
		{"truncated box", domain.AssetFinalVideo, "bad.mp4", append(isoFile("vide"), 0, 0, 0, 20, 'm', 'o', 'o', 'v'), false},
	} {
		t.Run(tt.name, func(t *testing.T) {
			saved, err := svc.SaveProjectAsset(id, tt.typ, tt.filename, bytes.NewReader(tt.data))
			if tt.ok && err != nil {
				t.Fatalf("error=%v", err)
			}
			if !tt.ok && !errors.Is(err, ErrInvalidProjectAsset) {
				t.Fatalf("error=%v", err)
			}
			if saved.Path != "" {
				_ = os.Remove(saved.Path)
			}
		})
	}
}

func TestProjectAssetSizeLimitsByType(t *testing.T) {
	for _, tt := range []struct {
		typ  domain.AssetType
		want int64
	}{{domain.AssetContinuousScript, 5 << 20}, {domain.AssetSubtitle, 5 << 20}, {domain.AssetAudio, 200 << 20}, {domain.AssetFinalVideo, 500 << 20}} {
		if got := MaxSizeForType(tt.typ); got != tt.want {
			t.Errorf("MaxSizeForType(%s)=%d want %d", tt.typ, got, tt.want)
		}
	}
}

func TestAudioStructureValidation(t *testing.T) {
	svc := NewService(t.TempDir())
	id := uuid.NewString()
	for _, tt := range []struct {
		name, file string
		data       []byte
		ok         bool
	}{
		{"valid wav", "voice.wav", validWAV(), true}, {"truncated wav", "voice.wav", validWAV()[:20], false}, {"fake riff", "voice.wav", []byte("RIFF\x20\x00\x00\x00WAVE"), false},
		{"valid mp3", "voice.mp3", validMP3(), true}, {"one mp3 frame", "voice.mp3", validMP3()[:417], false}, {"fake id3", "voice.mp3", []byte("ID3\x04\x00\x00\x00\x00\x00\x00fake"), false},
	} {
		t.Run(tt.name, func(t *testing.T) {
			_, err := svc.SaveProjectAsset(id, domain.AssetAudio, tt.file, bytes.NewReader(tt.data))
			if tt.ok && err != nil {
				t.Fatal(err)
			}
			if !tt.ok && !errors.Is(err, ErrInvalidProjectAsset) {
				t.Fatalf("error=%v", err)
			}
		})
	}
}

func TestISOBoxExtendedAndToEndSizes(t *testing.T) {
	svc := NewService(t.TempDir())
	id := uuid.NewString()
	for _, data := range [][]byte{isoFileWithTopSize("extended"), isoFileWithTopSize("to-end")} {
		if _, err := svc.SaveProjectAsset(id, domain.AssetFinalVideo, "video.mp4", bytes.NewReader(data)); err != nil {
			t.Fatalf("valid sized box: %v", err)
		}
	}
	bad := isoFile("vide")
	bad[0] = 0
	bad[1] = 0
	bad[2] = 0
	bad[3] = 1
	if _, err := svc.SaveProjectAsset(id, domain.AssetFinalVideo, "bad.mp4", bytes.NewReader(bad)); !errors.Is(err, ErrInvalidProjectAsset) {
		t.Fatalf("overflow error=%v", err)
	}
}

func validWAV() []byte {
	b := make([]byte, 46)
	copy(b, "RIFF")
	binary.LittleEndian.PutUint32(b[4:8], 38)
	copy(b[8:], "WAVEfmt ")
	binary.LittleEndian.PutUint32(b[16:20], 16)
	binary.LittleEndian.PutUint16(b[20:22], 1)
	binary.LittleEndian.PutUint16(b[22:24], 1)
	binary.LittleEndian.PutUint32(b[24:28], 8000)
	binary.LittleEndian.PutUint32(b[28:32], 8000)
	binary.LittleEndian.PutUint16(b[32:34], 1)
	binary.LittleEndian.PutUint16(b[34:36], 8)
	copy(b[36:], "data")
	binary.LittleEndian.PutUint32(b[40:44], 1)
	b[44] = 128
	return b
}
func validMP3() []byte {
	frame := make([]byte, 417)
	copy(frame, []byte{0xff, 0xfb, 0x90, 0x64})
	return append(append([]byte{}, frame...), frame...)
}
func isoFileWithTopSize(mode string) []byte {
	b := isoFile("vide")
	ftypSize := int(binary.BigEndian.Uint32(b[:4]))
	rest := b[ftypSize:]
	if mode == "extended" {
		ext := make([]byte, 16+ftypSize-8)
		binary.BigEndian.PutUint32(ext[:4], 1)
		copy(ext[4:8], "ftyp")
		binary.BigEndian.PutUint64(ext[8:16], uint64(len(ext)))
		copy(ext[16:], b[8:ftypSize])
		return append(ext, rest...)
	}
	binary.BigEndian.PutUint32(rest[:4], 0)
	return append(b[:ftypSize], rest...)
}

func isoFile(handlers ...string) []byte {
	box := func(kind string, payload []byte) []byte {
		size := 8 + len(payload)
		out := []byte{byte(size >> 24), byte(size >> 16), byte(size >> 8), byte(size)}
		out = append(out, kind...)
		return append(out, payload...)
	}
	ftyp := box("ftyp", []byte("isom\x00\x00\x00\x00isommp42"))
	var tracks []byte
	for _, handler := range handlers {
		hdlrPayload := make([]byte, 24)
		copy(hdlrPayload[8:12], handler)
		tracks = append(tracks, box("trak", box("mdia", box("hdlr", hdlrPayload)))...)
	}
	return append(ftyp, box("moov", tracks)...)
}

func malformedWebP() []byte {
	return []byte{
		'R', 'I', 'F', 'F', 12, 0, 0, 0,
		'W', 'E', 'B', 'P', 'V', 'P', '8', 'X',
		0, 0, 0, 0,
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

func encodeSizedPNG(t *testing.T, width, height int) []byte {
	t.Helper()
	data := make([]byte, 13)
	binary.BigEndian.PutUint32(data[0:4], uint32(width))
	binary.BigEndian.PutUint32(data[4:8], uint32(height))
	data[8], data[9], data[10], data[11], data[12] = 8, 2, 0, 0, 0
	var buffer bytes.Buffer
	buffer.Write([]byte("\x89PNG\r\n\x1a\n"))
	binary.Write(&buffer, binary.BigEndian, uint32(len(data)))
	buffer.WriteString("IHDR")
	buffer.Write(data)
	crc := crc32.NewIEEE()
	crc.Write([]byte("IHDR"))
	crc.Write(data)
	binary.Write(&buffer, binary.BigEndian, crc.Sum32())
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
