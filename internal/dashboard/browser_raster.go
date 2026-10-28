package dashboard

import (
	"bytes"
	"fmt"
	"image"
	"image/png"
)

// QuantizeBrowserRaster finishes a browser-rendered copy of RenderHTML using
// the exact palette, compositing, artwork dithering and PNG encoder used by
// Renderer.Render. The browser supplies only layout-derived artwork bounds.
func QuantizeBrowserRaster(raster []byte, regions []image.Rectangle) ([]byte, error) {
	if len(raster) == 0 || len(raster) > 5<<20 || len(regions) > 1024 {
		return nil, fmt.Errorf("invalid frame raster size")
	}
	config, err := png.DecodeConfig(bytes.NewReader(raster))
	if err != nil || config.Width != Width || config.Height != Height {
		return nil, fmt.Errorf("frame raster must be an 800x480 PNG")
	}
	return quantizePNGWithDitherRegions(raster, TheoreticalE6Palette, regions)
}
