package dashboard

import (
	"html/template"
	"regexp"
	"strings"
	"testing"
)

func TestProviderLogoSVGAliases(t *testing.T) {
	tests := []struct {
		want string
		ids  []string
	}{
		{want: `data-logo="openai"`, ids: []string{"codex", "OpenAI", " chatgpt "}},
		{want: `data-logo="grok"`, ids: []string{"xai", "x-ai", "GROK"}},
		{want: `data-logo="kimi"`, ids: []string{"kimi", "moonshot", "moonshot_ai", "MoonshotAI"}},
	}
	for _, test := range tests {
		for _, id := range test.ids {
			svg := string(ProviderLogoSVG(id))
			if !strings.Contains(svg, test.want) {
				t.Errorf("ProviderLogoSVG(%q) = %q, want %s", id, svg, test.want)
			}
		}
	}
}

func TestProviderLogoSVGIsSelfContainedBlackAndWhite(t *testing.T) {
	colorPattern := regexp.MustCompile(`(?i)#[0-9a-f]{6}`)
	for _, id := range []string{"codex", "xai", "kimi", "unknown"} {
		svg := string(ProviderLogoSVG(id))
		lower := strings.ToLower(svg)
		if !strings.HasPrefix(svg, "<svg") || !strings.HasSuffix(svg, "</svg>") {
			t.Errorf("ProviderLogoSVG(%q) is not a complete inline SVG: %q", id, svg)
		}
		for _, forbidden := range []string{"http:", "https:", "href=", "<image", "<script", "<style", "filter=", "mask=", "opacity=", "gradient"} {
			if strings.Contains(lower, forbidden) {
				t.Errorf("ProviderLogoSVG(%q) contains forbidden %q", id, forbidden)
			}
		}
		for _, color := range colorPattern.FindAllString(lower, -1) {
			if color != "#000000" && color != "#ffffff" {
				t.Errorf("ProviderLogoSVG(%q) contains non-palette color %q", id, color)
			}
		}
	}
}

func TestProviderLogoSVGUnknownUsesSafeFallback(t *testing.T) {
	untrusted := `<script>alert("logo")</script>`
	got := ProviderLogoSVG(untrusted)
	if got != fallbackLogoSVG {
		t.Fatalf("unknown provider did not use fallback: %q", got)
	}
	if strings.Contains(string(got), untrusted) || strings.Contains(string(got), "script") {
		t.Fatalf("fallback includes caller-provided content: %q", got)
	}
	if _, ok := any(got).(template.HTML); !ok {
		t.Fatalf("ProviderLogoSVG returns %T, want template.HTML", got)
	}
}
