# macOS BLE 服务部署记录

2026-10-22 在 Apple Silicon Mac、macOS 26.3.1 上部署。代码使用
`codex/ble-discovery-app-ota` 分支，实测程序提交为 `d263871`。
四个仓库的同名分支及协议、SDK、架构文档均已同步到 GitHub 和 Mac。

## 已验证

- 使用 Go 1.26.0 原生编译。`make check` 的单元测试、race、vet 和
  CGO 构建通过；macOS 的 CoreBluetooth 需要 CGO，不能使用仅 HTTP 的
  `CGO_ENABLED=0` 产物代替。
- 使用 Google Chrome 156.0.8078.47 运行渲染测试，包括尺寸、内容稳定性
  和并发渲染。只用于人工重生成文档预览的测试保持默认跳过。
- 使用现有网关身份获取真实数据，生成 12865 字节预览：5 个账号、
  `stale=false`、包含用量统计，有 1 条源端警告。独立解码检查通过：
  800×480、非隔行、完全不透明、精确六色。这是本地生成验证，未发送到屏幕。
- 程序 SHA-256：
  `8cda0a19435f6e792e021efcfbeddc94e7b43a26da33d94ab79c3797687036b8`。
- `com.m1ntch0c0.ai-quota-frame` LaunchAgent 已安装并启动，登录启动、
  退出后重启。健康判断仍是进程存活，不增加 HTTP 健康端口。

## 当前运行条件

相框与 Mac 不在同一地点，用户暂时不能操作 Mac 的图形界面授予蓝牙权限。
Mac 蓝牙硬件开关已开启，但应用扫描报告权限不可用。因此本次不声称
macOS 的实机发现、应用 OTA 或实体推图已经通过。

Mac 服务已配置已知设备 ID，并清除了旧的固定推图 IP。没有鉴权发现到
可用地址时，程序不会向旧 IP 发送设备令牌或图片；它保持采集、渲染及
有间隔的发现重试。Windows 服务继续负责现有相框，没有在本次部署中停止。
SSH 能管理 Mac，但不会把 Mac 的蓝牙射频延伸到另一地点。

应用位于 `~/Applications/PhotoPainter Renderer.app`。它声明蓝牙和本地网络
用途，CLI 参数通过同一启动器传递，使用同一份私有运行配置。macOS 权限
必须在该机器的图形界面授予；本次没有修改 TCC 数据库或绕过系统权限。

## 数据源与私有配置

Mac 原配置依赖已不可用的源站 SSH 隧道。本次改为已有的 HTTPS 网关和
Google 服务账号 ID Token，不再启动旧的源站隧道。

仅通过加密 SSH 传送位于原机器源码仓库之外的专用服务账号文件；没有复制
源码仓库中的 `.env`、SSH 私钥、NVS 或设备备份。Mac 已有的设备推图令牌
与显示配置保留，旧的管理密钥从活动配置中移除。运行配置和服务账号文件
均为权限 0600，位于 `~/Library/Application Support/ai-quota-frame`，不在 Git 中。
原配置保存在同一私有目录的 `.env.before-gateway-*`，旧程序和启动脚本在
`previous-*`，用于回退；不要打印或提交这些文件。

本机直连 Google 鉴权接口不可用，使用已有的 `127.0.0.1:7890` 系统代理。
为本服务配置了 `HTTP_PROXY`、`HTTPS_PROXY` 和本地地址的 `NO_PROXY`。
该代理必须保持可用；其它机器不应照抄端口。设备推图的 HTTP transport
仍明确禁用代理和重定向。构建期间使用的临时代理不属于常驻服务依赖。

## 相框移到 Mac 附近后

1. 先停用 Windows 自动推图及其看门狗，确认旧进程已经退出。
2. 保持相框独立 USB 供电，使其处于 Mac 的蓝牙范围内。
3. 在 Mac「隐私与安全性」中允许 PhotoPainter Renderer 使用蓝牙和本地网络。
4. 通过应用启动器查询状态，确认鉴权成功；PNG 推图还需要 Mac 能访问设备的 IP。
5. 完成一次 HTTP 200 和人工目测验收，再将 Mac 作为唯一日常推图端。

```sh
open -n -W "$HOME/Applications/PhotoPainter Renderer.app" --args ble status
launchctl print gui/$(id -u)/com.m1ntch0c0.ai-quota-frame
```

`last-frame.png` 只在服务收到设备 HTTP 200 后保存。`validation/preview.png`
是本次本地生成的预览，不能当作实体刷新成功的证据。查看运行状态优先使用
LaunchAgent 和进程信息，不要打开可能含敏感源端内容的日志。
