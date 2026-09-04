## PhotoPainter 7.3：SD 多 Wi-Fi + 鉴权主动推图（开发预发布）

**未完成真机验收，由用户自行验证。只适用于微雪 ESP32-S3-PhotoPainter 7.3 英寸、800×480 六色屏、16MB Flash / 8MB PSRAM。刷写前必须备份 Flash 和 SD 卡。**

下载 Assets 中的 **photopainter73-sd-wifi-push-dev.zip**，解压后阅读包内 README.md；不必安装 ESP-IDF 或重新编译。请勿误选 GitHub 自动生成的 Source code。

### 使用流程

1. 安装 Python 3.10+ 和 `esptool==5.4.0`，识别实际串口和板型，备份 16MB Flash 与 SD。
2. 在 FAT32 SD 上建立 `config/wifi.json`，按包内示例填入多组 2.4GHz Wi-Fi；数组顺序为优先级，最多 10 组。
3. 用 `openssl rand -hex 32` 生成独立推图码，保存至 SD 的 `config/push-token.txt`。不要复用主机或 CLIProxyAPI 的密钥。
4. 在解压目录运行 `shasum -a 256 -c SHA256SUMS`；全部通过后用真实串口执行 `python3 -m esptool --chip esp32s3 --port /dev/cu.YOUR_DEVICE --baud 460800 write-flash @flash_args`。
5. 重启并查找设备 IP。设置 `PHOTOFRAME_PUSH_TOKEN`，运行 `bash push-photoframe.sh http://设备IP preview.png`。包内 PNG 是演示数据。
6. 运行 `python3 verify_device.py http://设备IP --image preview.png --allow-display` 验证鉴权、错误请求保护及成功推图读回，并肉眼检查屏幕。

### 功能与边界

- SD 列表重启后按顺序连接，每组最多 20 秒，成功即停止；全部失败进入恢复配网，不清空已有列表。
- `POST /api/push` 接收 PNG/JPEG 原始二进制，最大 5MiB，使用 `Authorization: Bearer <码>`。
- 配置推图码后，旧 rotate/display/config/OTA 等写接口和敏感 GET 同样受保护，Web 页面遇到 401 会要求输入码。
- 启动默认关闭自动轮播和深睡，以便接收主动推图，建议 USB 供电。HTTP 不提供加密，只在受信 LAN 使用，不要开放到公网。
- 固件不直接读取 CLIProxyAPI，也没有自动修改主机现有的调度；首次用演示图验证，再推送主机生成的实时六色 PNG。
- 已通过 95 项主机测试、50 项网页测试、4 项验收客户端测试及 ESP32-S3 编译；这些不能代替真实网络切换和刷屏验收。
- 上游锁定 npm 依赖仍有审计告警，未做生产安全发布认证。不要把开发预发布当成稳定生产版。

[完整使用指南](https://github.com/M1nt-Ch0c0/ai-quota-frame/blob/feature/firmware-sd-wifi-push/firmware/FLASHING.md) · [源码和构建说明](https://github.com/M1nt-Ch0c0/ai-quota-frame/tree/feature/firmware-sd-wifi-push/firmware) · [验证记录](https://github.com/M1nt-Ch0c0/ai-quota-frame/blob/feature/firmware-sd-wifi-push/firmware/VERIFICATION.md)

验证反馈请包含硬件型号、启动日志、Wi-Fi 切换结果、HTTP 状态码和屏幕照片；请勿发送 Wi-Fi 密码、推图码或 Flash 备份。
