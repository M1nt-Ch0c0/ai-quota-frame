# 网页插件

`web/` 是 CPM 网页插件“发送到相框”的页面部分：一个在访问者浏览器里运行的单页应用。它用 WebAssembly 运行和原生程序同一套 Go 采集与渲染代码，再用 Web Bluetooth 把画面推给相框。`plugins/photopainter/` 把这里的构建产物打包成 CPA 插件，见[插件说明](../plugins/photopainter/README.md)。

这一页写给要修改或构建网页插件的开发者，讲页面的结构、数据源和存储、蓝牙连接和图片事务的实现规则，以及测试方法。页面上每个按钮的作用和使用步骤在项目文档里：[上手使用](https://github.com/M1nt-Ch0c0/esp32s3/blob/main/docs/02-getting-started.md#方式一浏览器网页插件)和[电脑端、控制台与网页插件](https://github.com/M1nt-Ch0c0/esp32s3/blob/main/docs/07-clients.md#cpm-网页插件)。

## 结构

| 路径 | 内容 |
|---|---|
| `../cmd/web-frame` | 编译成 `frame.wasm` 的 Go 入口：采集、模板、量化、PNG 编码、画面指纹 |
| `src/frame-bridge.ts`、`src/render.ts` | 调用 WASM；在离屏 iframe 里排版 HTML，用 SVG `foreignObject` 画进 Canvas，再交回 Go 量化 |
| `src/source-auth.ts` | 读取 CPM 已保存的登录，或保存手动填写的管理密钥 |
| `src/ble/` | 配对 v2、配对密钥存储、会话签名、GATT 分片 |
| `src/photoframe/` | 图片事务和待确认记录 |
| `src/dev-tools.ts` | 开发者工具：读槽位、检查和上传应用包、应用管理命令 |
| `src/screen-awake.ts` | 定时推送期间的屏幕常亮 |
| `src/main.ts`、`index.html` | 页面和交互 |
| `build.mjs`、`build-frame.mjs` | 构建页面和 WASM |
| `test/` | Node 单元测试 |
| `tools/` | 浏览器检查工具 |

## 画面和数据

`cmd/web-frame` 把原生程序的采集（`internal/cliproxy`、`internal/usage`）、快照规则（`internal/service`）、平台聚合、全部主题、模板、图标、量化、PNG 编码和 `frame.ContentKey` 画面指纹编译进 WASM。浏览器只负责把同一份 HTML 排版成像素，所以同样的数据在网页和原生程序里生成的画面一致。网页里没有另一套画面实现。

网页的默认值和原生程序不同，它们定义在 `internal/webframe/frame.go`：

| 设置 | 网页默认 | 范围 |
|---|---|---|
| 主题 | `mint-skin`（薄巧） | 和原生程序相同的四种主题 |
| 显示的服务 | `claude:CLAUDE,codex:CODEX,xai:GROK` | 1–5 个，规则同 `DISPLAY_PROVIDERS` |
| 时区 | `Asia/Shanghai` | 任意 IANA 时区 |
| 更新间隔 | 15 分钟 | 1 分钟到 24 小时 |

旧版保存过的 `mint` 主题会自动改成 `mint-skin`，保存过的 Gemini CLI 勾选会自动去掉。

“数据源”里还可以导入原生程序导出的完整 `quota.Snapshot` JSON，或者直接载入一张 PNG（会先检查格式）。

## 数据源和网络

WASM 里的 Go 代码通过浏览器 `fetch` 访问 CPM，只允许当前页面同源的下面几个接口，不跟随重定向，不发送 Cookie：

| 方法 | 路径 |
|---|---|
| `GET` | `/v0/management/auth-files` |
| `POST` | `/v0/management/api-call` |
| `GET` | `/v0/management/dashboard/summary` |
| `GET` | `/v0/management/plugins/photopainter/account-subject` |

原生程序在 xAI 账号缺少 subject 时会下载认证文件；在浏览器里，这一次读取被改成插件提供的 `account-subject` 接口，它只返回 subject，浏览器拿不到 OAuth 文件和令牌。

管理密钥的来源：

1. 优先读同源 CPM 保存在 `localStorage` 里的登录（键 `cli-proxy-auth`，支持明文 JSON 和 `enc::v1::`、`enc::v2::` 两种混淆格式）。如果 CPM 当前连的是别的服务器，页面拒绝使用。
2. 没有可用的 CPM 登录时，用户可以手动填写，并选择是否记住。记住的密钥存在 `localStorage` 的 `photopainter:source-auth-v1` 里，用和 CPM 相同的 `enc::v2::` 混淆格式。这是混淆，不是加密。“清除手动配置”只删这一项，不会退出 CPM。
3. CPM 退出登录后，页面丢弃内存里继承来的密钥和已经准备好的画面。

管理密钥只用于同源请求，不会写进 URL、日志、配对记录，也不会发给相框或存到服务器。采集失败时不会把旧画面当成新画面发送；有缓存时，画面如实显示数据的过期状态。

## 连接和配对

- 需要支持 Web Bluetooth 的 Chrome，并通过 HTTPS 打开（本机开发可以用 localhost）。浏览器按宿主管理服务 UUID `7b9d0001-6c82-4f31-9a48-6f5061696e74` 过滤设备。
- 连接时读 `7b9d0006` 得到每次写入的容量，按它切分请求；读到大于 20 字节的容量后，本连接内缓存使用，最初读到 20 字节时会在后续请求里重新读取。存在 `7b9d0007` 时启用 4096 字节的大消息，前面的分片用无应答写、最后一片用有应答写。
- 连接和服务发现最多等 120 秒。服务发现第一次报 `NetworkError` 时，先断开、等 500 毫秒再试一次；这时还没发过任何协议请求。
- 断开后，同一台设备 1 秒内不会重新连接，给 Android 的系统 GATT 客户端留出清理时间。
- 同一浏览器的多个标签页通过 Web Locks（`photopainter:session`）互斥，同一时间只有一个标签页能连接相框。浏览器不支持 Web Locks 时拒绝连接。
- 配对流程和原生客户端一样：先把候选密钥写进 IndexedDB，再让相框显示六位码；相框保存成功后才显示“配对完成”。协议见[蓝牙通信](https://github.com/M1nt-Ch0c0/esp32s3/blob/main/docs/06-bluetooth.md#4-配对-v2)。
- 读设备状态时，页面把一组白名单字段（版本、ABI、运行阶段、代次、复位原因、`runtime_diagnostic`、各应用槽等）显示在“设备运行诊断”里，不包含任何密钥。

## 本地存储

| 位置 | 名称 | 内容 |
|---|---|---|
| IndexedDB | `photopainter-bindings-v2`（`bonds`、`candidates`） | 配对关系：设备 ID、配对身份、名称、时间，以及不可导出的 HMAC `CryptoKey` |
| IndexedDB | `photopainter-web-v1`（`transactions`） | 一条待确认的图片事务：设备、配对身份、请求 ID、SHA-256、大小、应用代次和服务版本 |
| `localStorage` | `photopainter:display-v2` | 显示设置和定时推送设置 |
| `localStorage` | `photopainter:selected-v2` | 当前选中的相框 |
| `localStorage` | `photopainter:source-auth-v1` | 手动记住的管理密钥（混淆） |

所有 IndexedDB 写入都要求 `durability: 'strict'`。浏览器不支持时，配对和发送直接失败，不退而使用普通事务。

“移除此相框”只删除本浏览器里的配对记录，不会解除相框那边的主人关系，也不会删除待确认的图片记录。清除浏览器数据会丢失配对密钥，之后要在相框上长按功能键重置，再重新配对。

## 图片事务

图片命令（BEGIN、CHUNK、COMMIT、CANCEL、QUERY）和 64 字节回执的格式见[蓝牙通信](https://github.com/M1nt-Ch0c0/esp32s3/blob/main/docs/06-bluetooth.md#7-图片事务)。页面按相框应用的 ABI 选择承载方式：

| 条件 | 承载方式 | 每条 CHUNK 的图片字节 |
|---|---|---|
| 状态里 `payload_abi_minor >= 3` | ABI 2.3 通用消息（op 24–27） | CHANNEL_INFO 报告的 `message_max` 减 22 字节命令头，最多 4025 |
| `payload_abi_minor` 小于 3 | ABI 2.1 应用特征（op 14 写、op 13 读） | 106 |

相框应用声明了 ABI 2.3、但宿主没有 `7b9d0007` 时，页面报错并提示更新固件，不会退回旧接口。

发送规则：

1. 发送前先冻结一份 PNG 副本并检查格式，之后改动预览不影响正在发送的图。
2. 已经有一条待确认记录时，拒绝发新图。
3. 上传完所有 CHUNK 后，**先**把事务身份写进 IndexedDB，写成功**才**发 COMMIT。写失败就不提交。
4. 上传阶段可以取消：页面发签名的 CANCEL，收到匹配的取消回执后才清掉记录。
5. 只有收到请求 ID、长度、SHA-256 都匹配的签名完成回执，才算发送成功并清掉记录。
6. COMMIT 之后连接断了，记录保留。重新连接后只发 QUERY 查询原来的请求，不会再发 COMMIT。
7. 配对身份、应用代次或服务版本变了，或者相框上已经找不到这份回执时，结果判定为“未知”。页面提示用户看一眼相框屏幕，确认后手动清除记录。

整个发送操作的超时是 90 分钟。

## 定时推送

- 开启后，页面每隔设定的分钟数（1–1440，默认 15）采集一次、生成画面，只有画面指纹变了才发送。设置保存在 `localStorage`。
- 失败的采集不排队，队列只保留最新一份成功的画面。有待确认记录时只允许核对原事务，不会自动再发。
- 页面被隐藏时暂停定时，回到前台后继续下一轮，错过的轮次不补发。暂停优先于正在进行的重连。
- 勾选“定时期间保持屏幕唤醒”时使用浏览器的 Screen Wake Lock，不修改系统设置；不支持、被拒绝或被系统释放时页面会显示原因。
- 服务器不能代替访问者的手机或电脑发蓝牙，所以页面必须开着。需要全天无人值守，用原生 `daemon`。

## 开发者工具

开发者工具用已有的配对关系管理相框上的应用槽。按钮说明见[电脑端、控制台与网页插件](https://github.com/M1nt-Ch0c0/esp32s3/blob/main/docs/07-clients.md#开发者工具)。实现上的规则：

- 本地检查 `.pkg`：不超过 1 MiB、包头标识、格式和 ABI、应用 ID、载荷 SHA-256、载荷是 Xtensa ELF。
- 每个改变状态的命令（ACTIVATE、SWITCH、ROLLBACK、REMOVE）只发一次。回复丢失时重新连接、只读签名状态，让用户按槽位核对。
- 有待确认的图片、正在配对或正在发送时，这些按钮不可用。

## 构建和测试

需要 Node 22.18 或更新版本、Go 1.26 或更新版本，以及 Chrome 或 Chromium。

```sh
cd web
npm ci --ignore-scripts
npm run check                 # 单元测试、TypeScript 检查、构建
node tools/check-frame.mjs    # 原生和浏览器的采集、画面对照
node tools/check-ui.mjs       # 隔离浏览器里的界面检查
```

`npm run check` 的产物在 `dist/`：

| 文件 | 内容 |
|---|---|
| `panel.html` | 页面，脚本内联 |
| `frame.wasm`、`frame.wasm.gz` | Go 引擎，插件提供 gzip 版本 |
| `wasm_exec.js` | 从构建所用 Go 的 `GOROOT` 复制的运行时，必须和 `frame.wasm` 来自同一次构建 |
| `frame-manifest.json` | Go 版本，以及上面三个文件的字节数和 SHA-256 |

发布时把同一次构建的资源一起发布，可以按 `frame-manifest.json` 核对。

检查工具：

- 两个检查工具都要找到 Chrome，找不到时直接失败。可以用 `CHROME_BIN` 指定路径。它们只使用本地测试数据和新建的临时浏览器配置，不访问真实的 CPM，也不连接蓝牙。
- `check-frame.mjs` 构建 WASM，然后运行 Go 测试 `TestBrowser`（`internal/dashboard`、`internal/webframe`），对比原生 Chrome 截图和浏览器 WASM 的结果：采集到的数据、完整 HTML、画面指纹逐项一致；四种主题的像素逐个比较。`mint-skin` 主题允许最多 8 个像素因为字形边缘的灰度取整（不超过 2/255）落到量化阈值另一侧，布局、颜色、图标和数据的差异都不允许。设置 `PHOTOFRAME_PARITY_OUTPUT` 可以保存对照用的 PNG。
- `check-ui.mjs` 用本地模拟的蓝牙和发送器检查界面交互。`PHOTOFRAME_UI_CAPTURE=0` 跳过截图；`PHOTOFRAME_UI_OUTPUT` 指定截图目录。

仓库的 GitHub Actions 工作流 `.github/workflows/web-plugin.yml` 在改动相关代码时运行上面三项检查，然后在固定的 Debian 12 Go 容器里断网构建 Linux amd64 插件，并用 `verify-native.py` 核对。上传的产物名带 `unverified-hardware`，表示没有经过实机检查。
