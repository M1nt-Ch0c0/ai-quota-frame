package dashboard

import (
	"html/template"
	"strings"
)

// These logos are intentionally inline, monochrome SVGs. Keeping the geometry
// local avoids network access during frame rendering, while pure #000000 and
// #ffffff output maps directly onto the Spectra-6 palette.
//
// Sources:
//   - OpenAI: https://github.com/openai/openai-assistants-quickstart/blob/main/public/openai.svg
//     (official OpenAI repository; see https://openai.com/brand/ for usage terms)
//   - Grok: Grok_Logomark_Dark.svg from the official xAI brand archive at
//     https://data.x.ai/logos/xAI_Grok_Assets.zip
//     (see https://x.ai/legal/brand-guidelines for usage terms)
//   - Kimi: https://github.com/simple-icons/simple-icons/blob/develop/icons/kimi.svg
//     (CC0 Simple Icons geometry, sourced there from MoonshotAI's official
//     https://moonshotai.github.io/Branding-Guide)
const (
	openAILogoSVG template.HTML = `<svg class="provider-logo" data-logo="openai" width="40" height="40" viewBox="0 0 32 32" fill="#000000" role="img" aria-label="OpenAI" focusable="false"><path d="M29.71,13.09A8.09,8.09,0,0,0,20.34,2.68a8.08,8.08,0,0,0-13.7,2.9A8.08,8.08,0,0,0,2.3,18.9,8,8,0,0,0,3,25.45a8.08,8.08,0,0,0,8.69,3.87,8,8,0,0,0,6,2.68,8.09,8.09,0,0,0,7.7-5.61,8,8,0,0,0,5.33-3.86A8.09,8.09,0,0,0,29.71,13.09Zm-12,16.82a6,6,0,0,1-3.84-1.39l.19-.11,6.37-3.68a1,1,0,0,0,.53-.91v-9l2.69,1.56a.08.08,0,0,1,.05.07v7.44A6,6,0,0,1,17.68,29.91ZM4.8,24.41a6,6,0,0,1-.71-4l.19.11,6.37,3.68a1,1,0,0,0,1,0l7.79-4.49V22.8a.09.09,0,0,1,0,.08L13,26.6A6,6,0,0,1,4.8,24.41ZM3.12,10.53A6,6,0,0,1,6.28,7.9v7.57a1,1,0,0,0,.51.9l7.75,4.47L11.85,22.4a.14.14,0,0,1-.09,0L5.32,18.68a6,6,0,0,1-2.2-8.18Zm22.13,5.14-7.78-4.52L20.16,9.6a.08.08,0,0,1,.09,0l6.44,3.72a6,6,0,0,1-.9,10.81V16.56A1.06,1.06,0,0,0,25.25,15.67Zm2.68-4-.19-.12-6.36-3.7a1,1,0,0,0-1.05,0l-7.78,4.49V9.2a.09.09,0,0,1,0-.09L19,5.4a6,6,0,0,1,8.91,6.21ZM11.08,17.15,8.38,15.6a.14.14,0,0,1-.05-.08V8.1a6,6,0,0,1,9.84-4.61L18,3.6,11.61,7.28a1,1,0,0,0-.53.91ZM12.54,14,16,12l3.47,2v4L16,20l-3.47-2Z"/></svg>`

	grokLogoSVG template.HTML = `<svg class="provider-logo" data-logo="grok" width="40" height="40" viewBox="0 0 1024 1024" fill="#000000" role="img" aria-label="Grok" focusable="false"><path d="M395.479 633.828 735.91 381.105c16.689-12.39 40.544-7.557 48.496 11.687 41.854 101.493 23.155 223.461-60.118 307.204-83.272 83.743-199.137 102.108-305.041 60.281l-115.691 53.866C469.49 928.202 670.987 899.995 796.901 773.282c99.875-100.439 130.807-237.345 101.884-360.806l.262.263c-41.942-181.369 10.311-253.865 117.353-402.106L1024 0 883.144 141.651v-.439L395.392 633.916M325.226 695.251C206.128 580.84 226.662 403.776 328.285 301.668c75.146-75.571 198.264-106.414 305.741-61.072l115.428-53.602c-20.797-15.114-47.447-31.371-78.03-42.794-138.234-57.206-303.731-28.735-416.101 84.182-108.089 108.699-142.079 275.833-83.71 418.451 43.603 106.59-27.874 181.985-99.874 258.083C46.224 931.893 20.622 958.87 0 987.429l325.139-292.09"/></svg>`

	kimiLogoSVG template.HTML = `<svg class="provider-logo" data-logo="kimi" width="40" height="40" viewBox="0 0 24 24" fill="#000000" role="img" aria-label="Kimi" focusable="false"><path d="M21.765.351C22.998.351 24 1.353 24 2.586S22.998 4.82 21.765 4.82h-1.974c-.15 0-.26-.12-.26-.26V2.586A2.237 2.237 0 0 1 21.765.35M9.41 13.388l8.447-8.377c.16-.16.07-.471-.14-.471h-4.55s-.1.02-.14.06l-9.099 9.029c-.14.14-.35.02-.35-.21V4.81c0-.15-.1-.27-.221-.27H.22c-.12 0-.22.12-.22.27v18.57c0 .15.1.27.22.27h3.137c.12 0 .22-.12.22-.27v-3.79c0-.08.03-.16.08-.21l2.826-2.796c.07-.07.16-.08.241-.03l7.546 5.551a8.9 8.9 0 0 0 4.018 1.493c.12.01.23-.11.23-.27V19.76c0-.14-.08-.25-.19-.26a5.8 5.8 0 0 1-2.355-.942l-6.533-4.73c-.14-.09-.15-.32-.03-.441"/></svg>`

	// The fallback is deliberately generic and contains no caller-provided text.
	// Its solid terminal cursor stays legible even at 32 px.
	fallbackLogoSVG template.HTML = `<svg class="provider-logo" data-logo="fallback" width="40" height="40" viewBox="0 0 40 40" role="img" aria-label="Provider" focusable="false"><rect width="40" height="40" rx="4" fill="#000000"/><path d="M8 10h5l9 10-9 10H8l9-10Zm14 15h10v5H22Z" fill="#ffffff"/></svg>`
)

// ProviderLogoSVG returns a trusted, self-contained inline SVG for a
// normalized provider ID or group. Unknown values return a fixed fallback;
// providerOrGroup is never interpolated into the markup.
func ProviderLogoSVG(providerOrGroup string) template.HTML {
	switch normalizeLogoProvider(providerOrGroup) {
	case "openai":
		return openAILogoSVG
	case "grok":
		return grokLogoSVG
	case "kimi":
		return kimiLogoSVG
	default:
		return fallbackLogoSVG
	}
}

func normalizeLogoProvider(raw string) string {
	id := strings.ToLower(strings.TrimSpace(raw))
	id = strings.ReplaceAll(id, "_", "-")
	switch id {
	case "codex", "openai", "chatgpt":
		return "openai"
	case "xai", "x-ai", "grok":
		return "grok"
	case "kimi", "moonshot", "moonshot-ai", "moonshotai":
		return "kimi"
	default:
		return ""
	}
}
