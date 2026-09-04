package dashboard

import (
	"bytes"
	"context"
	"fmt"
	"html/template"
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
)

type Renderer struct {
	mu        sync.Mutex
	location  *time.Location
	tmpl      *template.Template
	providers []DisplayProvider
}

func New(location *time.Location) (*Renderer, error) {
	if location == nil {
		location = time.Local
	}
	tmpl, err := template.New("frame.html").ParseFS(templateFS, "templates/frame.html")
	if err != nil {
		return nil, fmt.Errorf("parse dashboard template: %w", err)
	}
	return &Renderer{location: location, tmpl: tmpl, providers: DefaultDisplayProviders()}, nil
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
	payload, err := screenshotHTML(html)
	if err != nil {
		return nil, err
	}
	quantized, err := QuantizeTheoreticalE6(payload)
	if err != nil {
		return nil, fmt.Errorf("quantize dashboard PNG: %w", err)
	}
	return quantized, nil
}

func (renderer *Renderer) renderHTML(snapshot quota.Snapshot) (string, error) {
	data := buildFrameData(snapshot, renderer.location, renderer.providers)
	var buffer bytes.Buffer
	if err := renderer.tmpl.Execute(&buffer, data); err != nil {
		return "", fmt.Errorf("execute dashboard template: %w", err)
	}
	return inlineStylesheet(buffer.String())
}

func inlineStylesheet(html string) (string, error) {
	css, err := fs.ReadFile(templateFS, "templates/frame.css")
	if err != nil {
		return "", fmt.Errorf("read dashboard stylesheet: %w", err)
	}
	linked := `<link rel="stylesheet" href="frame.css">`
	inlined := fmt.Sprintf("<style>%s</style>", css)
	if !strings.Contains(html, linked) {
		return "", fmt.Errorf("dashboard template missing stylesheet link")
	}
	return strings.Replace(html, linked, inlined, 1), nil
}

func screenshotHTML(html string) ([]byte, error) {
	workDir, err := os.MkdirTemp("", "ai-quota-frame-dashboard-*")
	if err != nil {
		return nil, fmt.Errorf("create dashboard temp dir: %w", err)
	}
	defer os.RemoveAll(workDir)

	pagePath := filepath.Join(workDir, "frame.html")
	if err := os.WriteFile(pagePath, []byte(html), 0600); err != nil {
		return nil, fmt.Errorf("write dashboard html: %w", err)
	}

	allocatorOptions := chromedp.DefaultExecAllocatorOptions[:]
	allocatorOptions = append(allocatorOptions,
		chromedp.Flag("headless", true),
		chromedp.Flag("disable-gpu", true),
		chromedp.Flag("hide-scrollbars", true),
		chromedp.Flag("no-sandbox", true),
		chromedp.Flag("disable-dev-shm-usage", true),
	)
	if chromePath := chromeExecPath(); chromePath != "" {
		allocatorOptions = append(allocatorOptions, chromedp.ExecPath(chromePath))
	}

	allocatorCtx, cancelAllocator := chromedp.NewExecAllocator(context.Background(), allocatorOptions...)
	defer cancelAllocator()

	ctx, cancelBrowser := chromedp.NewContext(allocatorCtx)
	defer cancelBrowser()

	ctx, cancelTimeout := context.WithTimeout(ctx, 20*time.Second)
	defer cancelTimeout()

	fileURL := localFileURL(pagePath)
	var pngBytes []byte
	if err := chromedp.Run(ctx,
		chromedp.EmulateViewport(int64(Width), int64(Height), chromedp.EmulateScale(1)),
		chromedp.Navigate(fileURL),
		chromedp.CaptureScreenshot(&pngBytes),
	); err != nil {
		return nil, fmt.Errorf("render dashboard png: %w", err)
	}

	if _, err := png.Decode(bytes.NewReader(pngBytes)); err != nil {
		return nil, fmt.Errorf("dashboard png invalid: %w", err)
	}
	return pngBytes, nil
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
