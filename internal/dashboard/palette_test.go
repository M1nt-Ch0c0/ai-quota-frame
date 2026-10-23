package dashboard

import (
	"bytes"
	"encoding/binary"
	"image"
	"image/color"
	"image/png"
	"math"
	"testing"
)

func TestQuantizeTheoreticalE6ProducesPhotoframeContractPNG(t *testing.T) {
	source := image.NewNRGBA(image.Rect(0, 0, Width, Height))
	colors := []color.NRGBA{
		{R: 0, G: 0, B: 0, A: 255},
		{R: 255, G: 255, B: 255, A: 255},
		{R: 255, G: 255, B: 0, A: 255},
		{R: 255, G: 0, B: 0, A: 255},
		{R: 0, G: 0, B: 255, A: 255},
		{R: 0, G: 255, B: 0, A: 255},
	}
	for x := 0; x < Width; x++ {
		stripe := colors[x*len(colors)/Width]
		for y := 0; y < Height; y++ {
			source.SetNRGBA(x, y, stripe)
		}
	}

	payload := encodeTestPNG(t, source)
	result, err := QuantizeTheoreticalE6(payload)
	if err != nil {
		t.Fatalf("QuantizeTheoreticalE6() error = %v", err)
	}

	assertPhotoframePNGHeader(t, result, Width, Height)
	decoded, err := png.Decode(bytes.NewReader(result))
	if err != nil {
		t.Fatalf("png.Decode() error = %v", err)
	}
	allowed := theoreticalColorSet()
	seen := make(map[RGB]bool, len(allowed))
	for y := decoded.Bounds().Min.Y; y < decoded.Bounds().Max.Y; y++ {
		for x := decoded.Bounds().Min.X; x < decoded.Bounds().Max.X; x++ {
			pixel := color.NRGBAModel.Convert(decoded.At(x, y)).(color.NRGBA)
			if pixel.A != 255 {
				t.Fatalf("pixel (%d,%d) alpha = %d, want 255", x, y, pixel.A)
			}
			rgb := RGB{R: pixel.R, G: pixel.G, B: pixel.B}
			if !allowed[rgb] {
				t.Fatalf("pixel (%d,%d) = %#v, not in theoretical E6 palette", x, y, rgb)
			}
			seen[rgb] = true
		}
	}
	if len(seen) != 6 {
		t.Fatalf("output contains %d theoretical colors, want all 6", len(seen))
	}
}

func TestQuantizeTheoreticalE6DoesNotDitherUniformColor(t *testing.T) {
	source := image.NewNRGBA(image.Rect(0, 0, 64, 64))
	uniform := color.NRGBA{R: 128, G: 128, B: 128, A: 255}
	for y := 0; y < source.Bounds().Dy(); y++ {
		for x := 0; x < source.Bounds().Dx(); x++ {
			source.SetNRGBA(x, y, uniform)
		}
	}

	result, err := QuantizeTheoreticalE6(encodeTestPNG(t, source))
	if err != nil {
		t.Fatalf("QuantizeTheoreticalE6() error = %v", err)
	}
	decoded, err := png.Decode(bytes.NewReader(result))
	if err != nil {
		t.Fatalf("png.Decode() error = %v", err)
	}
	seen := make(map[RGB]bool)
	for y := 0; y < decoded.Bounds().Dy(); y++ {
		for x := 0; x < decoded.Bounds().Dx(); x++ {
			pixel := color.NRGBAModel.Convert(decoded.At(x, y)).(color.NRGBA)
			seen[RGB{R: pixel.R, G: pixel.G, B: pixel.B}] = true
		}
	}
	if len(seen) != 1 {
		t.Fatalf("uniform source produced %d output colors, want 1 (no dithering): %#v", len(seen), seen)
	}
}

func TestQuantizeTheoreticalE6CompositesTransparencyOverWhite(t *testing.T) {
	source := image.NewNRGBA(image.Rect(0, 0, 1, 1))
	source.SetNRGBA(0, 0, color.NRGBA{R: 255, G: 0, B: 0, A: 0})

	result, err := QuantizeTheoreticalE6(encodeTestPNG(t, source))
	if err != nil {
		t.Fatalf("QuantizeTheoreticalE6() error = %v", err)
	}
	decoded, err := png.Decode(bytes.NewReader(result))
	if err != nil {
		t.Fatalf("png.Decode() error = %v", err)
	}
	pixel := color.NRGBAModel.Convert(decoded.At(0, 0)).(color.NRGBA)
	if want := (color.NRGBA{R: 255, G: 255, B: 255, A: 255}); pixel != want {
		t.Fatalf("transparent pixel = %#v, want opaque white %#v", pixel, want)
	}
}

func TestE6PaletteRejectsDuplicateColors(t *testing.T) {
	invalid := TheoreticalE6Palette
	invalid.Green = invalid.Blue
	if err := invalid.Validate(); err == nil {
		t.Fatal("Validate() error = nil, want duplicate-color error")
	}
}

func TestDitherRegionsApproximateBrandColorsWithoutChangingTextOrInkPixels(t *testing.T) {
	for name, target := range map[string]color.NRGBA{
		"Claude terracotta": {R: 217, G: 119, B: 87, A: 255},
		"ChatGPT teal":      {R: 16, G: 163, B: 127, A: 255},
		"antialiased grey":  {R: 128, G: 128, B: 128, A: 255},
	} {
		t.Run(name, func(t *testing.T) {
			source := image.NewNRGBA(image.Rect(0, 0, 96, 80))
			region := image.Rect(16, 16, 80, 64)
			for y := 0; y < 80; y++ {
				for x := 0; x < 96; x++ {
					pixel := color.NRGBA{R: 255, G: 255, B: 255, A: 255}
					if image.Pt(x, y).In(region) {
						pixel = target
					}
					// Lock a white/black rule through the artwork, plus grey
					// text outside it that must keep the original quantization.
					if y == 40 {
						pixel = color.NRGBA{A: 255}
						if x%2 == 0 {
							pixel = color.NRGBA{R: 255, G: 255, B: 255, A: 255}
						}
					}
					if y == 8 {
						pixel = color.NRGBA{R: 128, G: 128, B: 128, A: 255}
					}
					source.SetNRGBA(x, y, pixel)
				}
			}
			input := encodeTestPNG(t, source)
			plainPNG, err := QuantizeTheoreticalE6(input)
			if err != nil {
				t.Fatal(err)
			}
			result, err := quantizePNGWithDitherRegions(input, TheoreticalE6Palette, []image.Rectangle{region})
			if err != nil {
				t.Fatal(err)
			}
			assertPhotoframePNGHeader(t, result, 96, 80)
			plain, _ := png.Decode(bytes.NewReader(plainPNG))
			decoded, err := png.Decode(bytes.NewReader(result))
			if err != nil {
				t.Fatal(err)
			}
			allowed, seen := theoreticalColorSet(), map[RGB]bool{}
			var sum [3]float64
			count := 0.0
			for y := 0; y < 80; y++ {
				for x := 0; x < 96; x++ {
					pixel := color.NRGBAModel.Convert(decoded.At(x, y)).(color.NRGBA)
					ink := RGB{pixel.R, pixel.G, pixel.B}
					if pixel.A != 255 || !allowed[ink] {
						t.Fatalf("pixel (%d,%d) violates the six-ink contract: %v", x, y, pixel)
					}
					if !image.Pt(x, y).In(region) || y == 40 {
						if want := color.NRGBAModel.Convert(plain.At(x, y)); pixel != want {
							t.Fatalf("dithering changed text, background, or an exact ink at (%d,%d)", x, y)
						}
						continue
					}
					seen[ink] = true
					sum[0] += float64(pixel.R)
					sum[1] += float64(pixel.G)
					sum[2] += float64(pixel.B)
					count++
				}
			}
			if len(seen) < 2 {
				t.Fatal("brand color was replaced with a single ink instead of dithered")
			}
			// Pure device inks are 0 or 1 in linear light. Their coverage
			// must approximate the source reflectance, not its sRGB bytes.
			for channel, want := range linearRGB(RGB{target.R, target.G, target.B}) {
				if mean := sum[channel] / (255 * count); math.Abs(mean-want) > 0.025 {
					t.Errorf("mean linear channel %d = %.3f, want within 0.025 of %.3f", channel, mean, want)
				}
			}
			second, err := quantizePNGWithDitherRegions(input, TheoreticalE6Palette, []image.Rectangle{region})
			if err != nil || !bytes.Equal(result, second) {
				t.Fatal("identical artwork must produce identical PNGs for frame deduplication")
			}
		})
	}
}

func theoreticalColorSet() map[RGB]bool {
	result := make(map[RGB]bool, 6)
	for _, entry := range TheoreticalE6Palette.entries() {
		result[entry.color] = true
	}
	return result
}

func encodeTestPNG(t *testing.T, source image.Image) []byte {
	t.Helper()
	var encoded bytes.Buffer
	if err := png.Encode(&encoded, source); err != nil {
		t.Fatalf("png.Encode() error = %v", err)
	}
	return encoded.Bytes()
}

func assertPhotoframePNGHeader(t *testing.T, payload []byte, width, height int) {
	t.Helper()
	if len(payload) < 29 || !bytes.Equal(payload[:8], []byte("\x89PNG\r\n\x1a\n")) {
		t.Fatal("output is not a valid PNG header")
	}
	if chunkLength := binary.BigEndian.Uint32(payload[8:12]); chunkLength != 13 {
		t.Fatalf("IHDR length = %d, want 13", chunkLength)
	}
	if !bytes.Equal(payload[12:16], []byte("IHDR")) {
		t.Fatalf("first PNG chunk = %q, want IHDR", payload[12:16])
	}
	if got := int(binary.BigEndian.Uint32(payload[16:20])); got != width {
		t.Fatalf("PNG IHDR width = %d, want %d", got, width)
	}
	if got := int(binary.BigEndian.Uint32(payload[20:24])); got != height {
		t.Fatalf("PNG IHDR height = %d, want %d", got, height)
	}
	if bitDepth := payload[24]; bitDepth != 8 {
		t.Fatalf("PNG bit depth = %d, want 8", bitDepth)
	}
	if colorType := payload[25]; colorType != 2 {
		t.Fatalf("PNG color type = %d, want 2 (RGB truecolor without alpha)", colorType)
	}
	if interlace := payload[28]; interlace != 0 {
		t.Fatalf("PNG interlace method = %d, want 0", interlace)
	}
}
