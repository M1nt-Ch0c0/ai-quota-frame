# 画面模板维护

主机侧 800×480 画面由 **HTML/CSS 模板** 渲染，再用 Headless Chromium 截图成 PNG。

## 文件

| 文件 | 作用 |
|---|---|
| `templates/frame.html` | 页面结构与 `{{.字段}}` 数据绑定 |
| `templates/frame.css` | 布局、颜色、字体；OAuth 行已缩小，为近 7 日柱状图留出空间 |
| `viewmodel.go` | 把 `quota.Snapshot` 转成模板数据 |
| `providers.go` | `DISPLAY_PROVIDERS` 解析与默认订阅行 |
| `layout.go` | 按配置的 provider 行聚合额度窗口 |
| `render.go` | 模板 → Headless Chromium 截图 → PNG |

改 UI 时优先编辑 `frame.html` 和 `frame.css`。新增画面字段时改 `viewmodel.go`。增减默认订阅类型时改 `providers.go`。

## 自定义 OAuth 订阅行

环境变量 `DISPLAY_PROVIDERS` 控制画面上显示哪些订阅，最多 5 行：

```text
codex,claude,google
codex:CODEX,kimi:KIMI,gemini-cli+antigravity:GEMINI
```

空值使用默认 `codex,xai,kimi`（标签为 CODEX / GROK / KIMI）。

## 近 7 日柱状图

每个日期两根柱：蓝色为 token，绿色为 API 折合价格。高度按 7 日内各自最大值归一化。无用量数据时显示 `7-day usage unavailable`。

## 本地预览 HTML

```bash
go test ./internal/dashboard -run TestRenderHTMLProducesDocument -v
```

测试会在临时目录写出 `frame-preview.html`。

重新生成 README 预览图：

```bash
WRITE_PREVIEW=1 go test ./internal/dashboard -run TestWritePreviewPNG -count=1
```

## 渲染依赖

PNG 导出使用 **Headless Chromium**（`chromedp`）。

- **Linux 主机**：安装 `chromium` 或 `google-chrome-stable`
- **Docker**：镜像已包含 `chromium`
- **WSL**：可在 WSL 内安装 Chromium

未找到浏览器时，`Render()` 会返回明确错误。
