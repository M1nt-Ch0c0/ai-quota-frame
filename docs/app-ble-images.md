# 相框 APP BLE 收图

`ble push` 和 `PHOTOFRAME_PUSH_TRANSPORT=ble` 现在使用 `photoframe` 自己的 ABI 2.1 服务。
需要先选择支持该服务的相框；旧 ABI 1 相框或其他 APP 运行时明确返回不可用。
客户端不会抢回应用、回退 HTTP 或使用旧宿主 PNG opcode 15/16/17。
原 `internal/ble/image.go` 和对应实机/模拟回归仍保留，只验证旧宿主通道。

命令、鉴权、设备 ID、90 分钟 BLE 图片预算和 macOS 应用身份保持不变：

```sh
ai-quota-frame ble push --file frame.png --app photoframe
open -n -W "$HOME/Applications/PhotoPainter Renderer.app" --args ble push --file /absolute/path/frame.png
```

协议定义在相框仓库 `docs/ble-image-input.md`。发送器检查签名状态中的运行应用、服务 UUID、
运行代次和服务版本；每次写 106 字节 PNG 数据，以签名 op 13 读取 APP 的 64 字节状态。
BEGIN/CHUNK 只表示接收进度；COMMIT 后必须取得匹配 ID、长度、SHA 的 complete/OK 回执，
才算物理刷新及最终 POWER_OFF 完成。服务仍只在成功后保存 `last-frame.png`。

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
要求设备无 Wi-Fi，验证 APP 5 MiB PSRAM 预约、断线释放、坏 PNG 拒绝和取消；不提交有效图。
`app-image-display` 运行 `TestHardwareAppImageDisplay`，额外需要显式
`PHOTOFRAME_HARDWARE_PNG`，只提交一次有效图，丢弃 APP COMMIT 的签名回复，重连查询原回执，
并验证无重启。需要既有实机授权、停止常驻推图和人工确认画面。测试不改宿主、不自动激活应用。

macOS 恢复服务前保留 `FRAME_THEME=mint-skin`、
`DISPLAY_PROVIDERS=claude:CLAUDE,codex:CODEX,xai:GROK`、`REFRESH_INTERVAL=15m` 和 `TZ=Asia/Shanghai`。
日常服务日志可能含源端内容；维护只检查进程、LaunchAgent 和完成文件元数据。
