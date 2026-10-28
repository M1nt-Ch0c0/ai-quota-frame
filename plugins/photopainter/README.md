# PhotoPainter CPA 插件

当前开发候选 0.2.2，C ABI 1 / RPC schema 7，**尚未实机验证、尚未部署**。0.2.1 为夜间任务开始前的线上历史基线；本轮不修改线上 CPA/CPM 或运行容器。CPM 继续使用官方前端；插件注册“发送到相框”菜单，提供浏览器配对界面和与原生 ai-quota-frame 共用的 Go/WASM 引擎。

## 路由

公开静态资源：

- `/v0/resource/plugins/photopainter/panel`
- `/v0/resource/plugins/photopainter/frame.wasm`（gzip）
- `/v0/resource/plugins/photopainter/wasm_exec.js`

受既有管理鉴权保护的只读接口：

- `GET /v0/management/plugins/photopainter/account-subject?name=...`

该窄接口只为 xAI 返回 `{"sub":"..."}`。它通过官方 host auth 回调定位唯一的 xAI 记录，核对 auth_index/name/provider，只投影 subject；不返回 OAuth 文件、访问/刷新令牌或文件路径。匿名管理请求仍为 401，静态资源前缀不提供此数据接口。

插件不注册模型、执行器、OAuth 或计费 hook，不增加监听端口，不代理蓝牙。配对和图片收发由访问页面的浏览器执行。使用、存储和恢复规则见 [web/README.md](../../web/README.md)。

## 构建

```sh
./plugins/photopainter/build.sh
```

完整构建需要 Node 22.18+、Go 1.26+ 和目标平台 C 编译器。脚本构建完整网页/共享引擎，将三项生成资源嵌入 native `.so`，再通过 `build-native.sh` 运行原生测试并输出 `dist/photopainter-v<version>.so`。生成文件和 dist 均忽略，不提交二进制或凭据。

生产为 Linux amd64，不能部署 macOS 产物。本轮使用已有固定 builder：

```text
golang@sha256:05237276236793ebedaf0b87dab86add9ec9adc66ebef2dbc3107c18254cde1c
```

网页先在本地完成构建，复制 `panel.html`、`frame.wasm.gz`、`wasm_exec.js` 到本目录，再在该 Linux builder 中以 `--network none` 运行 `./build-native.sh`；Go 原生包装只依赖标准库。CI 也使用这个固定 builder，而非把较新 Ubuntu 的 GLIBC 依赖带入 Debian 12 生产环境。

如果在 macOS 上用 Zig 交叉编译 Linux，先用本机工具链完成 `go test ./...` 和本机 C ABI 验证，再单独交叉构建；不能在 macOS 上把 Linux 测试二进制直接执行并称为通过。Linux CPA 实际加载仍需独立验收。

```sh
# 本目录中运行，三个生成资源须已复制到位。
go test ./...
CGO_ENABLED=1 go build -buildvcs=false -trimpath -buildmode=c-shared \
  -o /tmp/photopainter-darwin-arm64.so .
python3 verify-native.py /tmp/photopainter-darwin-arm64.so ../../web/dist/panel.html

# Zig 路径按本机安装调整；这一步只构建 Linux 产物，不执行它。
GOOS=linux GOARCH=amd64 CGO_ENABLED=1 \
  CC='zig cc -target x86_64-linux-gnu.2.34' \
  go build -buildvcs=false -trimpath -buildmode=c-shared \
  -o dist/photopainter-v0.2.2.so .
```

验证真实导出 ABI 与三项内嵌资源：

```sh
python3 plugins/photopainter/verify-native.py \
  plugins/photopainter/dist/photopainter-v0.2.2.so web/dist/panel.html
```

## 安装、升级与回滚

以当前 CPA 8.0.45 / CPM 1.14.9 为验证基线。先备份配置、插件并记录容器/文件摘要。新 `.so` 置于既有 `plugins.dir/linux/amd64/`，文件 0644，运行 UID 可读。既有配置保留，仅启用 `plugins.configs.photopainter`。

版本升级使用新的版本文件名，保留旧库以便回滚。当前版本可在配置变化时扫描并热加载较新产物；测试已覆盖 0.1.0→0.2.0 且 Qoder 共存，CPA PID 不变。生产升级后必须验证真正加载的版本、菜单、完整资源摘要、匿名拒绝、受保护 subject 投影及其他容器/插件保持原状，不能仅凭文件复制成功宣布安装完成。

CPA 8.0.45 仅监视配置文件原 inode。外部写入须保留 inode，不能原子 rename 覆盖配置；那会使后续监听丢失。此前 0.1.0 首次安装已经遇到并回滚此问题，在用户批准窗口重启 CPA 后恢复监听。后续升级已使用保留 inode 的热加载，不需要重启整个栈。

停止本插件时仅关闭其 enabled 并验证菜单/资源撤销。回滚先保存当前状态，再撤下新库、恢复本插件对应配置变化并触发热加载；保留其他人的后续配置/凭据修改。不得无条件恢复整个旧配置或数据库。浏览器未确认事务不因软件回滚而成为“已取消”。

## 资源策略

页面使用同源脚本、WASM、离屏 srcdoc 布局和 data/blob 图片。CSP 仅开放所需来源与 `wasm-unsafe-eval`；frame-ancestors 保持 self，设备访问策略为 bluetooth=(self)。CPM 管理密钥只在页面内存中，服务端不为插件另存它；设备协商密钥不进入服务器。

## 当前证据

原生 Go 测试、真实 C ABI/内嵌资源逐字节比对、精确 CPA 镜像 `network=none` 隔离升级与 Qoder 共存验证通过。subject 投影、匿名 401、公开前缀 404 已在隔离环境验证；生产 0.2.0 的实际 xAI subject 投影也通过，未输出字段值或凭据。

0.2.0 生产热升级备份：`/var/backups/cpa-stack/photopainter-v2-20261028T194729Z`。所有容器保持原 ID/PID/启动时间，Qoder 与旧 0.1.0 文件保留。

上述线上记录属于夜间任务前的历史证据。0.2.2 今晚未部署、未访问生产，离线新结果见 [夜间验证记录](../../web/validation-2026-10-29.md)。

共享渲染、浏览器软件测试和真实设备验收是不同证据层次。设备固件与客户端配对 v2 已实现并构建，但在物理按键、重启和最终显示完成前，不能宣称整套目标已经闭环。
