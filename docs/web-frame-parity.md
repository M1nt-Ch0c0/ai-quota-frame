# 浏览器与原生相框渲染共用实现

2026-10-29。浏览器现直接使用 Go/WASM 中的原实现，不再以简化 Canvas
卡片或仅有新鲜持久快照的账户列表替代原生 ai-quota-frame。

**尚未实机验证。** 本页仅证明共享代码与隔离浏览器 fixture 的一致性，
不能证明设备配对、按键重置、重启保持关系或实体刷屏已经通过。

## 共享范围

- `internal/cliproxy`：原主动额度查询、套餐、窗口、重置卡、失败回退。
- `internal/usage`：原 CPM 七天 token / cost 查询与时区口径。
- `internal/service`：原快照新鲜度、账户状态、最好已知值保留和语义更新时间。
- `internal/dashboard`：原平台聚合、图标、全部五个主题、模板、CSS、皮肤、
  窗口标签、重置时间、用量 skyline、精确六色及艺术区域抖动算法。
- `internal/frame.ContentKey`：原画面变化去重规则；浏览器另将显示设置纳入缓存键。

原生 `Renderer.Render` 保持原 Chrome 截图和量化流程。`RenderHTML` 只是
公开同一模板输出；浏览器在离屏 iframe 中排版同一完整文档，用 SVG
foreignObject 绘入 Canvas，再由同一 Go 量化和 PNG 编码函数生成最终图片。
浏览器没有独立的平台行、进度条、用量图表或配色实现。

浏览器默认与当前已部署设置一致：`mint-skin`、
`claude:CLAUDE,codex:CODEX,xai:GROK`、`Asia/Shanghai`、15 分钟。
这只影响浏览器适配器的默认值，不更改 Go 原有默认配置。

## 浏览器网络边界

WASM 使用 Go 的浏览器 fetch transport，只允许当前 origin 的账户元数据、
既有 API-call 和用量摘要路径，拒绝重定向，不发送 Cookie。管理密钥仅在
当前页面输入及引擎内存中使用，清除凭据会取消采集并丢弃当前 Service。

原生 xAI adapter 在元数据没有 subject 时读取认证文件，但浏览器不允许
接收该文件。WASM 将此唯一读取改送受原管理鉴权保护的
`/v0/management/plugins/photopainter/account-subject?name=…`。
插件通过 CPA host callbacks 核实唯一 xAI 账户和身份，只返回 `sub`，
不返回 OAuth、路径或原始 JSON。其余源数据解析仍使用原 Go adapter。

## 可重复验证

```sh
cd web
node tools/check-frame.mjs
```

该命令生成 Go/WASM 和测试入口，用本机 Chrome 对照原生/浏览器行为，
不会读取私有配置、访问真实账户或连接设备。可以用 `CHROME_BIN` 指定
Chrome，用 `PHOTOFRAME_PARITY_OUTPUT` 保存明确虚构数据的 PNG。

2026-10-29 的结果：

- 同一 loopback API fixture 下，两端得到相同的 Claude/Codex/Grok 额度与
  套餐、保留停用 Kimi，并产生相同的七天 usage。上游失败后，两端采用
  同一 Service 规则保留已知值并标记 stale。
- 浏览器共执行 18 次 fixture 主动 API 查询、2 次 subject 查询；认证文件
  下载为零。取消会终止正在执行的 fetch，跨 origin 重定向未被跟随。
- 同一快照经原生 Go 与真实浏览器 WASM 生成的完整 HTML 文档逐字节一致。
  `ContentKey` 指纹也一致；只改变采集调度时间/账户展示名不会变，修改
  可见额度会变，沿用原生 producer 的去重语义。
- 默认三平台与 usage，以及单有效 provider / 其余 missing 两个场景，分别
  对照五个主题，共十组。classic、mint、arcade、pop 的 384,000 像素均
  完全一致；mint-skin 每组仅 3 个 9px 日期字形边缘因 Chrome 截图与 SVG
  光栅化的灰度取整（127 对 129）跨越黑白阈值，其余像素一致。
- 对 mint-skin 的回归边界只允许最多 8 个非艺术抖动区域、原始灰度差
  不超过 2/255 的字形像素；不会把布局、颜色、图标或数据差异算作通过。
  不能宣称其与原生 PNG 字节完全相同。

浏览器/操作系统字体选择仍沿用原模板的系统字体链。本验证证明共享代码
与同机 Chrome 的画面一致性，不代替不同操作系统或真实设备刷屏验收。

`web/build-frame.mjs` 每次构建同步生成原始 WASM、gzip WASM、匹配的 Go
运行时脚本和 `dist/frame-manifest.json`。部署应将同一次生成的资源一起
发布，按 manifest 核对字节数与 SHA-256；不能混用不同 Go 版本的运行时。
