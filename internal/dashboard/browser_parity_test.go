//go:build !js

package dashboard

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"image"
	"image/png"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/M1nt-Ch0c0/ai-quota-frame/internal/quota"
	"github.com/chromedp/cdproto/runtime"
	"github.com/chromedp/chromedp"
)

// This integration test compares pixels, not DOM descriptions or palette
// validity. Run after building web/dist/raster-test.js; it never uses hardware
// or production credentials.
func TestBrowserRasterMatchesNative(t *testing.T) {
	if os.Getenv("PHOTOFRAME_WEB_PARITY") != "1" {
		t.Skip("set PHOTOFRAME_WEB_PARITY=1 for native/browser pixel parity")
	}
	if !chromeAvailable() {
		t.Fatal("Chrome is required for renderer parity")
	}
	bundle, err := os.ReadFile("../../web/dist/raster-test.js")
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/raster.js" {
			w.Header().Set("Content-Type", "text/javascript")
			_, _ = w.Write(bundle)
			return
		}
		w.Header().Set("Content-Type", "text/html")
		_, _ = w.Write([]byte(`<!doctype html><html><body><script src="/raster.js"></script></body></html>`))
	}))
	defer server.Close()
	options := append(chromedp.DefaultExecAllocatorOptions[:], chromedp.ExecPath(chromeExecPath()), chromedp.Flag("disable-gpu", true), chromedp.Flag("disable-lcd-text", true), chromedp.Flag("hide-scrollbars", true))
	allocator, cancelAllocator := chromedp.NewExecAllocator(context.Background(), options...)
	defer cancelAllocator()
	browser, cancelBrowser := chromedp.NewContext(allocator)
	defer cancelBrowser()
	defer closeBrowser(browser)
	ctx, cancel := context.WithTimeout(browser, 2*time.Minute)
	defer cancel()
	if err := chromedp.Run(ctx, chromedp.EmulateViewport(800, 480, chromedp.EmulateScale(1)), chromedp.Navigate(server.URL)); err != nil {
		t.Fatal(err)
	}
	providers, err := ParseDisplayProviders("claude:CLAUDE,codex:CODEX,xai:GROK")
	if err != nil {
		t.Fatal(err)
	}
	location, err := time.LoadLocation("Asia/Shanghai")
	if err != nil {
		t.Fatal(err)
	}
	for _, theme := range []string{"mint-skin", "classic", "mint", "arcade", "pop"} {
		for _, scene := range []struct {
			name     string
			snapshot quota.Snapshot
		}{
			{"one-provider", testSnapshot()}, {"three-providers", allProviderSnapshot()},
		} {
			t.Run(theme+"/"+scene.name, func(t *testing.T) {
				renderer, err := New(location)
				if err != nil {
					t.Fatal(err)
				}
				renderer.SetDisplayProviders(providers)
				if err := renderer.SetTheme(theme); err != nil {
					t.Fatal(err)
				}
				snapshot := scene.snapshot
				html, err := renderer.RenderHTML(snapshot)
				if err != nil {
					t.Fatal(err)
				}
				nativeRaster, err := screenshotHTML(html)
				if err != nil {
					t.Fatal(err)
				}
				native, err := QuantizeBrowserRaster(nativeRaster.png, nativeRaster.ditherRegions)
				if err != nil {
					t.Fatal(err)
				}
				encodedHTML, _ := json.Marshal(html)
				expression := `(async () => {const result=await FrameRasterTesting.rasterFrameHTML(` + string(encodedHTML) + `);let binary='';for(const byte of result.png)binary+=String.fromCharCode(byte);return {png:btoa(binary),bounds:result.bounds};})()`
				var raster struct {
					PNG    string
					Bounds []struct{ Left, Top, Right, Bottom int }
				}
				if err := chromedp.Run(ctx, chromedp.Evaluate(expression, &raster, func(p *runtime.EvaluateParams) *runtime.EvaluateParams { return p.WithAwaitPromise(true) })); err != nil {
					t.Fatal(err)
				}
				pixels, err := base64.StdEncoding.DecodeString(raster.PNG)
				if err != nil {
					t.Fatal(err)
				}
				regions := make([]image.Rectangle, len(raster.Bounds))
				for i, b := range raster.Bounds {
					regions[i] = image.Rect(b.Left, b.Top, b.Right, b.Bottom)
				}
				web, err := QuantizeBrowserRaster(pixels, regions)
				if err != nil {
					t.Fatal(err)
				}
				if directory := os.Getenv("PHOTOFRAME_PARITY_OUTPUT"); directory != "" {
					if err := os.MkdirAll(directory, 0700); err != nil {
						t.Fatal(err)
					}
					_ = os.WriteFile(filepath.Join(directory, theme+"-"+scene.name+"-native.png"), native, 0600)
					_ = os.WriteFile(filepath.Join(directory, theme+"-"+scene.name+"-browser.png"), web, 0600)
					_ = os.WriteFile(filepath.Join(directory, theme+"-"+scene.name+"-native-raster.png"), nativeRaster.png, 0600)
					_ = os.WriteFile(filepath.Join(directory, theme+"-"+scene.name+"-browser-raster.png"), pixels, 0600)
				}
				left, err := png.Decode(bytes.NewReader(native))
				if err != nil {
					t.Fatal(err)
				}
				right, err := png.Decode(bytes.NewReader(web))
				if err != nil {
					t.Fatal(err)
				}
				leftRaster, err := png.Decode(bytes.NewReader(nativeRaster.png))
				if err != nil {
					t.Fatal(err)
				}
				rightRaster, err := png.Decode(bytes.NewReader(pixels))
				if err != nil {
					t.Fatal(err)
				}
				different, textAntialias := 0, 0
				for y := 0; y < Height; y++ {
					for x := 0; x < Width; x++ {
						lr, lg, lb, la := left.At(x, y).RGBA()
						rr, rg, rb, ra := right.At(x, y).RGBA()
						if lr != rr || lg != rg || lb != rb || la != ra {
							different++
							if onlyTextAntialiasRounding(leftRaster, rightRaster, regions, x, y) {
								textAntialias++
							}
						}
					}
				}
				// Chromium's screenshot and SVG-image text pipelines round a few
				// 9px skyline-date glyph edges by 2/255 on macOS. Permit only that
				// measured grayscale boundary, never a layout/color/dither change.
				if theme == "mint-skin" && different > 0 && different <= 8 && textAntialias == different {
					t.Logf("same layout/artwork/data; %d of %d pixels differ only from <=2/255 grayscale glyph rounding", different, Width*Height)
				} else if different != 0 {
					t.Errorf("browser differs at %d / %d pixels (%.3f%%)", different, Width*Height, float64(different)*100/(Width*Height))
				} else {
					t.Logf("pixel-identical: %d pixels; PNG native=%d bytes browser=%d bytes", Width*Height, len(native), len(web))
				}
			})
		}
	}
}

func allProviderSnapshot() quota.Snapshot {
	snapshot := testSnapshot()
	reset := snapshot.DataUpdatedAt.Add(7 * 24 * time.Hour)
	snapshot.Accounts = append(snapshot.Accounts,
		quota.Account{Provider: "claude", Name: "claude@example.test", Plan: "max", Status: "ok", Windows: []quota.Window{
			{ID: "5h", Label: "5h", RemainingPercent: quota.Percent(81), ResetsAt: &reset, Source: "test"},
			{ID: "7d", Label: "7d", RemainingPercent: quota.Percent(54), ResetsAt: &reset, Source: "test"},
		}},
		quota.Account{Provider: "xai", Name: "grok@example.test", Plan: "heavy", Status: "low", Windows: []quota.Window{
			{ID: "7d", Label: "7d", RemainingPercent: quota.Percent(7), ResetsAt: &reset, Source: "test"},
		}},
	)
	return snapshot
}

func onlyTextAntialiasRounding(left, right image.Image, artwork []image.Rectangle, x, y int) bool {
	for _, region := range artwork {
		if image.Pt(x, y).In(region) {
			return false
		}
	}
	lr, lg, lb, la := left.At(x, y).RGBA()
	rr, rg, rb, ra := right.At(x, y).RGBA()
	if lr != lg || lg != lb || rr != rg || rg != rb || la != 65535 || ra != 65535 {
		return false
	}
	difference := int(lr>>8) - int(rr>>8)
	if difference < 0 {
		difference = -difference
	}
	return difference <= 2
}
