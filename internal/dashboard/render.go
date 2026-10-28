package dashboard

import (
	"bytes"
	"fmt"
	"html/template"
	"io/fs"
	"strings"
	"sync"
	"time"

	"github.com/M1nt-Ch0c0/ai-quota-frame/internal/quota"
)

const (
	Width  = 800
	Height = 480
)

type Renderer struct {
	mu        sync.Mutex
	location  *time.Location
	tmpl      *template.Template
	themed    *template.Template
	skin      *template.Template
	theme     string
	providers []DisplayProvider
}

type themedFrameData struct {
	frameData
	Theme string
	Art   themeArt
}

func New(location *time.Location) (*Renderer, error) {
	if location == nil {
		location = time.Local
	}
	tmpl, err := template.New("frame.html").ParseFS(templateFS, "templates/frame.html")
	if err != nil {
		return nil, fmt.Errorf("parse dashboard template: %w", err)
	}
	themed, err := template.New("themed.html").ParseFS(templateFS, "templates/themed.html")
	if err != nil {
		return nil, fmt.Errorf("parse themed dashboard template: %w", err)
	}
	skin, err := template.New("skin.html").ParseFS(templateFS, "templates/skin.html")
	if err != nil {
		return nil, fmt.Errorf("parse skin dashboard template: %w", err)
	}
	return &Renderer{location: location, tmpl: tmpl, themed: themed, skin: skin, theme: "classic", providers: DefaultDisplayProviders()}, nil
}

// SetTheme selects the frame look; see ParseTheme for the accepted names.
func (renderer *Renderer) SetTheme(theme string) error {
	theme, err := ParseTheme(theme)
	if err != nil {
		return err
	}
	renderer.mu.Lock()
	renderer.theme = theme
	renderer.mu.Unlock()
	return nil
}

func (renderer *Renderer) SetDisplayProviders(providers []DisplayProvider) {
	if len(providers) == 0 {
		providers = DefaultDisplayProviders()
	}
	copied := append([]DisplayProvider(nil), providers...)
	renderer.mu.Lock()
	renderer.providers = copied
	renderer.mu.Unlock()
}

// RenderHTML builds the exact document consumed by both native Chrome and the
// browser plugin. Data aggregation, theme assets and text are shared code.
func (renderer *Renderer) RenderHTML(snapshot quota.Snapshot) (string, error) {
	renderer.mu.Lock()
	defer renderer.mu.Unlock()
	return renderer.renderHTML(snapshot)
}

func (renderer *Renderer) renderHTML(snapshot quota.Snapshot) (string, error) {
	data := buildFrameData(snapshot, renderer.location, renderer.providers)
	var buffer bytes.Buffer
	if renderer.theme == "classic" {
		if err := renderer.tmpl.Execute(&buffer, data); err != nil {
			return "", fmt.Errorf("execute dashboard template: %w", err)
		}
		return inlineStylesheet(buffer.String(), "templates/frame.css")
	}
	theme := renderer.theme
	if look, isSkin := skinThemes[theme]; isSkin {
		if len(data.Rows) <= len(skinRowSlots) {
			return renderer.renderSkin(data, theme, look)
		}
		// The skin artwork only has room for three rows.
		theme = look
	}
	themed := themedFrameData{frameData: data, Theme: theme, Art: buildThemeArt(theme)}
	if err := renderer.themed.Execute(&buffer, themed); err != nil {
		return "", fmt.Errorf("execute themed dashboard template: %w", err)
	}
	return inlineStylesheet(buffer.String(), "templates/themed.css", "templates/theme-"+theme+".css")
}

func (renderer *Renderer) renderSkin(data frameData, theme, look string) (string, error) {
	background, err := skinBackground(theme)
	if err != nil {
		return "", err
	}
	skin := skinFrameData{
		frameData:  data,
		Theme:      theme,
		Art:        buildThemeArt(look),
		Background: background,
		ClockStyle: skinClockSlot.Style(),
		UsageStyle: skinUsageSlot.Style(),
	}
	for _, slot := range skinRowSlots {
		skin.RowStyles = append(skin.RowStyles, slot.Style())
	}
	var buffer bytes.Buffer
	if err := renderer.skin.Execute(&buffer, skin); err != nil {
		return "", fmt.Errorf("execute skin dashboard template: %w", err)
	}
	return inlineStylesheet(buffer.String(), "templates/skin.css")
}

func inlineStylesheet(html string, paths ...string) (string, error) {
	var css []byte
	for _, path := range paths {
		part, err := fs.ReadFile(templateFS, path)
		if err != nil {
			return "", fmt.Errorf("read dashboard stylesheet: %w", err)
		}
		css = append(css, part...)
		css = append(css, '\n')
	}
	linked := `<link rel="stylesheet" href="frame.css">`
	inlined := fmt.Sprintf("<style>%s</style>", css)
	if !strings.Contains(html, linked) {
		return "", fmt.Errorf("dashboard template missing stylesheet link")
	}
	return strings.Replace(html, linked, inlined, 1), nil
}
