# 画面模板维护

主机侧 800×480 画面由 **HTML/CSS 模板** 渲染，再用 Headless Chromium 截图成 PNG。

## 文件

| 文件 | 作用 |
|---|---|
| `templates/frame.html` | 页面结构与 `{{.字段}}` 数据绑定 |
| `templates/frame.css` | 终端风布局、六色样式、大字号和紧凑 7 日用量面板 |
| `viewmodel.go` | 把 `quota.Snapshot` 转成模板数据 |
| `providers.go` | `DISPLAY_PROVIDERS` 解析与默认订阅行 |
| `layout.go` | 按配置的 provider 行聚合额度窗口 |
| `logos.go` | 默认 provider 的纯黑白 inline SVG 与安全 fallback |
| `palette.go` | 无抖动量化到 PhotoFrame fast path 所需的理论六色 |
| `render.go` | 模板 → Headless Chromium 截图 → 理论六色 PNG |

改 UI 时优先编辑 `frame.html` 和 `frame.css`。新增画面字段时改 `viewmodel.go`。增减默认订阅类型时改 `providers.go`。

## 自定义 OAuth 订阅行

环境变量 `DISPLAY_PROVIDERS` 控制画面上显示哪些订阅，最多 5 行：

```text
codex,claude,google
codex:CODEX,kimi:KIMI,gemini-cli+antigravity:GEMINI
```

空值使用默认 `codex,xai,kimi`（标签为 CODEX / GROK / KIMI）。

## 近 7 日用量面板

左侧把 `usage.days[]` 的最近 7 天 token 归一化成紧邻的纯黑柱状图，并在柱上方以一位小数和 `k` / `M` / `B` 单位标值；右侧以纯黑白三列显示 TODAY、7DAY 和 PEAK 的 token 与 API 折合价格。状态文字位于页眉正中，底部空间全部留给用量面板。`usage.days[]` 缺失时显示 `NO USAGE DATA`；不会用 today 字段伪造 7 日数据。

## 额度进度条

每个额度窗口显示 10 个带黑色边框的离散格，每格代表 10%。0–10% 的已用格为红色、10–40% 为黄色、40–100% 为绿色；不足整格时只硬切分填充当前格。空余部分保持白色，百分比文字继续作为非颜色提示。

## 六色输出

`Render()` 会把 Chromium 的抗锯齿截图以 nearest-color 方式量化到纯黑、纯白、纯黄、纯红、纯蓝、纯绿，且不做误差扩散。输出为 800×480、8-bit RGB、non-interlaced PNG，PhotoFrame v2.18 可直接走 processed-PNG fast path，避免设备再次抖动。

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
