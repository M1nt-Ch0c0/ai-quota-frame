# AI Quota Frame

当前源码提供 **按键配对 v2、原生 BLE 发送，以及完整复用原 Go 引擎的 CPM 浏览器插件**。新版配对、长按解绑、重启保持关系和实际刷图 **尚未实机验证**；离线/浏览器 fixture 测试通过不等于设备验收完成。入口与验证范围见 [BLE 指南](docs/ble.md)、[浏览器使用说明](web/README.md)、[共享渲染验证](docs/web-frame-parity.md) 和 [客户端离线验证记录](docs/validation-client-pairing-v2-2026-10-29.md)。

原生 Mac 在支持的设备上优先使用 ABI 2.2 CoC，其他已配对客户端走 GATT。2026-10-27 的 67,509 字节/2.477 秒和显示完成回执属于旧鉴权基线，保留在 [APP 收图历史](docs/app-ble-images.md)，不能用来证明配对 v2 已实机通过。

**仓库职责：本仓库保留Go采集、渲染、发送与设备后台。** SwiftUI界面已拆至独立 [photopainter-console](https://github.com/M1nt-Ch0c0/photopainter-console)，通过本机Unix socket使用同一串行设备队列；验证边界见 [代码与证据](https://github.com/M1nt-Ch0c0/esp32s3/blob/main/wiki/Status-and-Evidence.md)。

**CPM 浏览器相框插件：**[使用与协议验证](web/README.md) · [原生插件构建、安装与回滚](plugins/photopainter/README.md)。页面共用 Go/WASM 数据与画面，通过访问者电脑的 Web Bluetooth/GATT 发送。资源和最小账号投影使用现有 CPA 插件路由，官方 CPM 前端保持原样。浏览器与原生客户端是不同 owner；切换前暂停原 owner，按键重置后在新端配对。

> **AI / 开发者入口：**先阅读 [`AGENTS.md`](AGENTS.md)。空白电脑部署、三仓联调和真机诊断使用 [`develop-photopainter-stack`](https://github.com/M1nt-Ch0c0/photopainter-host/blob/main/.agents/skills/develop-photopainter-stack/SKILL.md)。

五个仓库的职责、架构图、耦合边界与修改影响，见 [PhotoPainter 架构总览](https://github.com/M1nt-Ch0c0/esp32s3/blob/main/ARCHITECTURE.md)。

**开发规范见 [PhotoPainter Wiki](https://github.com/M1nt-Ch0c0/esp32s3/blob/main/wiki/Home.md)。** 本仓库负责采集、渲染、发送和主机部署；应用安装能力由设备宿主提供，本程序是其客户端。

BLE 图片优先访问 photoframe ABI 2.2 CoC，旧设备/未接入CoC的平台使用APP的GATT兼容服务；应用必须已被选中；不自动切换应用，也不回退 HTTP。旧宿主 PNG 通道实现与回归测试保留。常驻发现、鉴权和应用 OTA 继续由宿主提供。

`ai-quota-frame` 读取额度和用量、生成 800×480 的 Spectra 6 六色 PNG，再通过已配对 BLE 主动推送到 ESP32-S3 PhotoPainter。设备发现、推图和应用更新不需要相框联网；采集数据需要互联网。HTTP 发送仅用于明确选用旧 v1 固件的历史兼容。设备不轮询主机，也不运行设备 WebUI。

## 刷写

设备固件由独立的 [`M1nt-Ch0c0/photopainter-host`](https://github.com/M1nt-Ch0c0/photopainter-host) 仓库构建；可加载组件位于 [`M1nt-Ch0c0/photoframe`](https://github.com/M1nt-Ch0c0/photoframe)。不要把本仓库、第三方相册/轮询固件或微雪整机工程当作设备底包。

刷写前必须先完整备份并校验设备的 16 MiB Flash；机内有 microSD 时也要关机取卡并制作整卡镜像。具体构建、刷写和 NVS 配网步骤以 `photopainter-host` 的 README 为准。备份、密钥文件和生成的 NVS 镜像都应放在仓库之外。

## 推图

复制数据源配置，然后与相框配对一次：

```bash
cp .env.example .env
chmod 600 .env
make check
./dist/ai-quota-frame ble scan
./dist/ai-quota-frame ble pair --device-id 001122334455 --name "PhotoPainter desktop"
```

将示例设备 ID 替换为扫描结果。核对电脑和相框显示的六位配对码，然后短按相框 **GPIO4 功能键** 同意；BOOT 和电源键不参与。关系保存在本机私有目录和设备 NVS，下次连接自动鉴权，不需要生成或输入推图令牌。更换连接方时，长按功能键至少 8 秒并松开；若正在刷新，先封住新操作，再等已开始的刷新完整结束后清除关系。Wi-Fi 和已安装 APP 保留。macOS 应按下文安装应用身份后使用 `pp pair`，不要把裸终端的权限表现当成应用权限已验证。

主动推送所需的核心配置如下；配对完成后设备 ID 自动沿用，也可显式指定：

```dotenv
CLIPROXY_BASE_URL=http://127.0.0.1:8317
SOURCE_AUTH_MODE=management-key
CLIPROXY_MANAGEMENT_KEY=replace-with-management-key
PHOTOFRAME_PUSH_TRANSPORT=ble
```

主机必须安装 Chromium 或 Google Chrome；也可用 `CHROME_BIN` 指向浏览器可执行文件。程序会在启动时检查浏览器是否可用，然后才开始刷新额度和推图。

构建并启动：

```bash
make check
set -a
. ./.env
set +a
./dist/ai-quota-frame
```

每次成功的额度刷新都会交给单工作线程处理；队列只保留最新快照。主机生成的图片必须不超过 5 MiB。默认 BLE 模式使用已配对关系，支持时走 CoC，否则使用 GATT；只在收到匹配的签名完成回执后记为刷屏完成。没有配对关系时返回可操作的配对提示，不打开蓝牙连接反复尝试。

刷新失败会立即作废待推送的旧 LIVE 帧，并尽力取消尚未完成的旧请求；失败快照不会推到设备。COMMIT 结果不确定时保留原请求身份，只查询原回执；重置配对、更换绑定或回执丢失都不能触发自动重刷。渲染错误不进入定时重算，只等下一次成功额度刷新。

## 鉴权

设备使用 P-256 ECDH、双向承诺和六位比较码建立关系，用户必须在相框上按键同意；客户端先持久化候选密钥，设备成功保存 NVS 后才算绑定。后续连接使用新挑战和已协商密钥进行双向认证，所有现有图片/应用命令继续签名。客户端不会把数据源管理密钥交给设备。

原生客户端与网页各自持有独立关系；从一个客户端换到另一个，需要在设备端重置并重新配对。同一客户端可以保存多台相框。网页密钥为 IndexedDB 中不可导出的 WebCrypto Key，原生密钥使用 `0600` 私有文件；CLI 和诊断 JSON 不返回密钥。

普通管理密钥接入使用 `SOURCE_AUTH_MODE=management-key`，额度与用量分别使用 `CLIPROXY_MANAGEMENT_KEY`、`CPAMP_ADMIN_KEY`。该模式不会因为保留了 Google 服务账号文件而改用 ID Token。

管理 API 也可以放在接受 JWT bearer 的 oauth2-proxy 之后，由网关注入管理密钥。仅在网关确实提供此能力时使用 `SOURCE_AUTH_MODE=google-id-token`，并设置 `GOOGLE_SERVICE_ACCOUNT_FILE` 与 `GOOGLE_ID_TOKEN_AUDIENCE`（oauth2-proxy 的 client_id），两个 `BASE_URL` 指向网关 HTTPS 地址。程序用服务账号私钥签名断言，向 `oauth2.googleapis.com` 换取一小时有效的 ID Token 并提前 5 分钟刷新；私钥文件只在本机读取，权限应为 0600 并放在仓库之外。网关应仅为该服务账号放行额度采集所需的四个管理路由。未指定模式时兼容单一凭据配置；同时配置 Google 和静态凭据则要求显式选择，不静默覆盖。

当前 `manager.m1ntch0c0.com` 已停用 Google 网关，须使用普通管理密钥。只读的 `ai-quota-frame source-check` 可独立验证额度及用量采集，不启动渲染器、BLE、推图或后台；通过既有可信启动环境运行，输出不含密钥、账号名称或响应正文。故障排查和私下迁移说明见 [数据源鉴权修复](docs/source-auth-diagnostics.md)。

配对 v2 宿主永久拒绝旧固定令牌 BLE 鉴权，并对旧 HTTP 令牌管理/变更入口返回 `410`，原令牌不能绕过物理重置。`PHOTOFRAME_PUSH_TRANSPORT=http` 与 `PHOTOFRAME_PUSH_TOKEN` 仅保留给明确使用旧 v1 固件的历史兼容流程，新安装默认 BLE，后台 BLE 模式会忽略遗留令牌环境值。

不要提交 `.env`、设备密钥、Flash/SD 备份或含密钥的命令输出。相框只应位于受信局域网，禁止把设备的 HTTP 端口映射到公网。

## BLE 发现、推图与应用安装

完成一次按键配对后，配置：

```dotenv
PHOTOFRAME_PUSH_TRANSPORT=ble
# 可选；省略时沿用最近一次配对成功的设备。
# PHOTOFRAME_DEVICE_ID=001122334455
```

`ble pair` 完成协商并保存关系；`ble paired` 查看本机配对摘要，`ble pair-status` 读取设备公开配对状态，`ble forget` 只忘记本机凭据。设备端解绑只能用按键完成。`ble bind`、`ble unbind` 保留为配对/本地忘记的兼容名称。BLE 模式不需要 IP，也不回退 HTTP；这些命令不会改写用户 `.env`。

图片分块传输并校验 SHA-256，由同一个 App Manager 交给当前相框应用。只有签名完成回执确认实体刷新和最终 POWER_OFF 后才算成功。完成回复丢失时只查询原请求，不重复刷屏；设备重启、配对身份变化或回执丢失时停止该图片的自动重试。设备回执保存在有限 RAM 中。协议提供鉴权和完整性，不加密图片及应用包内容。

`ble push --file frame.png` 可单次推图，`ble install --file module.pkg` 暂存应用包，`ble activate --app APP_ID` 显式试运行。命令、平台要求和历史兼容边界见 [BLE 指南](docs/ble.md)。

## macOS 常驻运行

原生构建、当前鉴权和历史 BLE 实机结果见 [macOS 部署记录](docs/macos-deployment.md)。配对 v2 尚未实机验证。

先配置忽略的 `.env`，使用 `go.mod` 要求的 Go 版本或更新版本执行 `make check`，再运行：

```sh
python3 deploy/install-macos.py --env-file .env
```

首次调试尚未取得实体刷屏成功时，使用 `--no-start` 只安装并禁用自动启动；确认刷屏成功后重新运行不带该参数的安装命令。

macOS 的 `make build`/`make check` 默认启用 CGO，以编译 CoreBluetooth；需要本机 Xcode 工具链。`BUILD_CGO_ENABLED=0` 可显式构建仅 HTTP 的版本，但该版本不能执行 BLE 发现或应用安装。Linux/Windows 构建保持静态。

安装器使用 Xcode Command Line Tools 编译本地启动器，在 `~/Applications/PhotoPainter Renderer.app` 建立应用身份，并更新用户级 `com.m1ntch0c0.ai-quota-frame` LaunchAgent，登录自动启动，异常退出后重启。应用声明 `NSLocalNetworkUsageDescription` 和 `NSBluetoothAlwaysUsageDescription`，LaunchAgent 通过 `AssociatedBundleIdentifiers` 关联应用，遵循 [Apple TN3179](https://developer.apple.com/documentation/technotes/tn3179-understanding-local-network-privacy)。首次出现本地网络或蓝牙提示时，在 Mac 图形界面允许该应用访问；SSH 本身不会代替用户授予系统隐私权限。若出现 `no route to host`，同时检查实际 Wi-Fi 路由和“隐私与安全性 → 本地网络”，不能仅凭这个错误断定是权限问题。凭据只保存在权限 0600 的本地配置文件中。运行期间通过 `caffeinate` 阻止空闲休眠，日志位于 `~/Library/Logs/photopainter-render.log`。硬件诊断或模块升级前先 `launchctl bootout gui/$(id -u)/com.m1ntch0c0.ai-quota-frame` 停止自动推图。

已安装的应用启动器也接受 BLE 命令，并私下加载同一份运行配置。例如通过应用身份发起首次绑定：

```sh
python3 deploy/pp.py pair --device-id 001122334455 --name "PhotoPainter Mac"
```

启动器不会将凭据写入参数或应用包。迁移到另一台电脑或浏览器时，先停止原 owner 的自动推图，再在相框上物理重置并重新配对；不要恢复已经失去绑定的旧 owner。

使用前述 Google ID Token 网关时，不需要源站 SSH 隧道。若采用已有的 SSH loopback 数据源部署，本机 28317、28318 可分别转发至远端 8317、18317。先在本机 SSH 配置中建立 `photopainter-source` 别名，设置真实服务器、登录用户及已有密钥，并核验服务器主机指纹。确认无需交互认证：

```sh
ssh -o BatchMode=yes -o StrictHostKeyChecking=yes photopainter-source true
python3 deploy/install-source-tunnel.py --ssh-host photopainter-source
```

安装器不创建或复制 SSH 密钥，保持主机密钥校验，只监听 loopback；隧道登录启动、断线自动重连。`--dry-run` 只打印待安装 plist。将 `.env` 中 `CLIPROXY_BASE_URL` 设为 `http://127.0.0.1:28317`，`CPAMP_BASE_URL` 设为 `http://127.0.0.1:28318`，分别填写远端管理凭据。不要公开管理端口。建议 `REFRESH_INTERVAL=5m`、`DEMO_MODE=false`、`TZ=Asia/Shanghai`；机器必须保持开机、登录和联网。

检查和维护：

```sh
launchctl print gui/$(id -u)/com.m1ntch0c0.photopainter-source-tunnel
launchctl print gui/$(id -u)/com.m1ntch0c0.ai-quota-frame
# 停止自动推送，再进行硬件或模块操作：
launchctl bootout gui/$(id -u)/com.m1ntch0c0.ai-quota-frame
# 恢复已安装的服务（不会替换文件）：
launchctl bootstrap gui/$(id -u) "$HOME/Library/LaunchAgents/com.m1ntch0c0.ai-quota-frame.plist"
```

若曾用 `--no-start` 禁用服务，先 `launchctl enable gui/$(id -u)/com.m1ntch0c0.ai-quota-frame`，或重新运行安装器。正常日志 `photopainter-render.log` 中的 `frame pushed` 表示收到设备 HTTP 200 或 BLE 鉴权完成凭据；`last-frame.png` 也只在这时保存。错误日志为同目录 `photopainter-render.error.log`，隧道日志使用 `photopainter-source-tunnel` 前缀。查看日志时注意保护管理端返回的敏感内容。停用并取消登录启动可执行 `launchctl disable` 后 `bootout`；二进制和凭据仍保留在本机运行目录。

只生成一张真实数据预览、不向设备推送：

```sh
set -a
. ./.env
set +a
go run ./cmd/render-frame --output frame.png
```
