# ai-quota-frame

`ai-quota-frame` 是 PhotoPainter 项目的电脑端程序。它从 CLIProxyAPI（CPA）读取各 AI 服务商的额度，排版成 800×480 的六色 PNG，再通过蓝牙推给相框上的 `photoframe` 应用。同一套采集和渲染代码还编译成 WebAssembly，作为 CPM 管理中心里的网页插件运行。

这一页写给要构建、测试或修改本仓库的开发者。只想把额度推到相框上，先看项目文档：

- [项目介绍](https://github.com/M1nt-Ch0c0/esp32s3/blob/main/docs/01-overview.md)：五个仓库的分工和整体架构
- [上手使用](https://github.com/M1nt-Ch0c0/esp32s3/blob/main/docs/02-getting-started.md)：配对、推图、在 Mac 上装成常驻服务
- [电脑端、控制台与网页插件](https://github.com/M1nt-Ch0c0/esp32s3/blob/main/docs/07-clients.md)：全部子命令、配置项、主题和平台差异
- [蓝牙通信](https://github.com/M1nt-Ch0c0/esp32s3/blob/main/docs/06-bluetooth.md)：配对 v2、签名命令和图片事务
- [维护与排错](https://github.com/M1nt-Ch0c0/esp32s3/blob/main/docs/08-maintenance.md)：备份、USB 刷写和常见症状

## 仓库内容

| 路径 | 内容 |
|---|---|
| `cmd/ai-quota-frame` | 主程序。子命令 `daemon`、`ble`、`console`、`source-check`、`help`；不带参数只打印帮助 |
| `cmd/aqf` | 调试客户端，通过本机 socket 使用 `daemon` 持有的蓝牙会话，见 [aqf](docs/aqf.md) |
| `cmd/render-frame` | 采集一次并把画面写成 PNG（`--output`，默认 `frame.png`），不连接相框 |
| `cmd/web-frame` | 编译成 WASM 的采集和渲染入口，供网页插件使用 |
| `cmd/bake-skin` | 把插画素材转换成主题背景：800×480、六色误差扩散、数据区留白 |
| `internal/cliproxy`、`internal/usage`、`internal/sources`、`internal/googleauth` | 读取额度和用量，以及两种数据源鉴权 |
| `internal/service`、`internal/quota` | 快照、新鲜度和账号状态 |
| `internal/dashboard`、`internal/frame` | HTML 模板、主题、图标、六色量化、PNG 编码、画面去重 |
| `internal/push` | 推送队列：单工作线程，只保留最新一份快照 |
| `internal/ble` | Go 蓝牙客户端：配对、签名命令、应用包安装、图片事务，见[原生蓝牙客户端](docs/ble.md) |
| `internal/aqf`、`internal/console` | `daemon` 的两个本机 Unix socket：`aqf` 接口和 macOS 控制台接口 |
| `web/` | 网页插件的页面和 Web Bluetooth 客户端，见 [web/README.md](web/README.md) |
| `plugins/photopainter/` | 把网页资源打包成 CPA 插件 `.so`，见 [plugins/photopainter/README.md](plugins/photopainter/README.md) |
| `deploy/` | macOS 安装脚本、`pp.py`、systemd 单元、SSH 隧道、数据源迁移和 USB 维护脚本 |

macOS 图形控制台在独立仓库 [photopainter-console](https://github.com/M1nt-Ch0c0/photopainter-console)，它通过 `daemon` 的本机 socket 工作。相框固件在 [photopainter-host](https://github.com/M1nt-Ch0c0/photopainter-host)，相框应用在 [photoframe](https://github.com/M1nt-Ch0c0/photoframe)。

## 构建和测试

需要 `go.mod` 声明的 Go 版本或更新版本，以及 Chrome 或 Chromium。找不到浏览器时可以用 `CHROME_BIN` 指定路径。macOS 还需要 Xcode Command Line Tools，因为 CoreBluetooth 部分要用 CGO 编译。

```sh
make check
```

`make check` 依次运行 `go test ./...`、`go test -race ./...`、`go vet ./...`，然后构建 `dist/ai-quota-frame` 和 `dist/aqf`。macOS 上默认启用 CGO；Linux 和 Windows 上静态编译。没有 Chrome 时，渲染相关的测试会跳过。

Windows 版本用交叉编译：

```sh
GOOS=windows GOARCH=amd64 CGO_ENABLED=0 go build -buildvcs=false -trimpath -ldflags='-s -w' \
  -o dist/ai-quota-frame.exe ./cmd/ai-quota-frame
```

网页插件的构建和测试见 [web/README.md](web/README.md#构建和测试)。

## 运行

```sh
cp .env.example .env
chmod 600 .env          # 然后填写 CLIPROXY_MANAGEMENT_KEY 等
make check
./dist/ai-quota-frame ble scan
./dist/ai-quota-frame ble pair --device-id 001122334455
set -a; . ./.env; set +a
./dist/ai-quota-frame daemon
```

配对时核对电脑和相框上的六位码，然后短按相框的功能键。完整步骤、macOS 安装方式和全部配置项见[上手使用](https://github.com/M1nt-Ch0c0/esp32s3/blob/main/docs/02-getting-started.md)和[电脑端、控制台与网页插件](https://github.com/M1nt-Ch0c0/esp32s3/blob/main/docs/07-clients.md#配置项)。

常驻运行的三种方式都已经带上 `daemon` 子命令：

| 方式 | 文件 |
|---|---|
| macOS LaunchAgent | 由 `deploy/install-macos.py` 生成 |
| Linux systemd | `deploy/ai-quota-frame.service`，从 `/etc/ai-quota-frame.env` 读配置 |
| Docker | `Dockerfile`，镜像自带 Chromium，默认命令是 `daemon` |

数据源的两种鉴权方式、只读检查 `source-check` 和 SSH 隧道见[数据源](docs/data-sources.md)。

## 文档目录

| 文档 | 内容 |
|---|---|
| [docs/ble.md](docs/ble.md) | Go 蓝牙客户端的实现：配对文件、图片发送、应用包安装、各平台适配、实机测试 |
| [docs/aqf.md](docs/aqf.md) | `aqf` 调试客户端和 `daemon` 的常驻蓝牙会话 |
| [docs/data-sources.md](docs/data-sources.md) | 数据源鉴权、`source-check` 输出、SSH 隧道、迁移脚本 |
| [web/README.md](web/README.md) | 网页插件的结构、存储、图片事务和测试 |
| [plugins/photopainter/README.md](plugins/photopainter/README.md) | CPA 插件的路由、构建、安装、升级和回滚 |
| [AGENTS.md](AGENTS.md) | 给编程助手的工程约束 |

## 安全

- `.env`、配对密钥、Flash 和 SD 卡备份都不要提交到 Git。
- 程序只主动向外连接，不监听网络端口。本机的两个 Unix socket 只有当前用户能访问。
- 数据源管理密钥只用于访问 CPA 和 CPM，不会发给相框。
