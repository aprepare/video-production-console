package cover

import (
	"errors"
	"fmt"
	"image"
	"image/color"
	"image/draw"
	_ "image/jpeg"
	"image/png"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"golang.org/x/image/font"
	"golang.org/x/image/font/opentype"
	"golang.org/x/image/math/fixed"
)

const titleFontPath = "C:/Windows/Fonts/msyhbd.ttc"

type RenderRequest struct {
	ReferencePath  string
	Titles         [2]string
	Colors         [2]string
	SeparatorLines [2]float64
	// TitleBox 是标定过的标题区 [上, 下)（像素）；为零时自动找分隔线。
	TitleBox   [2]int
	OutputPath string
}

type Result struct {
	Width          int
	Height         int
	SeparatorLines [2]int
}

type lineGroup struct{ start, end int }

func Render(req RenderRequest) (Result, error) {
	if req.ReferencePath == "" || req.OutputPath == "" {
		return Result{}, errors.New("cover reference and output paths are required")
	}
	reference, err := filepath.Abs(req.ReferencePath)
	if err != nil {
		return Result{}, err
	}
	output, err := filepath.Abs(req.OutputPath)
	if err != nil {
		return Result{}, err
	}
	if strings.EqualFold(reference, output) {
		return Result{}, errors.New("cover output must not overwrite its reference")
	}
	var colors [2]color.NRGBA
	for i := range req.Titles {
		req.Titles[i], err = NormalizeTitle(req.Titles[i])
		if err != nil {
			return Result{}, err
		}
		if req.Colors[i] == "" {
			req.Colors[i] = defaultColors[i]
		}
		colors[i], err = parseColor(req.Colors[i])
		if err != nil {
			return Result{}, err
		}
	}
	file, err := os.Open(reference)
	if err != nil {
		return Result{}, err
	}
	source, _, decodeErr := image.Decode(file)
	closeErr := file.Close()
	if decodeErr != nil {
		return Result{}, fmt.Errorf("decode cover reference: %w", decodeErr)
	}
	if closeErr != nil {
		return Result{}, closeErr
	}
	width, height := source.Bounds().Dx(), source.Bounds().Dy()
	if width < 2 || height < 3 {
		return Result{}, errors.New("cover reference is too small")
	}
	img := image.NewNRGBA(image.Rect(0, 0, width, height))
	draw.Draw(img, img.Bounds(), source, source.Bounds().Min, draw.Src)
	groups := separatorGroups(img)
	var positions [2]int
	var top, bottom int
	if box := req.TitleBox; box[1] > box[0] {
		// 账号模板标定过标题区（像素）时直接用，横版等特殊版式靠它。
		top, bottom = max(0, box[0]), min(height, box[1])
		positions = [2]int{top, bottom}
	} else if len(groups) >= 2 {
		positions = [2]int{(groups[0].start + groups[0].end - 1) / 2, (groups[1].start + groups[1].end - 1) / 2}
		top, bottom = groups[0].end, groups[1].start
	} else {
		ratios := req.SeparatorLines
		if ratios == [2]float64{} {
			ratios = defaultSeparators
		}
		if !validSeparators(ratios) {
			return Result{}, errors.New("invalid cover separator ratios")
		}
		positions = [2]int{int(ratios[0] * float64(height)), int(ratios[1] * float64(height))}
		top, bottom = positions[0]+1, positions[1]
	}
	if bottom-top < 2 {
		return Result{}, errors.New("cover separators leave no title region")
	}
	fontBytes, err := os.ReadFile(titleFontPath)
	if err != nil {
		return Result{}, fmt.Errorf("read cover font: %w", err)
	}
	collection, err := opentype.ParseCollection(fontBytes)
	if err != nil {
		return Result{}, fmt.Errorf("parse cover font collection: %w", err)
	}
	titleFont, err := collection.Font(0)
	if err != nil {
		return Result{}, fmt.Errorf("read first cover font: %w", err)
	}
	region := image.Rect(0, top, width, bottom)
	clearTitles(img, region)
	for i, title := range req.Titles {
		if err := drawTitle(img, region, titleFont, title, colors[i], i); err != nil {
			return Result{}, err
		}
	}
	if err := writePNGAtomic(output, img); err != nil {
		return Result{}, err
	}
	return Result{Width: width, Height: height, SeparatorLines: positions}, nil
}

// separatorGroups 找封面上横贯全宽的彩色分隔线（暖金或玫红）。按「一行里
// 鲜艳像素的占比」判断，比整行平均亮度可靠：暗一些的玫红线也能找到，
// 夜景里零散的灯光又凑不满一整行。
func separatorGroups(img *image.NRGBA) []lineGroup {
	var groups []lineGroup
	start := -1
	width := img.Bounds().Dx()
	for y := 0; y <= img.Bounds().Dy(); y++ {
		line := false
		if y < img.Bounds().Dy() {
			// 分隔线是一整段连着的鲜艳色；标题笔画虽鲜艳但一行里断成很多小段。
			run, longest := 0, 0
			for x := 0; x < width; x++ {
				c := img.NRGBAAt(x, y)
				r, g, b := int(c.R), int(c.G), int(c.B)
				if r > 110 && (r-b > 40 || r-g > 60) {
					run++
					longest = max(longest, run)
				} else {
					run = 0
				}
			}
			line = float64(longest) >= float64(width)*0.22
		}
		if line && start < 0 {
			start = y
		}
		if !line && start >= 0 {
			// 分隔线是细线；连续很多行都鲜艳的是标题大字或灯光，不算。
			if y-start > 12 {
				start = -1
				continue
			}
			groups = append(groups, lineGroup{start, y})
			start = -1
			if len(groups) == 2 {
				break
			}
		}
	}
	return groups
}

func clearTitles(img *image.NRGBA, region image.Rectangle) {
	width := img.Bounds().Dx()
	side := min(50, width/2)
	var channels [4][]int
	for i := range channels {
		channels[i] = make([]int, side*2)
	}
	for y := region.Min.Y; y < region.Max.Y; y++ {
		for i := 0; i < side; i++ {
			for j, x := range []int{i, width - side + i} {
				c := img.NRGBAAt(x, y)
				index := i + j*side
				channels[0][index], channels[1][index], channels[2][index], channels[3][index] = int(c.R), int(c.G), int(c.B), int(c.A)
			}
		}
		var median [4]uint8
		for i, values := range channels {
			sort.Ints(values)
			median[i] = uint8((values[side-1] + values[side]) / 2)
		}
		fill := color.NRGBA{median[0], median[1], median[2], median[3]}
		for x := 0; x < width; x++ {
			img.SetNRGBA(x, y, fill)
		}
	}
}

func drawTitle(img *image.NRGBA, region image.Rectangle, titleFont *opentype.Font, title string, ink color.NRGBA, line int) error {
	size := max(40.0, float64(region.Dy())*0.30)
	var face font.Face
	var bounds fixed.Rectangle26_6
	for {
		var err error
		face, err = opentype.NewFace(titleFont, &opentype.FaceOptions{Size: size, DPI: 72, Hinting: font.HintingFull})
		if err != nil {
			return fmt.Errorf("create cover font face: %w", err)
		}
		bounds, _ = font.BoundString(face, title)
		if float64(bounds.Max.X-bounds.Min.X)/64 <= float64(region.Dx())*0.84 {
			break
		}
		_ = face.Close()
		if size <= 40 {
			return errors.New("cover title does not fit at the minimum 40px font size")
		}
		size = max(40, size-1)
	}
	defer face.Close()
	mask := image.NewAlpha(image.Rect(0, 0, region.Dx(), region.Dy()))
	centerY := fixed.Int26_6(float64(region.Dy()*64) * float64(line+1) / 3)
	drawer := font.Drawer{Dst: mask, Src: image.White, Face: face, Dot: fixed.Point26_6{
		X: fixed.I(region.Dx())/2 - (bounds.Min.X+bounds.Max.X)/2,
		Y: centerY - (bounds.Min.Y+bounds.Max.Y)/2,
	}}
	drawer.DrawString(title)
	glow := blurAlpha(mask, 8, 3)
	softInk := ink
	softInk.A = 128
	// 柔光和正文都裁在两线之间，分隔线与下方城市区域保持原像素。
	draw.DrawMask(img, region, image.NewUniform(softInk), image.Point{}, glow, image.Point{}, draw.Over)
	draw.DrawMask(img, region, image.NewUniform(ink), image.Point{}, mask, image.Point{}, draw.Over)
	return nil
}

// 三次半径 8 的可分离 box blur 近似 sigma=8px 的高斯柔光。
func blurAlpha(src *image.Alpha, radius, passes int) *image.Alpha {
	width, height := src.Rect.Dx(), src.Rect.Dy()
	current := src
	for pass := 0; pass < passes; pass++ {
		horizontal := image.NewAlpha(src.Rect)
		for y := 0; y < height; y++ {
			sum := 0
			for x := 0; x < min(width, radius+1); x++ {
				sum += int(current.Pix[y*current.Stride+x])
			}
			for x := 0; x < width; x++ {
				horizontal.Pix[y*horizontal.Stride+x] = uint8(sum / (radius*2 + 1))
				if remove := x - radius; remove >= 0 {
					sum -= int(current.Pix[y*current.Stride+remove])
				}
				if add := x + radius + 1; add < width {
					sum += int(current.Pix[y*current.Stride+add])
				}
			}
		}
		vertical := image.NewAlpha(src.Rect)
		for x := 0; x < width; x++ {
			sum := 0
			for y := 0; y < min(height, radius+1); y++ {
				sum += int(horizontal.Pix[y*horizontal.Stride+x])
			}
			for y := 0; y < height; y++ {
				vertical.Pix[y*vertical.Stride+x] = uint8(sum / (radius*2 + 1))
				if remove := y - radius; remove >= 0 {
					sum -= int(horizontal.Pix[remove*horizontal.Stride+x])
				}
				if add := y + radius + 1; add < height {
					sum += int(horizontal.Pix[add*horizontal.Stride+x])
				}
			}
		}
		current = vertical
	}
	return current
}

func writePNGAtomic(path string, img image.Image) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	file, err := os.CreateTemp(filepath.Dir(path), "cover-*.png")
	if err != nil {
		return err
	}
	tmp := file.Name()
	defer os.Remove(tmp)
	if err := png.Encode(file, img); err != nil {
		file.Close()
		return err
	}
	if err := file.Close(); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}
