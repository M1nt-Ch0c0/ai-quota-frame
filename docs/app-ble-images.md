# 相框 APP BLE 收图

`ble push` 和 `PHOTOFRAME_PUSH_TRANSPORT=ble` 使用 `photoframe` 自己的应用服务。
需要先选择支持该服务的相框；旧 ABI 1 相框或其他 APP 运行时明确返回不可用。
客户端不会抢回应用、回退 HTTP 或使用旧宿主 PNG opcode 15/16/17。

## macOS CoC 路径（2026-10-27）

Mac 客户端在签名状态包含 `coc_version=1` 和非零 `coc_psm` 时优先使用 ABI 2.2 CoC。
仍先通过现有 GATT 双向鉴权和校验当前 APP，再由原生 CoreBluetooth 适配连接同一
peripheral 的 CoC。现有 Go 蓝牙库的私有字段和依赖缓存未修改；新桥接位于
`internal/ble/coc_darwin.m`，原有连接在整个操作期间保留。

CoC hello 验证运行代次、会话和 HMAC，派生两方向独立密钥；每条记录带长度、序号和
HMAC。长度封套处理 CoreBluetooth 字节流的拆分/合并。详细格式见宿主
`docs/abi-2-2-coc.md`。连接最长等待 20 秒，hello 最长等待 15 秒，后续遵守原图片操作上下文。

BEGIN 等待 APP 接纳后连续发送较大的 CHUNK，不等待逐块业务回执；最后发送 COMMIT，
只有验证匹配请求 ID、长度、SHA 和 complete/OK 的回执才报告显示成功。进度表示送入
本地流的数据量，不作为远端接收或物理完成证明。

COMMIT 发送前即标记结果可能不确定；重试只查询同一请求。代次变化或原回执缺失时
保持原有停止自动重刷策略。CoC 开启后失败不会在同次尝试中偷偷重发为 GATT。

旧设备未声明能力，或 Linux/Windows 尚无本项目 CoC 原生后端时，使用原有 GATT。
macOS 新路径已于2026-10-27完成一轮真实推图：67,509字节发送阶段2.477秒，随后取得APP物理完成回执，表明该轮CoreBluetooth连接与CoC收发可用。尚未完成各类断线、长时间耐久或跨平台CoC验证。
原 `internal/ble/image.go` 和对应实机/模拟回归仍保留，只验证旧宿主通道。

命令、鉴权、设备 ID、90 分钟 BLE 图片预算和 macOS 应用身份保持不变：

```sh
ai-quota-frame ble push --file frame.png --app photoframe
open -n -W "$HOME/Applications/PhotoPainter Renderer.app" --args ble push --file /absolute/path/frame.png
```

协议定义在相框仓库 `docs/ble-image-input.md`。发送器检查签名状态中的运行应用、服务 UUID、
运行代次和服务版本。以下106字节/签名op13描述仅属于保留的GATT路径；CoC连续发送大块数据，成功CHUNK不逐块读取回执。
BEGIN/CHUNK 只表示接收进度；COMMIT 后必须取得匹配 ID、长度、SHA 的 complete/OK 回执，
才算物理刷新及最终 POWER_OFF 完成。服务仍只在成功后保存 `last-frame.png`。

宿主定时事件可能短暂占用共享接纳锁；仅对签名 busy（宿主码 3，证明命令没有被接纳）
等待 50 毫秒后重试。传输错误或鉴权错误不会触发这种重试。
COMMIT 前断线可用新请求 ID 重传。COMMIT 可能到达设备后，重试只查询原请求。
切出、重启或回执缺失/不匹配会永久停止本进程对该图片摘要的自动重试；中间换过快照也不清除该决定。
仅依赖 RAM，不保证跨程序重启的永久去重。相框切出时其收图内存与服务注销，宿主发现与应用 OTA 常驻。

`make check` 验证新路径和保留的旧路径。`app_image_test.go` 覆盖签名服务选择、106 字节分块、
丢写回复、丢状态读取回复、提交前取消、迟到的原生完成、失败/不确定回执、快照替换后的身份保留；
任何新路径测试误用宿主图片或管理 opcode 都会失败。

编译显式实机测试：

```sh
GOTOOLCHAIN=go1.26.0 go test -tags photopainter_hardware -c -o dist/photopainter-app-hardware ./internal/ble
```

`PHOTOFRAME_HARDWARE_TEST=app-image-safety` 运行 `TestHardwareAppImageSafety`，
要求设备无 Wi-Fi，验证 APP 5 MiB PSRAM 预约、断线释放、坏 PNG 拒绝、取消和 60 秒 APP 空闲超时；不提交有效图。
`app-image-display` 运行 `TestHardwareAppImageDisplay`，额外需要显式
`PHOTOFRAME_HARDWARE_PNG`，只提交一次有效图，丢弃 APP COMMIT 的签名回复，重连查询原回执，
并验证无重启。`app-image-lifecycle` 运行 `TestHardwareAppImageLifecycle`，要求已确认的 ABI 2
`color-test`，验证部分收图时切出释放、服务消失、旧代次读写拒绝及切回恢复。
需要既有实机授权、停止常驻推图和人工确认画面。测试不改宿主、不自动激活应用。

macOS 恢复服务前保留 `FRAME_THEME=mint-skin`、
`DISPLAY_PROVIDERS=claude:CLAUDE,codex:CODEX,xai:GROK`、`REFRESH_INTERVAL=15m` 和 `TZ=Asia/Shanghai`。
日常服务日志可能含源端内容；维护只检查进程、LaunchAgent 和完成文件元数据。
