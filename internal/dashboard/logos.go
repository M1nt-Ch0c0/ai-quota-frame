package dashboard

import (
	"html/template"
	"strings"
)

// These inline logos retain source RGB colors and their original geometry.
// The renderer dithers their final pixel bounds into the six Spectra-6 inks:
// ChatGPT teal, Claude terracotta, and the original monochrome Grok mark.
//
// Sources:
//   - OpenAI: https://github.com/openai/openai-assistants-quickstart/blob/main/public/openai.svg
//     (official OpenAI repository; see https://openai.com/brand/ for usage terms)
//   - Grok: Grok_Logomark_Dark.svg from the official xAI brand archive at
//     https://data.x.ai/logos/xAI_Grok_Assets.zip
//     (see https://x.ai/legal/brand-guidelines for usage terms)
//   - Claude: https://github.com/simple-icons/simple-icons/blob/develop/icons/claude.svg
//     (CC0 Simple Icons geometry of the Claude spark)
//   - Kimi: https://github.com/simple-icons/simple-icons/blob/develop/icons/kimi.svg
//     (CC0 Simple Icons geometry, sourced there from MoonshotAI's official
//     https://moonshotai.github.io/Branding-Guide)
//   - Antigravity: official multicolour mark supplied by the project owner
//     (VTracer 0.6.4 trace, 64x59 view box; paths and colours unchanged)
const (
	openAILogoSVG template.HTML = `<svg class="provider-logo" data-logo="openai" width="40" height="40" viewBox="0 0 32 32" fill="#10a37f" role="img" aria-label="OpenAI" focusable="false"><path d="M29.71,13.09A8.09,8.09,0,0,0,20.34,2.68a8.08,8.08,0,0,0-13.7,2.9A8.08,8.08,0,0,0,2.3,18.9,8,8,0,0,0,3,25.45a8.08,8.08,0,0,0,8.69,3.87,8,8,0,0,0,6,2.68,8.09,8.09,0,0,0,7.7-5.61,8,8,0,0,0,5.33-3.86A8.09,8.09,0,0,0,29.71,13.09Zm-12,16.82a6,6,0,0,1-3.84-1.39l.19-.11,6.37-3.68a1,1,0,0,0,.53-.91v-9l2.69,1.56a.08.08,0,0,1,.05.07v7.44A6,6,0,0,1,17.68,29.91ZM4.8,24.41a6,6,0,0,1-.71-4l.19.11,6.37,3.68a1,1,0,0,0,1,0l7.79-4.49V22.8a.09.09,0,0,1,0,.08L13,26.6A6,6,0,0,1,4.8,24.41ZM3.12,10.53A6,6,0,0,1,6.28,7.9v7.57a1,1,0,0,0,.51.9l7.75,4.47L11.85,22.4a.14.14,0,0,1-.09,0L5.32,18.68a6,6,0,0,1-2.2-8.18Zm22.13,5.14-7.78-4.52L20.16,9.6a.08.08,0,0,1,.09,0l6.44,3.72a6,6,0,0,1-.9,10.81V16.56A1.06,1.06,0,0,0,25.25,15.67Zm2.68-4-.19-.12-6.36-3.7a1,1,0,0,0-1.05,0l-7.78,4.49V9.2a.09.09,0,0,1,0-.09L19,5.4a6,6,0,0,1,8.91,6.21ZM11.08,17.15,8.38,15.6a.14.14,0,0,1-.05-.08V8.1a6,6,0,0,1,9.84-4.61L18,3.6,11.61,7.28a1,1,0,0,0-.53.91ZM12.54,14,16,12l3.47,2v4L16,20l-3.47-2Z"/></svg>`

	grokLogoSVG template.HTML = `<svg class="provider-logo" data-logo="grok" width="40" height="40" viewBox="0 0 1024 1024" fill="#000000" role="img" aria-label="Grok" focusable="false"><path d="M395.479 633.828 735.91 381.105c16.689-12.39 40.544-7.557 48.496 11.687 41.854 101.493 23.155 223.461-60.118 307.204-83.272 83.743-199.137 102.108-305.041 60.281l-115.691 53.866C469.49 928.202 670.987 899.995 796.901 773.282c99.875-100.439 130.807-237.345 101.884-360.806l.262.263c-41.942-181.369 10.311-253.865 117.353-402.106L1024 0 883.144 141.651v-.439L395.392 633.916M325.226 695.251C206.128 580.84 226.662 403.776 328.285 301.668c75.146-75.571 198.264-106.414 305.741-61.072l115.428-53.602c-20.797-15.114-47.447-31.371-78.03-42.794-138.234-57.206-303.731-28.735-416.101 84.182-108.089 108.699-142.079 275.833-83.71 418.451 43.603 106.59-27.874 181.985-99.874 258.083C46.224 931.893 20.622 958.87 0 987.429l325.139-292.09"/></svg>`

	kimiLogoSVG template.HTML = `<svg class="provider-logo" data-logo="kimi" width="40" height="40" viewBox="0 0 24 24" fill="#000000" role="img" aria-label="Kimi" focusable="false"><path d="M21.765.351C22.998.351 24 1.353 24 2.586S22.998 4.82 21.765 4.82h-1.974c-.15 0-.26-.12-.26-.26V2.586A2.237 2.237 0 0 1 21.765.35M9.41 13.388l8.447-8.377c.16-.16.07-.471-.14-.471h-4.55s-.1.02-.14.06l-9.099 9.029c-.14.14-.35.02-.35-.21V4.81c0-.15-.1-.27-.221-.27H.22c-.12 0-.22.12-.22.27v18.57c0 .15.1.27.22.27h3.137c.12 0 .22-.12.22-.27v-3.79c0-.08.03-.16.08-.21l2.826-2.796c.07-.07.16-.08.241-.03l7.546 5.551a8.9 8.9 0 0 0 4.018 1.493c.12.01.23-.11.23-.27V19.76c0-.14-.08-.25-.19-.26a5.8 5.8 0 0 1-2.355-.942l-6.533-4.73c-.14-.09-.15-.32-.03-.441"/></svg>`

	claudeLogoSVG template.HTML = `<svg class="provider-logo" data-logo="claude" width="40" height="40" viewBox="0 0 24 24" fill="#d97757" role="img" aria-label="Claude" focusable="false"><path d="m4.7144 15.9555 4.7174-2.6471.079-.2307-.079-.1275h-.2307l-.7893-.0486-2.6956-.0729-2.3375-.0971-2.2646-.1214-.5707-.1215-.5343-.7042.0546-.3522.4797-.3218.686.0608 1.5179.1032 2.2767.1578 1.6514.0972 2.4468.255h.3886l.0546-.1579-.1336-.0971-.1032-.0972L6.973 9.8356l-2.55-1.6879-1.3356-.9714-.7225-.4918-.3643-.4614-.1578-1.0078.6557-.7225.8803.0607.2246.0607.8925.686 1.9064 1.4754 2.4893 1.8336.3643.3035.1457-.1032.0182-.0728-.164-.2733-1.3539-2.4467-1.445-2.4893-.6435-1.032-.17-.6194c-.0607-.255-.1032-.4674-.1032-.7285L6.287.1335 6.6997 0l.9957.1336.419.3642.6192 1.4147 1.0018 2.2282 1.5543 3.0296.4553.8985.2429.8318.091.255h.1579v-.1457l.1275-1.706.2368-2.0947.2307-2.6957.0789-.7589.3764-.9107.7468-.4918.5828.2793.4797.686-.0668.4433-.2853 1.8517-.5586 2.9021-.3643 1.9429h.2125l.2429-.2429.9835-1.3053 1.6514-2.0643.7286-.8196.85-.9046.5464-.4311h1.0321l.759 1.1293-.34 1.1657-1.0625 1.3478-.8804 1.1414-1.2628 1.7-.7893 1.36.0729.1093.1882-.0183 2.8535-.607 1.5421-.2794 1.8396-.3157.8318.3886.091.3946-.3278.8075-1.967.4857-2.3072.4614-3.4364.8136-.0425.0304.0486.0607 1.5482.1457.6618.0364h1.621l3.0175.2247.7892.522.4736.6376-.079.4857-1.2142.6193-1.6393-.3886-3.825-.9107-1.3113-.3279h-.1822v.1093l1.0929 1.0686 2.0035 1.8092 2.5075 2.3314.1275.5768-.3218.4554-.34-.0486-2.2039-1.6575-.85-.7468-1.9246-1.621h-.1275v.17l.4432.6496 2.3436 3.5214.1214 1.0807-.17.3521-.6071.2125-.6679-.1214-1.3721-1.9246L14.38 17.959l-1.1414-1.9428-.1397.079-.674 7.2552-.3156.3703-.7286.2793-.6071-.4614-.3218-.7468.3218-1.4753.3886-1.9246.3157-1.53.2853-1.9004.17-.6314-.0121-.0425-.1397.0182-1.4328 1.9672-2.1796 2.9446-1.7243 1.8456-.4128.164-.7164-.3704.0667-.6618.4008-.5889 2.386-3.0357 1.4389-1.882.929-1.0868-.0062-.1579h-.0546l-6.3385 4.1164-1.1293.1457-.4857-.4554.0608-.7467.2307-.2429 1.9064-1.3114Z"/></svg>`

	antigravityLogoSVG template.HTML = `<svg class="provider-logo" data-logo="antigravity" width="40" height="40" viewBox="0 0 64 59" role="img" aria-label="Antigravity" focusable="false"><path d="M0,0 L8,0 L14,4 L19,14 L27,40 L32,50 L36,54 L35,59 L30,59 L22,52 L11,35 L6,33 L-1,34 L-6,39 L-14,52 L-22,59 L-28,59 L-27,53 L-22,47 L-17,34 L-10,12 L-5,3 Z" fill="#3789f9" transform="translate(28,0)"/><path d="M0,0 L8,0 L14,4 L19,14 L25,35 L21,34 L16,29 L11,26 L7,20 L7,18 L2,16 L-3,15 L-8,18 L-12,19 L-9,9 L-4,2 Z" fill="#6d80d8" transform="translate(28,0)"/><path d="M0,0 L8,0 L14,4 L19,14 L20,19 L13,15 L10,12 L3,10 L-1,8 L-7,7 L-4,2 Z" fill="#d78240" transform="translate(28,0)"/><path d="M0,0 L5,1 L10,4 L12,9 L1,8 L-5,13 L-10,21 L-13,26 L-16,26 L-9,5 L-4,2 Z M6,7 Z" fill="#3294cc" transform="translate(25,14)"/><path d="M0,0 L5,2 L10,10 L12,18 L5,14 L1,10 L0,4 L-3,3 L0,2 Z" fill="#e45c49" transform="translate(36,1)"/><path d="M0,0 L9,1 L12,3 L12,5 L7,6 L4,8 L-1,11 L-5,12 L-2,2 Z" fill="#90ae64" transform="translate(21,7)"/><path d="M0,0 L5,1 L5,4 L-2,7 L-7,11 L-11,10 L-9,5 L-4,2 Z" fill="#53a89a" transform="translate(25,14)"/><path d="M0,0 L5,0 L16,9 L17,13 L12,12 L8,9 L8,7 L4,5 L0,2 Z" fill="#b5677d" transform="translate(33,11)"/><path d="M0,0 L6,0 L14,6 L19,11 L23,12 L22,15 L15,12 L10,8 L10,6 L4,5 Z" fill="#778998" transform="translate(27,12)"/><path d="M0,0 L4,2 L-11,17 L-12,14 L-5,4 Z" fill="#3390df" transform="translate(26,21)"/><path d="M0,0 L2,1 L-4,5 L-9,9 L-13,13 L-14,10 L-13,7 L-6,4 L-3,1 Z" fill="#3fa1b7" transform="translate(27,18)"/><path d="M0,0 L4,0 L9,5 L13,6 L12,9 L5,6 L0,2 Z" fill="#8277bb" transform="translate(37,18)"/><path d="M0,0 L5,1 L7,6 L-2,5 Z M1,4 Z" fill="#4989cf" transform="translate(30,17)"/><path d="M0,0 L5,1 L2,3 L-3,6 L-7,7 L-6,3 Z" fill="#71b774" transform="translate(23,12)"/><path d="M0,0 L7,1 L9,7 L5,6 L0,1 Z" fill="#6687e9" transform="translate(44,28)"/><path d="M0,0 L7,0 L5,1 L5,3 L8,4 L4,5 L-2,4 Z" fill="#c7af38" transform="translate(23,3)"/><path d="M0,0 L8,0 L8,3 L4,4 L-4,3 Z" fill="#ef842a" transform="translate(28,0)"/><path d="M0,0 L7,4 L7,6 L10,6 L11,10 L4,6 L0,2 Z" fill="#cd5d67" transform="translate(37,9)"/><path d="M0,0 L5,2 L9,8 L8,11 L2,3 L0,2 Z" fill="#f35241" transform="translate(36,1)"/><path d="M0,0 L8,2 L9,6 L4,5 L0,2 Z" fill="#a667a2" transform="translate(41,18)"/><path d="M0,0 L9,1 L8,3 L-2,3 Z" fill="#a4b34c" transform="translate(21,7)"/><path d="M0,0 L2,0 L7,5 L8,7 L3,6 L0,2 Z" fill="#617fcf" transform="translate(35,18)"/><path d="M0,0 L5,2 L8,7 L4,5 L0,2 Z" fill="#9d7784" transform="translate(33,11)"/><path d="M0,0 L6,2 L6,4 L0,3 Z" fill="#bc7f59" transform="translate(31,7)"/></svg>`

	// OpenCode: a dark square frame whose lower interior is a 50% stripe tint,
	// drawn with whole pixels so it stays crisp after six-ink quantization.
	openCodeLogoSVG template.HTML = `<svg class="provider-logo" data-logo="opencode" width="40" height="40" viewBox="0 0 40 40" role="img" aria-label="OpenCode" focusable="false" shape-rendering="crispEdges"><rect x="6" y="4" width="28" height="32" fill="#000000"/><rect x="12" y="10" width="16" height="20" fill="#ffffff"/><rect x="12" y="20" width="16" height="1" fill="#000000"/><rect x="12" y="22" width="16" height="1" fill="#000000"/><rect x="12" y="24" width="16" height="1" fill="#000000"/><rect x="12" y="26" width="16" height="1" fill="#000000"/><rect x="12" y="28" width="16" height="1" fill="#000000"/></svg>`
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
	case "claude":
		return claudeLogoSVG
	case "antigravity":
		return antigravityLogoSVG
	case "opencode":
		return openCodeLogoSVG
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
	case "claude", "anthropic":
		return "claude"
	case "antigravity", "anti-gravity":
		return "antigravity"
	case "opencode", "opencode-go":
		return "opencode"
	default:
		return ""
	}
}
