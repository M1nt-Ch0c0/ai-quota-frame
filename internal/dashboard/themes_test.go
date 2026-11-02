package dashboard

import (
	"fmt"
	"image"
	"image/color"
	"strings"
	"testing"
	"time"

	"github.com/M1nt-Ch0c0/ai-quota-frame/internal/quota"
)

func TestParseTheme(t *testing.T) {
	for raw, want := range map[string]string{"": "classic", " Mint ": "mint-skin", "arcade": "arcade", "pop": "pop", "mint-skin": "mint-skin"} {
		if got, err := ParseTheme(raw); err != nil || got != want {
			t.Errorf("ParseTheme(%q) = %q, %v; want %q", raw, got, err, want)
		}
	}
	if _, err := ParseTheme("neon"); err == nil {
		t.Fatal("ParseTheme accepted an unknown theme")
	}
}

func TestEveryThemeRendersLiveData(t *testing.T) {
	expires := time.Date(2026, 10, 23, 5, 3, 0, 0, time.UTC)
	snapshot := testSnapshot()
	snapshot.Accounts = append(snapshot.Accounts, quota.Account{
		Provider: "codex", Status: "ok",
		Windows:    []quota.Window{{ID: "7d", Label: "7d", RemainingPercent: quota.Percent(67)}},
		ResetCards: []quota.ResetCard{{ExpiresAt: &expires}},
	})
	for _, theme := range []string{"classic", "arcade", "pop", "mint-skin"} {
		renderer, err := New(time.UTC)
		if err != nil {
			t.Fatal(err)
		}
		if err := renderer.SetTheme(theme); err != nil {
			t.Fatal(err)
		}
		html, err := renderer.renderHTML(snapshot)
		if err != nil {
			t.Fatalf("%s: renderHTML() error = %v", theme, err)
		}
		for _, needle := range []string{"CODEX", "<style>", "10-23"} {
			if !strings.Contains(html, needle) {
				t.Errorf("%s: rendered HTML missing %q", theme, needle)
			}
		}
		if theme == "mint-skin" && !strings.Contains(html, "data:image/png;base64,") {
			t.Error("mint-skin: background artwork is not inlined")
		}
	}
}

func TestBakeSkinUsesOnlyInksAndClearsSlots(t *testing.T) {
	art := testGradientImage(160, 96)
	baked := BakeSkin(art)
	if baked.Bounds().Dx() != Width || baked.Bounds().Dy() != Height {
		t.Fatalf("baked size = %v", baked.Bounds())
	}
	inks := map[[3]uint8]bool{}
	for _, entry := range TheoreticalE6Palette.entries() {
		inks[[3]uint8{entry.color.R, entry.color.G, entry.color.B}] = true
	}
	for y := 0; y < Height; y++ {
		for x := 0; x < Width; x++ {
			c := baked.NRGBAAt(x, y)
			if !inks[[3]uint8{c.R, c.G, c.B}] || c.A != 255 {
				t.Fatalf("pixel (%d,%d) = %v is not an opaque ink", x, y, c)
			}
		}
	}
	for _, slot := range skinSlots() {
		cx, cy := (slot.X0+slot.X1)/2, (slot.Y0+slot.Y1)/2
		if c := baked.NRGBAAt(cx, cy); c.R != 255 || c.G != 255 || c.B != 255 {
			t.Fatalf("slot %+v centre = %v, want white", slot, c)
		}
	}
}

func testGradientImage(width, height int) *image.NRGBA {
	art := image.NewNRGBA(image.Rect(0, 0, width, height))
	for y := 0; y < height; y++ {
		for x := 0; x < width; x++ {
			art.SetNRGBA(x, y, color.NRGBA{R: uint8(x * 255 / width), G: uint8(y * 255 / height), B: 120, A: 255})
		}
	}
	return art
}

func TestMintSkinCompactsFourAndFiveRows(t *testing.T) {
	for _, list := range []string{"codex,claude,xai,kimi", "codex,claude,xai,kimi,antigravity"} {
		renderer, err := New(time.UTC)
		if err != nil {
			t.Fatal(err)
		}
		if err := renderer.SetTheme("mint-skin"); err != nil {
			t.Fatal(err)
		}
		providers, err := ParseDisplayProviders(list)
		if err != nil {
			t.Fatal(err)
		}
		renderer.SetDisplayProviders(providers)
		html, err := renderer.renderHTML(testSnapshot())
		if err != nil {
			t.Fatal(err)
		}
		rows := len(providers)
		if strings.Contains(html, `class="brand-title"`) || !strings.Contains(html, "data:image/png;base64,") ||
			!strings.Contains(html, fmt.Sprintf("rows-%d", rows)) || !strings.Contains(html, `class="row-band"`) ||
			strings.Count(html, `class="slot row `) != rows {
			t.Fatalf("%d rows on mint-skin did not use the compact skin layout", rows)
		}
	}
}

func TestSkinRowLayoutStaysInsideBakedBand(t *testing.T) {
	if got := skinRowLayout(3); len(got) != 3 || got[0] != skinRowSlots[0] {
		t.Fatal("three rows must keep the baked slots")
	}
	top, bottom := skinRowSlots[0].Y0, skinRowSlots[2].Y1
	for _, n := range []int{4, 5} {
		slots := skinRowLayout(n)
		if len(slots) != n || slots[0].Y0 != top || slots[n-1].Y1 != bottom {
			t.Fatalf("%d rows: %+v", n, slots)
		}
		for i := 1; i < n; i++ {
			if slots[i].Y0 <= slots[i-1].Y1 || slots[i].Y1-slots[i].Y0 < 36 {
				t.Fatalf("%d rows overlap or are too short: %+v", n, slots)
			}
		}
	}
}
