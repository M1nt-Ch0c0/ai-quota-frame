package dashboard

import (
	"bytes"
	"context"
	"fmt"
	"html/template"
	"image"
	"image/png"
	"io/fs"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/M1nt-Ch0c0/ai-quota-frame/internal/quota"
	"github.com/chromedp/chromedp"
)

const (
	Width  = 800
	Height = 480

	renderTimeout       = 20 * time.Second
	browserCloseTimeout = 5 * time.Second
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

func (renderer *Renderer) Render(snapshot quota.Snapshot) ([]byte, error) {
	renderer.mu.Lock()
	defer renderer.mu.Unlock()

	html, err := renderer.renderHTML(snapshot)
	if err != nil {
		return nil, err
	}
	raster, err := screenshotHTML(html)
	if err != nil {
		return nil, err
	}
	quantized, err := quantizePNGWithDitherRegions(raster.png, TheoreticalE6Palette, raster.ditherRegions)
	if err != nil {
		return nil, fmt.Errorf("quantize dashboard PNG: %w", err)
	}
	return quantized, nil
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

type frameRaster struct {
	png           []byte
	ditherRegions []image.Rectangle
}

func screenshotHTML(html string) (frameRaster, error) {
	workDir, err := os.MkdirTemp("", "ai-quota-frame-dashboard-*")
	if err != nil {
		return frameRaster{}, fmt.Errorf("create dashboard temp dir: %w", err)
	}
	defer os.RemoveAll(workDir)

	pagePath := filepath.Join(workDir, "frame.html")
	if err := os.WriteFile(pagePath, []byte(html), 0600); err != nil {
		return frameRaster{}, fmt.Errorf("write dashboard html: %w", err)
	}

	allocatorOptions := chromedp.DefaultExecAllocatorOptions[:]
	allocatorOptions = append(allocatorOptions,
		chromedp.Flag("headless", true),
		chromedp.Flag("disable-gpu", true),
		chromedp.Flag("hide-scrollbars", true),
		chromedp.Flag("no-sandbox", true),
		chromedp.Flag("disable-dev-shm-usage", true),
		// Subpixel (ClearType) text leaves red/blue fringes that survive
		// palette quantization as colored pixels around every glyph.
		chromedp.Flag("disable-lcd-text", true),
		chromedp.ModifyCmdFunc(prepareBrowserCommand),
	)
	if chromePath := chromeExecPath(); chromePath != "" {
		allocatorOptions = append(allocatorOptions, chromedp.ExecPath(chromePath))
	}

	allocatorCtx, cancelAllocator := chromedp.NewExecAllocator(context.Background(), allocatorOptions...)
	defer cancelAllocator()

	browserCtx, cancelBrowser := chromedp.NewContext(allocatorCtx)
	defer cancelBrowser()

	// chromedp binds the Chrome process to the context of the first Run and
	// SIGKILLs only the main process when that context ends. Close the browser
	// before the render deadline is cancelled so Chrome can stop its helpers.
	ctx, cancelTimeout := context.WithTimeout(browserCtx, renderTimeout)
	defer cancelTimeout()
	defer closeBrowser(browserCtx)

	fileURL := localFileURL(pagePath)
	var pngBytes []byte
	var artworkBounds []struct{ Left, Top, Right, Bottom int }
	if err := chromedp.Run(ctx,
		chromedp.EmulateViewport(int64(Width), int64(Height), chromedp.EmulateScale(1)),
		chromedp.Navigate(fileURL),
		chromedp.Evaluate(`Array.from(document.querySelectorAll("svg.provider-logo, [data-dither]"), artwork => {
          const r = artwork.getBoundingClientRect();
          return {left: Math.floor(r.left), top: Math.floor(r.top), right: Math.ceil(r.right), bottom: Math.ceil(r.bottom)};
        })`, &artworkBounds),
		chromedp.CaptureScreenshot(&pngBytes),
	); err != nil {
		return frameRaster{}, fmt.Errorf("render dashboard png: %w", err)
	}

	if _, err := png.Decode(bytes.NewReader(pngBytes)); err != nil {
		return frameRaster{}, fmt.Errorf("dashboard png invalid: %w", err)
	}
	raster := frameRaster{png: pngBytes}
	for _, bounds := range artworkBounds {
		raster.ditherRegions = append(raster.ditherRegions, image.Rect(bounds.Left, bounds.Top, bounds.Right, bounds.Bottom))
	}
	return raster, nil
}

// closeBrowser asks Chrome to exit gracefully, then kills whatever is left in
// its process group. Without this, helper processes orphaned by a hard kill
// accumulate on macOS with every render.
func closeBrowser(browserCtx context.Context) {
	c := chromedp.FromContext(browserCtx)
	if c == nil || c.Browser == nil {
		return
	}
	process := c.Browser.Process()
	closeCtx, cancel := context.WithTimeout(browserCtx, browserCloseTimeout)
	defer cancel()
	_ = chromedp.Cancel(closeCtx)
	if process != nil {
		killBrowserProcessGroup(process)
	}
}

func localFileURL(filePath string) string {
	slashPath := strings.ReplaceAll(filePath, `\`, "/")
	if len(slashPath) >= 2 && slashPath[1] == ':' {
		slashPath = "/" + slashPath
	}
	return (&url.URL{Scheme: "file", Path: slashPath}).String()
}

// CheckChrome verifies that a browser executable is available before the
// service starts its refresh loop. It intentionally does not launch Chrome;
// rendering remains the end-to-end browser check.
func CheckChrome() error {
	if chromeExecPath() == "" {
		return fmt.Errorf("Chromium or Google Chrome was not found; install one or set CHROME_BIN to an executable path")
	}
	return nil
}

func chromeExecPath() string {
	if path := strings.TrimSpace(os.Getenv("CHROME_BIN")); path != "" {
		resolved, err := exec.LookPath(path)
		if err != nil {
			return ""
		}
		return resolved
	}
	for _, name := range []string{"chromium", "chromium-browser", "google-chrome-stable", "google-chrome"} {
		if path, err := exec.LookPath(name); err == nil && strings.TrimSpace(path) != "" {
			return path
		}
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	patterns := []string{
		filepath.Join(home, ".cache", "ms-playwright", "chromium-*", "chrome-linux*", "chrome"),
		filepath.Join(home, ".cache", "ms-playwright", "chromium_headless_shell-*", "chrome-headless-shell-linux*", "chrome-headless-shell"),
	}
	for _, pattern := range patterns {
		found, _ := filepath.Glob(pattern)
		if len(found) == 0 {
			continue
		}
		sort.Strings(found)
		for index := len(found) - 1; index >= 0; index-- {
			if resolved, err := exec.LookPath(found[index]); err == nil {
				return resolved
			}
		}
	}
	return ""
}
