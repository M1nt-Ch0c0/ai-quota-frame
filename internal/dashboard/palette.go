package dashboard

import (
	"bytes"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"math"
)

// RGB is one color in an e-paper output palette.
type RGB struct {
	R uint8
	G uint8
	B uint8
}

// E6Palette contains the six output colors supported by a Spectra 6 panel.
type E6Palette struct {
	Black  RGB
	White  RGB
	Yellow RGB
	Red    RGB
	Blue   RGB
	Green  RGB
}

// TheoreticalE6Palette is the exact raw-PNG palette consumed by the self-built
// photoframe component.
var TheoreticalE6Palette = E6Palette{
	Black:  RGB{R: 0, G: 0, B: 0},
	White:  RGB{R: 255, G: 255, B: 255},
	Yellow: RGB{R: 255, G: 255, B: 0},
	Red:    RGB{R: 255, G: 0, B: 0},
	Blue:   RGB{R: 0, G: 0, B: 255},
	Green:  RGB{R: 0, G: 255, B: 0},
}

// Validate ensures all six logical colors map to distinct RGB values.
func (palette E6Palette) Validate() error {
	seen := make(map[RGB]string, 6)
	for _, entry := range palette.entries() {
		if previous, exists := seen[entry.color]; exists {
			return fmt.Errorf("E6 palette colors %q and %q have duplicate RGB values", previous, entry.name)
		}
		seen[entry.color] = entry.name
	}
	return nil
}

type paletteEntry struct {
	name  string
	color RGB
}

func (palette E6Palette) entries() [6]paletteEntry {
	return [6]paletteEntry{
		{name: "black", color: palette.Black},
		{name: "white", color: palette.White},
		{name: "yellow", color: palette.Yellow},
		{name: "red", color: palette.Red},
		{name: "blue", color: palette.Blue},
		{name: "green", color: palette.Green},
	}
}

// QuantizeTheoreticalE6 maps every source pixel to the exact theoretical E6
// output palette. It deliberately performs no error diffusion or ordered
// dithering. Transparent input is composited over white and every output pixel
// is opaque.
func QuantizeTheoreticalE6(input []byte) ([]byte, error) {
	return quantizePNG(input, TheoreticalE6Palette)
}

func quantizePNG(input []byte, palette E6Palette) ([]byte, error) {
	return quantizePNGWithDitherRegions(input, palette, nil)
}

// quantizePNGWithDitherRegions preserves the normal crisp quantization outside
// the selected artwork. Only those regions mix output inks to approximate the
// source RGB colors; the finished PNG still contains exactly the device inks.
func quantizePNGWithDitherRegions(input []byte, palette E6Palette, regions []image.Rectangle) ([]byte, error) {
	if err := palette.Validate(); err != nil {
		return nil, err
	}
	source, err := png.Decode(bytes.NewReader(input))
	if err != nil {
		return nil, fmt.Errorf("decode dashboard PNG: %w", err)
	}

	bounds := source.Bounds()
	output := image.NewNRGBA(image.Rect(0, 0, bounds.Dx(), bounds.Dy()))
	for y := bounds.Min.Y; y < bounds.Max.Y; y++ {
		for x := bounds.Min.X; x < bounds.Max.X; x++ {
			sourceColor := color.NRGBAModel.Convert(source.At(x, y)).(color.NRGBA)
			composited := compositeOver(sourceColor, palette.White)
			nearest := nearestPaletteColor(composited, palette)
			output.SetNRGBA(x-bounds.Min.X, y-bounds.Min.Y, color.NRGBA{
				R: nearest.R,
				G: nearest.G,
				B: nearest.B,
				A: 255,
			})
		}
	}

	for _, region := range regions {
		ditherRegion(source, output, region.Intersect(output.Bounds()), palette)
	}

	var encoded bytes.Buffer
	if err := png.Encode(&encoded, output); err != nil {
		return nil, fmt.Errorf("encode theoretical E6 dashboard PNG: %w", err)
	}
	return encoded.Bytes(), nil
}

// ditherRegion uses serpentine Floyd-Steinberg error diffusion in linear RGB.
// Ink coverage mixes reflected light, not gamma-encoded sRGB channel values;
// diffusing encoded values would make the small marks much too bright.
// Exact palette pixels are locked to keep backgrounds and ink patterns clean.
func ditherRegion(source image.Image, output *image.NRGBA, region image.Rectangle, palette E6Palette) {
	if region.Empty() {
		return
	}
	width, height := region.Dx(), region.Dy()
	work := make([][3]float64, width*height)
	active := make([]bool, len(work))
	entries := palette.entries()
	var linearInks [6][3]float64
	inks := make(map[RGB]bool, 6)
	for index, entry := range entries {
		inks[entry.color] = true
		linearInks[index] = linearRGB(entry.color)
	}
	for y := 0; y < height; y++ {
		for x := 0; x < width; x++ {
			pixel := color.NRGBAModel.Convert(source.At(source.Bounds().Min.X+region.Min.X+x, source.Bounds().Min.Y+region.Min.Y+y)).(color.NRGBA)
			rgb := compositeOver(pixel, palette.White)
			index := y*width + x
			active[index] = !inks[rgb]
			work[index] = linearRGB(rgb)
		}
	}
	for y := 0; y < height; y++ {
		for step := 0; step < width; step++ {
			x, direction := step, 1
			if y%2 != 0 {
				x, direction = width-1-step, -1
			}
			index := y*width + x
			if !active[index] {
				continue
			}
			old := work[index]
			nearest, distance := 0, math.Inf(1)
			for candidate, ink := range linearInks {
				var squared float64
				for channel := range old {
					delta := min(1, max(0, old[channel])) - ink[channel]
					squared += delta * delta
				}
				if squared < distance {
					nearest, distance = candidate, squared
				}
			}
			ink := entries[nearest].color
			output.SetNRGBA(region.Min.X+x, region.Min.Y+y, color.NRGBA{R: ink.R, G: ink.G, B: ink.B, A: 255})
			diff := [3]float64{old[0] - linearInks[nearest][0], old[1] - linearInks[nearest][1], old[2] - linearInks[nearest][2]}
			spread := func(dx, dy int, weight float64) {
				nx, ny := x+dx*direction, y+dy
				if nx < 0 || nx >= width || ny >= height || !active[ny*width+nx] {
					return
				}
				for channel := range diff {
					work[ny*width+nx][channel] += diff[channel] * weight
				}
			}
			spread(1, 0, 7.0/16)
			spread(-1, 1, 3.0/16)
			spread(0, 1, 5.0/16)
			spread(1, 1, 1.0/16)
		}
	}
}

func linearRGB(rgb RGB) [3]float64 {
	var result [3]float64
	for index, value := range []uint8{rgb.R, rgb.G, rgb.B} {
		v := float64(value) / 255
		if v <= 0.04045 {
			result[index] = v / 12.92
		} else {
			result[index] = math.Pow((v+0.055)/1.055, 2.4)
		}
	}
	return result
}

func compositeOver(foreground color.NRGBA, background RGB) RGB {
	alpha := uint32(foreground.A)
	inverseAlpha := uint32(255 - foreground.A)
	return RGB{
		R: uint8((uint32(foreground.R)*alpha + uint32(background.R)*inverseAlpha + 127) / 255),
		G: uint8((uint32(foreground.G)*alpha + uint32(background.G)*inverseAlpha + 127) / 255),
		B: uint8((uint32(foreground.B)*alpha + uint32(background.B)*inverseAlpha + 127) / 255),
	}
}

func nearestPaletteColor(source RGB, palette E6Palette) RGB {
	entries := palette.entries()
	nearest := entries[0].color
	nearestDistance := colorDistanceSquared(source, nearest)
	for _, entry := range entries[1:] {
		distance := colorDistanceSquared(source, entry.color)
		if distance < nearestDistance {
			nearest = entry.color
			nearestDistance = distance
		}
	}
	return nearest
}

func colorDistanceSquared(left, right RGB) uint32 {
	r := int32(left.R) - int32(right.R)
	g := int32(left.G) - int32(right.G)
	b := int32(left.B) - int32(right.B)
	return uint32(r*r + g*g + b*b)
}
