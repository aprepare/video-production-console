package cover

import (
	"bytes"
	"errors"
	"image"
	"image/color"
	"image/draw"
	"image/png"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
)

func syntheticReference(t *testing.T, dir string, lines bool) (string, *image.NRGBA) {
	t.Helper()
	img := image.NewNRGBA(image.Rect(0, 0, 800, 1000))
	for y := 0; y < 1000; y++ {
		shade := uint8(y / 40)
		draw.Draw(img, image.Rect(0, y, 800, y+1), image.NewUniform(color.NRGBA{shade, shade, shade + 5, 255}), image.Point{}, draw.Src)
	}
	if lines {
		for _, y := range []int{300, 600, 900} {
			draw.Draw(img, image.Rect(0, y, 800, y+4), image.NewUniform(color.NRGBA{255, 209, 102, 255}), image.Point{}, draw.Src)
		}
	}
	for _, x := range []int{70, 100, 130} {
		draw.Draw(img, image.Rect(x, 445, x+15, 465), image.White, image.Point{}, draw.Src)
	}
	draw.Draw(img, image.Rect(75, 700, 400, 850), image.NewUniform(color.NRGBA{20, 100, 180, 255}), image.Point{}, draw.Src)
	path := filepath.Join(dir, "reference.png")
	writePNG(t, path, img)
	return path, img
}

func writePNG(t *testing.T, path string, img image.Image) {
	t.Helper()
	var b bytes.Buffer
	if err := png.Encode(&b, img); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, b.Bytes(), 0o600); err != nil {
		t.Fatal(err)
	}
}

func requireCoverFont(t *testing.T) {
	t.Helper()
	if _, err := os.Stat("C:/Windows/Fonts/msyhbd.ttc"); err != nil {
		t.Skip("封面字体测试需要 Windows 微软雅黑 Bold TTC")
	}
}

func TestRenderReplacesOnlyTitleRegion(t *testing.T) {
	requireCoverFont(t)
	root := t.TempDir()
	ref, before := syntheticReference(t, root, true)
	path := filepath.Join(root, "output", "cover.png")
	result, err := Render(RenderRequest{ReferencePath: ref, Titles: [2]string{"守住家底", "存款别乱动"}, Colors: [2]string{"#FFD166", "#F5F2E9"}, OutputPath: path})
	if err != nil {
		t.Fatal(err)
	}
	if result.Width != 800 || result.Height != 1000 || result.SeparatorLines != [2]int{301, 601} {
		t.Fatalf("dimensions/lines = %+v", result)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	after, err := png.Decode(bytes.NewReader(raw))
	if err != nil {
		t.Fatal(err)
	}
	for y := 0; y < 1000; y++ {
		if y >= 304 && y < 600 {
			continue
		}
		for x := 0; x < 800; x++ {
			if before.At(x, y) != color.NRGBAModel.Convert(after.At(x, y)) {
				t.Fatalf("pixel outside title region changed at (%d,%d)", x, y)
			}
		}
	}
	for _, x := range []int{75, 105, 135} {
		got := color.NRGBAModel.Convert(after.At(x, 455)).(color.NRGBA)
		want := before.NRGBAAt(0, 455)
		if absInt(int(got.R)-int(want.R)) > 2 || absInt(int(got.G)-int(want.G)) > 2 || absInt(int(got.B)-int(want.B)) > 2 {
			t.Fatalf("old title remains at %d: %v, background %v", x, got, want)
		}
	}
	for i, target := range []color.NRGBA{{255, 209, 102, 255}, {245, 242, 233, 255}} {
		pixels, glow := 0, 0
		minX, maxX := 800, 0
		for y := 304 + i*148; y < 304+(i+1)*148; y++ {
			for x := 0; x < 800; x++ {
				c := color.NRGBAModel.Convert(after.At(x, y)).(color.NRGBA)
				if c == target {
					pixels++
					minX, maxX = min(minX, x), max(maxX, x)
				}
				if c.R > 20 && c.R < 80 && c.R > c.B+3 {
					glow++
				}
			}
		}
		if pixels < 100 || absInt(minX+maxX-799) > 4 {
			t.Fatalf("line %d target pixels=%d bounds=%d..%d", i, pixels, minX, maxX)
		}
		if i == 0 && glow == 0 {
			t.Fatal("warm title is missing its glow")
		}
	}
}

func TestClearTitlesUsesPerRowMedian(t *testing.T) {
	img := image.NewNRGBA(image.Rect(0, 0, 400, 8))
	for y := 0; y < 8; y++ {
		background := color.NRGBA{uint8(y * 2), uint8(y * 3), uint8(y * 4), 255}
		draw.Draw(img, image.Rect(0, y, 400, y+1), image.NewUniform(background), image.Point{}, draw.Src)
		// 两侧样本中的少量亮点不应像均值那样抬高底色。
		draw.Draw(img, image.Rect(0, y, 20, y+1), image.White, image.Point{}, draw.Src)
		draw.Draw(img, image.Rect(100, y, 300, y+1), image.White, image.Point{}, draw.Src)
	}
	clearTitles(img, img.Bounds())
	for y := 0; y < 8; y++ {
		want := color.NRGBA{uint8(y * 2), uint8(y * 3), uint8(y * 4), 255}
		for x := 0; x < 400; x++ {
			if got := img.NRGBAAt(x, y); got != want {
				t.Fatalf("median background at (%d,%d) = %v, want %v", x, y, got, want)
			}
		}
	}
}

func TestRenderFallsBackToSeparatorRatios(t *testing.T) {
	requireCoverFont(t)
	root := t.TempDir()
	ref, _ := syntheticReference(t, root, false)
	result, err := Render(RenderRequest{ReferencePath: ref, Titles: [2]string{"家底", "安心"}, SeparatorLines: [2]float64{0.30, 0.60}, OutputPath: filepath.Join(root, "cover.png")})
	if err != nil {
		t.Fatal(err)
	}
	if result.SeparatorLines != [2]int{300, 600} {
		t.Fatalf("fallback = %v", result.SeparatorLines)
	}
}

func TestResolveAccountStyleMatchesConsoleIDAndDefaults(t *testing.T) {
	root := t.TempDir()
	accountDir := filepath.Join(root, "accounts", "测试账号")
	ref, _ := syntheticReference(t, t.TempDir(), true)
	raw, _ := os.ReadFile(ref)
	defaultRef := filepath.Join(accountDir, "cover-style", "cover-reference.png")
	if err := os.MkdirAll(filepath.Dir(defaultRef), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(defaultRef, raw, 0o600); err != nil {
		t.Fatal(err)
	}
	profile := filepath.Join(accountDir, "profile.json")
	if err := os.WriteFile(profile, []byte(`{"console_id":"matched"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	style, err := ResolveAccountStyle(root, "matched", "")
	if err != nil {
		t.Fatal(err)
	}
	if style.Account != "测试账号" || style.ReferencePath != defaultRef || style.Colors != [2]string{"#FFD166", "#F5F2E9"} || style.SeparatorLines != [2]float64{0.34, 0.53} {
		t.Fatalf("defaults = %+v", style)
	}
	if _, err := ResolveAccountStyle(root, "missing", ""); !errors.Is(err, ErrNoTemplate) {
		t.Fatalf("missing account = %v", err)
	}
	if err := os.WriteFile(profile, []byte(`{"account":"显示账号名","console_id":"matched","cover_reference":"cover-style/cover-reference.png","cover_style":"custom.json"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(accountDir, "custom.json"), []byte(`{"cover":{"title_colors":["#123456","#abcdef"],"separator_lines":[0.25,0.55]}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	style, err = ResolveAccountStyle(root, "matched", "")
	if err != nil || style.Account != "显示账号名" || style.Colors != [2]string{"#123456", "#ABCDEF"} || style.SeparatorLines != [2]float64{0.25, 0.55} {
		t.Fatalf("custom style = %+v, %v", style, err)
	}
	if err := os.WriteFile(profile, []byte(`{"cover_reference":"cover-style/cover-reference.png"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if style, err := ResolveAccountStyle(root, "unregistered", "测试账号"); err != nil || style.ReferencePath != defaultRef {
		t.Fatalf("name fallback = %+v, %v", style, err)
	}
	if _, err := ResolveAccountStyle(root, "unregistered", "别的账号"); !errors.Is(err, ErrNoTemplate) {
		t.Fatalf("unknown name = %v", err)
	}
	if err := os.Remove(defaultRef); err != nil {
		t.Fatal(err)
	}
	if _, err := ResolveAccountStyle(root, "matched", ""); !errors.Is(err, ErrNoTemplate) {
		t.Fatalf("missing reference = %v", err)
	}
}

func TestResolveAccountStyleDefaultRootUsesUserProfile(t *testing.T) {
	root := t.TempDir()
	t.Setenv("USERPROFILE", root)
	dir := filepath.Join(root, ".codex", "skills", "multi-account-video-production", "accounts", "默认账号")
	if err := os.MkdirAll(filepath.Join(dir, "cover-style"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "profile.json"), []byte(`{"console_id":"default"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	writePNG(t, filepath.Join(dir, "cover-style", "cover-reference.png"), image.NewRGBA(image.Rect(0, 0, 2, 2)))
	if style, err := ResolveAccountStyle("", "default", ""); err != nil || style.Account != "默认账号" {
		t.Fatalf("default skill root = %+v, %v", style, err)
	}
}

func TestSaveToLibraryReusesContentAndVersionsNames(t *testing.T) {
	root := t.TempDir()
	src, library := filepath.Join(root, "src.png"), filepath.Join(root, "library")
	if err := os.WriteFile(src, []byte("first"), 0o600); err != nil {
		t.Fatal(err)
	}
	first, err := SaveToLibrary(src, library, "2026-10-14", "账<号>", "钱:放哪?")
	if err != nil || filepath.Base(first) != "2026-10-14_账号_钱放哪.png" {
		t.Fatalf("first = %s, %v", first, err)
	}
	again, err := SaveToLibrary(src, library, "2026-10-14", "账<号>", "钱:放哪?")
	if err != nil || again != first {
		t.Fatalf("same content not reused: %s, %v", again, err)
	}
	if err := os.WriteFile(src, []byte("second"), 0o600); err != nil {
		t.Fatal(err)
	}
	second, err := SaveToLibrary(src, library, "2026-10-14", "账<号>", "钱:放哪?")
	if err != nil || !strings.HasSuffix(second, "_v2.png") {
		t.Fatalf("different content not versioned: %s, %v", second, err)
	}
	again, err = SaveToLibrary(src, library, "2026-10-14", "账<号>", "钱:放哪?")
	if err != nil || again != second {
		t.Fatalf("versioned content not reused: %s, %v", again, err)
	}
	raw, _ := os.ReadFile(first)
	if string(raw) != "first" {
		t.Fatal("old cover was overwritten")
	}
}

func TestSaveToLibraryConcurrentSameContent(t *testing.T) {
	root := t.TempDir()
	src := filepath.Join(root, "source.png")
	if err := os.WriteFile(src, []byte("cover"), 0o600); err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	paths := make([]string, 8)
	errs := make([]error, len(paths))
	for i := range paths {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			paths[i], errs[i] = SaveToLibrary(src, filepath.Join(root, "library"), "2026-10-14", "账号", "标题")
		}(i)
	}
	wg.Wait()
	for i := range paths {
		if errs[i] != nil || paths[i] != paths[0] {
			t.Fatalf("concurrent save: %v %v", paths, errs)
		}
	}
	files, _ := os.ReadDir(filepath.Dir(paths[0]))
	if len(files) != 1 || !reflect.DeepEqual(errs, make([]error, 8)) {
		t.Fatalf("extra files or errors: %v %v", files, errs)
	}
}

func absInt(n int) int {
	if n < 0 {
		return -n
	}
	return n
}
