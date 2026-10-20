package dashboard

import (
	"encoding/base64"
	"fmt"
	"html/template"
	"image"
	"image/color"
	"io/fs"
)

// A skin is a pre-baked six-ink background illustration with fixed white
// slots. The renderer draws live data only inside the slots, so the artwork
// can be as detailed as a dithered photo while text and bars stay crisp.

type skinRect struct{ X0, Y0, X1, Y1 int }

func (rect skinRect) Style() template.CSS {
	return template.CSS(fmt.Sprintf("left: %dpx; top: %dpx; width: %dpx; height: %dpx;",
		rect.X0, rect.Y0, rect.X1-rect.X0, rect.Y1-rect.Y0))
}

const skinSlotRadius = 8

var (
	skinClockSlot = skinRect{628, 14, 784, 42}
	skinRowSlots  = []skinRect{{16, 92, 784, 160}, {16, 168, 784, 236}, {16, 244, 784, 312}}
	skinUsageSlot = skinRect{16, 324, 688, 468}
)

func skinSlots() []skinRect {
	slots := []skinRect{skinClockSlot, skinUsageSlot}
	return append(slots, skinRowSlots...)
}

// skinThemes maps a skin theme to its baked background and to the themed
// look used when the configured rows do not fit the skin's three slots.
var skinThemes = map[string]string{"mint-skin": "mint"}

func skinBackground(theme string) (template.URL, error) {
	payload, err := fs.ReadFile(templateFS, "templates/skins/"+theme+".png")
	if err != nil {
		return "", fmt.Errorf("read %s background: %w", theme, err)
	}
	return template.URL("data:image/png;base64," + base64.StdEncoding.EncodeToString(payload)), nil
}

type skinFrameData struct {
	frameData
	Theme      string
	Art        themeArt
	Background template.URL
	ClockStyle template.CSS
	UsageStyle template.CSS
	RowStyles  []template.CSS
}

// BakeSkin turns generated artwork into a skin background: it scales the
// image to the frame, error-diffuses it onto the six inks with a serpentine
// Floyd-Steinberg pass and clears every slot to white.
func BakeSkin(art image.Image) *image.NRGBA {
	bounds := art.Bounds()
	work := make([][3]float64, Width*Height)
	for y := 0; y < Height; y++ {
		y0, y1 := bounds.Min.Y+y*bounds.Dy()/Height, bounds.Min.Y+(y+1)*bounds.Dy()/Height
		for x := 0; x < Width; x++ {
			x0, x1 := bounds.Min.X+x*bounds.Dx()/Width, bounds.Min.X+(x+1)*bounds.Dx()/Width
			var sum [3]float64
			count := 0.0
			for sy := y0; sy < max(y1, y0+1); sy++ {
				for sx := x0; sx < max(x1, x0+1); sx++ {
					c := color.NRGBAModel.Convert(art.At(sx, sy)).(color.NRGBA)
					sum[0] += float64(c.R)
					sum[1] += float64(c.G)
					sum[2] += float64(c.B)
					count++
				}
			}
			work[y*Width+x] = [3]float64{sum[0] / count, sum[1] / count, sum[2] / count}
		}
	}

	output := image.NewNRGBA(image.Rect(0, 0, Width, Height))
	for y := 0; y < Height; y++ {
		forward := y%2 == 0
		for step := 0; step < Width; step++ {
			x, dir := step, 1
			if !forward {
				x, dir = Width-1-step, -1
			}
			old := work[y*Width+x]
			ink := nearestPaletteColor(RGB{R: clampByte(old[0]), G: clampByte(old[1]), B: clampByte(old[2])}, TheoreticalE6Palette)
			output.SetNRGBA(x, y, color.NRGBA{R: ink.R, G: ink.G, B: ink.B, A: 255})
			diff := [3]float64{old[0] - float64(ink.R), old[1] - float64(ink.G), old[2] - float64(ink.B)}
			spread := func(dx, dy int, weight float64) {
				nx, ny := x+dx*dir, y+dy
				if nx < 0 || nx >= Width || ny >= Height {
					return
				}
				for channel := range diff {
					work[ny*Width+nx][channel] += diff[channel] * weight
				}
			}
			spread(1, 0, 7.0/16)
			spread(-1, 1, 3.0/16)
			spread(0, 1, 5.0/16)
			spread(1, 1, 1.0/16)
		}
	}

	white := color.NRGBA{R: 255, G: 255, B: 255, A: 255}
	for _, slot := range skinSlots() {
		for y := slot.Y0; y < slot.Y1; y++ {
			for x := slot.X0; x < slot.X1; x++ {
				if insideRoundedRect(x, y, slot, skinSlotRadius) {
					output.SetNRGBA(x, y, white)
				}
			}
		}
	}
	return output
}

func insideRoundedRect(x, y int, rect skinRect, radius int) bool {
	cx := min(max(x, rect.X0+radius), rect.X1-1-radius)
	cy := min(max(y, rect.Y0+radius), rect.Y1-1-radius)
	dx, dy := x-cx, y-cy
	return dx*dx+dy*dy <= radius*radius
}

func clampByte(value float64) uint8 {
	switch {
	case value < 0:
		return 0
	case value > 255:
		return 255
	default:
		return uint8(value + 0.5)
	}
}
