package dashboard

import (
	"bytes"
	"fmt"
	"image"
	"image/color"
	"image/png"
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

// TheoreticalE6Palette is the exact output palette recognized by the
// PhotoFrame v2.18 processed-PNG fast path.
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

	var encoded bytes.Buffer
	if err := png.Encode(&encoded, output); err != nil {
		return nil, fmt.Errorf("encode theoretical E6 dashboard PNG: %w", err)
	}
	return encoded.Bytes(), nil
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
