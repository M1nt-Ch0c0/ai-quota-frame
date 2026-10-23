package dashboard

import (
	"fmt"
	"html"
	"html/template"
	"math"
	"strings"
)

// cityChartSVG is the selected seven-day skyline. Building height encodes the
// existing normalized usage; windows and the peak flag stay inside the plot.
func cityChartSVG(days []usageDayData) template.HTML {
	var s strings.Builder
	s.WriteString(`<svg class="city-chart" width="320" height="112" viewBox="0 0 320 112" role="img" aria-label="Seven day token usage skyline">`)
	s.WriteString(`<path d="M2 96H318" stroke="#000" stroke-width="1" stroke-dasharray="1 3"/>`)
	for index, day := range days {
		cx := 22 + index*46
		height := int(math.Round(float64(day.Height) * .70))
		fmt.Fprintf(&s, `<text x="%d" y="9" text-anchor="middle" font-family="monospace" font-size="10" font-weight="700" fill="#000">%s</text>`, cx, html.EscapeString(day.Value))
		if height > 0 {
			fill := "#5a727c"
			if day.Peak {
				fill = "#263f51"
			}
			top := 94 - height
			s.WriteString(`<g data-dither shape-rendering="crispEdges">`)
			cityRect(&s, cx-13, top, 26, height, fill)
			for y := top + 5; y < 90; y += 7 {
				for x := cx - 9; x <= cx+7; x += 8 {
					tone := "#fff"
					if (x+y)%3 == 0 {
						tone = "#ffff00"
					}
					cityRect(&s, x, y, 4, min(3, 94-y), tone)
				}
			}
			if day.Peak {
				cityRect(&s, cx-8, top-5, 16, 5, fill)
				cityRect(&s, cx-1, top-10, 1, 5, "#000")
				cityRect(&s, cx, top-10, 6, 3, "#bf9650")
			} else if height > 12 && index%2 == 0 {
				cityRect(&s, cx-6, top-3, 7, 3, fill)
			}
			s.WriteString(`</g>`)
		}
		fmt.Fprintf(&s, `<text x="%d" y="110" text-anchor="middle" font-family="monospace" font-size="9" font-weight="700" fill="#000">%s</text>`, cx, html.EscapeString(day.Label))
	}
	s.WriteString(`</svg>`)
	return template.HTML(s.String())
}

func cityRect(s *strings.Builder, x, y, width, height int, fill string) {
	if width > 0 && height > 0 {
		fmt.Fprintf(s, `<rect x="%d" y="%d" width="%d" height="%d" fill="%s"/>`, x, y, width, height, fill)
	}
}
