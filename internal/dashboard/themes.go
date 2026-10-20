package dashboard

import (
	"fmt"
	"html/template"
	"strings"
)

// Themes other than "classic" share themed.html and differ by stylesheet and
// by the pixel-art decorations drawn below. Every decoration is built from
// whole-pixel rectangles filled with pure inks or the 2x2 ink patterns
// declared in themeDefsSVG, so the frame stays noise-free after quantization.
var themeWordmarkScale = map[string]int{
	"mint":   5,
	"arcade": 5,
	"pop":    8,
}

func validTheme(theme string) bool {
	if theme == "classic" {
		return true
	}
	_, ok := themeWordmarkScale[theme]
	_, skin := skinThemes[theme]
	return ok || skin
}

// ParseTheme normalizes FRAME_THEME. Empty input selects the classic layout.
func ParseTheme(raw string) (string, error) {
	theme := strings.ToLower(strings.TrimSpace(raw))
	if theme == "" {
		return "classic", nil
	}
	if !validTheme(theme) {
		return "", fmt.Errorf("FRAME_THEME %q is not one of classic, mint, arcade, pop, mint-skin", raw)
	}
	return theme, nil
}

// themeDefsSVG declares the shared ink patterns once per page. Patterns use
// userSpaceOnUse, so they only stay pixel-exact inside SVGs drawn at 1:1.
const themeDefsSVG template.HTML = `<svg class="ink-defs" width="0" height="0" aria-hidden="true" focusable="false"><defs>` +
	`<pattern id="ink-mint" width="2" height="2" patternUnits="userSpaceOnUse"><rect width="2" height="2" fill="#ffffff"/><rect width="1" height="1" fill="#00ff00"/><rect x="1" y="1" width="1" height="1" fill="#00ff00"/></pattern>` +
	`<pattern id="ink-choco" width="2" height="2" patternUnits="userSpaceOnUse"><rect width="2" height="2" fill="#000000"/><rect width="1" height="1" fill="#ff0000"/><rect x="1" y="1" width="1" height="1" fill="#ff0000"/></pattern>` +
	`<pattern id="ink-orange" width="2" height="2" patternUnits="userSpaceOnUse"><rect width="2" height="2" fill="#ffff00"/><rect width="1" height="1" fill="#ff0000"/><rect x="1" y="1" width="1" height="1" fill="#ff0000"/></pattern>` +
	`</defs></svg>`

// pixelArt draws a grid of glyph rows. Each character maps to the attributes
// of its cells; '.' and unmapped characters stay transparent. Horizontal runs
// are merged so the markup stays small.
func pixelArt(rows []string, scale int, inks map[byte]string, class string) template.HTML {
	width := 0
	for _, row := range rows {
		if len(row) > width {
			width = len(row)
		}
	}
	var b strings.Builder
	fmt.Fprintf(&b, `<svg class="%s" width="%d" height="%d" viewBox="0 0 %d %d" shape-rendering="crispEdges" aria-hidden="true" focusable="false">`,
		class, width*scale, len(rows)*scale, width*scale, len(rows)*scale)
	for y, row := range rows {
		for x := 0; x < len(row); {
			ink, ok := inks[row[x]]
			if !ok {
				x++
				continue
			}
			run := 1
			for x+run < len(row) && row[x+run] == row[x] {
				run++
			}
			fmt.Fprintf(&b, `<rect x="%d" y="%d" width="%d" height="%d" %s/>`, x*scale, y*scale, run*scale, scale, ink)
			x += run
		}
	}
	b.WriteString(`</svg>`)
	return template.HTML(b.String())
}

var wordmarkGlyphs = map[rune][7]string{
	'M': {"1...1", "11.11", "1.1.1", "1.1.1", "1...1", "1...1", "1...1"},
	'I': {"11111", "..1..", "..1..", "..1..", "..1..", "..1..", "11111"},
	'N': {"1...1", "11..1", "1.1.1", "1..11", "1...1", "1...1", "1...1"},
	'T': {"11111", "..1..", "..1..", "..1..", "..1..", "..1..", "..1.."},
	'C': {".111.", "1...1", "1....", "1....", "1....", "1...1", ".111."},
	'H': {"1...1", "1...1", "1...1", "11111", "1...1", "1...1", "1...1"},
	'O': {".111.", "1...1", "1...1", "1...1", "1...1", "1...1", ".111."},
}

// pixelWordmark spells MINTCHOCO in a bold 5x7 bitmap face: every stroke is
// widened by one cell to the right. The first four letters use class wm-a,
// the rest wm-b, and a one-cell drop shadow uses wm-shadow.
func pixelWordmark(scale int) template.HTML {
	const text = "MINTCHOCO"
	const advance = 7
	width := len(text)*advance - 1
	grid := make([][]byte, 8)
	for y := range grid {
		grid[y] = []byte(strings.Repeat(".", width+1))
	}
	for index, letter := range text {
		glyph := wordmarkGlyphs[letter]
		mark := byte('a')
		if index >= 4 {
			mark = 'b'
		}
		for y, row := range glyph {
			for x := range row {
				if row[x] != '1' {
					continue
				}
				for _, gx := range []int{index*advance + x, index*advance + x + 1} {
					grid[y][gx] = mark
					if grid[y+1][gx+1] == '.' || grid[y+1][gx+1] == 's' {
						grid[y+1][gx+1] = 's'
					}
				}
			}
		}
	}
	rows := make([]string, len(grid))
	for y := range grid {
		rows[y] = string(grid[y])
	}
	return pixelArt(rows, scale, map[byte]string{
		'a': `class="wm-a"`,
		'b': `class="wm-b"`,
		's': `class="wm-shadow"`,
	}, "wordmark")
}

var starRows = []string{
	"....k....",
	"...kyk...",
	"...kyk...",
	"kkkkyykkk",
	"kyyyyyyyk",
	".kyyyyyk.",
	"..kyyyk..",
	".kyykyyk.",
	"kyk...kyk",
	"kk.....kk",
}

func pixelStar(scale int) template.HTML {
	return pixelArt(starRows, scale, map[byte]string{'k': `fill="#000000"`, 'y': `fill="#ffff00"`}, "pixel-star")
}

// coneRows is a mint-choco-chip scoop on a waffle cone.
var coneRows = []string{
	"...kkkk...",
	".kkmmmmkk.",
	"kmmcmmmmmk",
	"kmmmmmmcmk",
	"kmcmmmmmmk",
	"kkkkkkkkkk",
	".kyoyoyok.",
	".koyoyoyk.",
	"..kyoyok..",
	"..koyoyk..",
	"...kyok...",
	"...koyk...",
	"....kk....",
}

// mascotRows is the larger cone with a happy face and rosy cheeks.
var mascotRows = []string{
	".....kkkk.....",
	"...kkmmmmkk...",
	"..kmmmmmcmmk..",
	".kmcmmmmmmmmk.",
	".kmmmmmmmmcmk.",
	"kmmmkkmmkkmmmk",
	"kmmmkkmmkkmmmk",
	"kmrrmmmmmmrrmk",
	"kmmmmmkkmmmmmk",
	"kkkkkkkkkkkkkk",
	".kyoyoyoyoyok.",
	".koyoyoyoyoyk.",
	"..kyoyoyoyok..",
	"..koyoyoyoyk..",
	"...kyoyoyok...",
	"...koyoyoyk...",
	"....kyoyok....",
	".....koyk.....",
	"......kk......",
}

var coneInks = map[byte]string{
	'k': `fill="#000000"`,
	'm': `fill="url(#ink-mint)"`,
	'c': `fill="url(#ink-choco)"`,
	'r': `fill="#ff0000"`,
	'y': `fill="#ffff00"`,
	'o': `fill="url(#ink-orange)"`,
}

func pixelCone(scale int, face bool) template.HTML {
	if face {
		return pixelArt(mascotRows, scale, coneInks, "pixel-mascot")
	}
	return pixelArt(coneRows, scale, coneInks, "pixel-cone")
}

// meltDrips draws chocolate drips hanging from the header band. The drip
// layout is fixed so frames only change when the data does.
func meltDrips(width int) template.HTML {
	type drip struct{ x, w, h int }
	drips := []drip{}
	widths := []int{14, 10, 18, 8, 12, 16, 10, 20, 12, 8}
	heights := []int{18, 10, 26, 8, 14, 22, 12, 16, 24, 9}
	x, index := 6, 0
	for x < width-24 {
		w, h := widths[index%len(widths)], heights[(index*7)%len(heights)]
		drips = append(drips, drip{x, w, h})
		x += w + 18 + (index*13)%26
		index++
	}
	var b strings.Builder
	fmt.Fprintf(&b, `<svg class="melt-drips" width="%d" height="32" viewBox="0 0 %d 32" shape-rendering="crispEdges" aria-hidden="true" focusable="false">`, width, width)
	fmt.Fprintf(&b, `<rect width="%d" height="4" fill="url(#ink-choco)"/>`, width)
	for _, d := range drips {
		// a 2px black rim keeps the drip edge crisp against the mint ground
		fmt.Fprintf(&b, `<rect x="%d" y="0" width="%d" height="%d" fill="#000000"/>`, d.x-2, d.w+4, d.h)
		fmt.Fprintf(&b, `<rect x="%d" y="%d" width="%d" height="4" fill="#000000"/>`, d.x, d.h, d.w)
		fmt.Fprintf(&b, `<rect x="%d" y="0" width="%d" height="%d" fill="url(#ink-choco)"/>`, d.x, d.w, d.h)
		fmt.Fprintf(&b, `<rect x="%d" y="%d" width="%d" height="2" fill="url(#ink-choco)"/>`, d.x+2, d.h, d.w-4)
	}
	b.WriteString(`</svg>`)
	return template.HTML(b.String())
}

// chocoChips scatters chips over the mint ground with a fixed layout.
func chocoChips(width, height int) template.HTML {
	var b strings.Builder
	fmt.Fprintf(&b, `<svg class="choco-chips" width="%d" height="%d" viewBox="0 0 %d %d" shape-rendering="crispEdges" aria-hidden="true" focusable="false">`, width, height, width, height)
	for index := 0; index < 90; index++ {
		x := (index*137 + 29) % width
		y := (index*89 + 61) % height
		size := 4 + (index%3)*2
		fmt.Fprintf(&b, `<rect x="%d" y="%d" width="%d" height="%d" fill="#000000"/>`, x-1, y-1, size+2, size+2)
		fmt.Fprintf(&b, `<rect x="%d" y="%d" width="%d" height="%d" fill="url(#ink-choco)"/>`, x, y, size, size)
	}
	b.WriteString(`</svg>`)
	return template.HTML(b.String())
}

type themeArt struct {
	Defs     template.HTML
	Wordmark template.HTML
	Star     template.HTML
	Cone     template.HTML
	Mascot   template.HTML
	Drips    template.HTML
	Chips    template.HTML
}

func buildThemeArt(theme string) themeArt {
	art := themeArt{Defs: themeDefsSVG, Wordmark: pixelWordmark(themeWordmarkScale[theme])}
	switch theme {
	case "mint":
		art.Cone = pixelCone(2, false)
		art.Mascot = pixelCone(3, true)
		art.Drips = meltDrips(Width)
		art.Chips = chocoChips(Width, Height)
	case "arcade":
		art.Star = pixelStar(3)
	}
	return art
}
