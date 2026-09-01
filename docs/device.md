# Waveshare E6 PhotoPainter 接入

本项目把 OAuth 和 CLIProxyAPI 留在主机上，电子相框只拿一个局域网 Bearer token 请求已渲染的图片：

```text
OAuth accounts -> CLIProxyAPI -> ai-quota-frame :8787
                                      |
                                      +-- GET /api/v1/frame.png
                                               |
                                      Wi-Fi LAN + Bearer token
                                               |
                                ESP32-S3-PhotoPainter 800x480
```

## 1. 先核对 SKU

本文档只适用于微雪 **ESP32-S3-PhotoPainter**：

- 7.3 英寸 Spectra 6 六色屏，800x480。
- 主控为 ESP32-S3-WROOM-1-N16R8，16 MB Flash + 8 MB PSRAM。
- 官方产品页：<https://www.waveshare.com/esp32-s3-photopainter.htm>
- 官方 Wiki：<https://www.waveshare.com/wiki/ESP32-S3-PhotoPainter>
- 官方源码：<https://github.com/waveshareteam/ESP32-S3-PhotoPainter>

它不是 `ESP32-S3-ePaper-13.3E6` 裸板。13.3 英寸 E6 板与 PhotoPainter 的屏幕尺寸、引脚和驱动不同，下面的预编译固件不支持它。不确定时，先核对购买链接或 PCB 上的产品名。

E6 只支持约 25 秒的全屏刷新，官方 FAQ 明确说不支持快刷。因此不要设成每分钟刷屏；本方案依靠 ETag/304 在数据没变时完全跳过屏幕刷新。

## 2. 固件选择与刷写

使用 <https://github.com/aitjcize/esp32-photoframe> 的 `waveshare_photopainter_73` board profile。本接入契约已按 commit `bf0298263310c3fa023d42eca1e22f55948f1e50` 核对；它原生支持 PhotoPainter、URL 轮询、Bearer token、ETag/304、PNG 和深度睡眠。

这里没有重写一套未验证的 E6 驱动。所用固件的 [PhotoPainter board HAL](https://github.com/aitjcize/esp32-photoframe/blob/bf0298263310c3fa023d42eca1e22f55948f1e50/components/board_hal/include/board_waveshare_photopainter_73.h) 与 [board driver](https://github.com/aitjcize/esp32-photoframe/blob/bf0298263310c3fa023d42eca1e22f55948f1e50/components/board_hal/src/driver_waveshare_photopainter_73.c) 专门适配这块官方硬件，并复用微雪电子纸驱动源。其绑定的 EPD 引脚与官方板级代码一致：SCLK GPIO10、MOSI GPIO11、DC GPIO8、CS GPIO9、RST GPIO12、BUSY GPIO13。本项目只提供主机数据服务和已核验的固件配置，不要在未核对 SKU 时手工套用这些引脚。

### 用 PlatformIO Core 刷官方 merged.bin

该仓库没有 `platformio.ini`，`waveshare_photopainter_73` 是固件自己的 board profile，不是 PlatformIO environment。不要执行不存在的 `pio run -e waveshare_photopainter_73`。可以用 PlatformIO 包管理器提供的 `tool-esptoolpy` 刷官方预编译镜像：

```bash
python3 -m pip install --upgrade platformio

curl -fL \
  -o photoframe-firmware-waveshare_photopainter_73-merged.bin \
  https://github.com/aitjcize/esp32-photoframe/releases/download/v2.18.0/photoframe-firmware-waveshare_photopainter_73-merged.bin

printf '%s  %s\n' \
  41a680d59ae65f37ef581fd66568a988fd0e64469617651b1a1ec98e77fd30b3 \
  photoframe-firmware-waveshare_photopainter_73-merged.bin | sha256sum --check

PORT=/dev/ttyUSB0
pio pkg exec --package platformio/tool-esptoolpy -- \
  esptool.py --chip esp32s3 --port "$PORT" --baud 921600 \
  write_flash 0x0 photoframe-firmware-waveshare_photopainter_73-merged.bin
```

macOS 的串口通常是 `/dev/cu.usbmodem*` 或 `/dev/cu.usbserial*`，Windows 使用如 `COM5` 的端口名。如果不能进入下载模式，按住 BOOT，再点按 PWR。

也可直接使用项目的 [Web Flasher](https://aitjcize.github.io/esp32-photoframe/#flash)。如需从源码编译，官方路径是 ESP-IDF v6.0+ 而不是 PlatformIO：

```bash
./build.py --board waveshare_photopainter_73
idf.py -p /dev/ttyUSB0 flash monitor
```

## 3. 首次 Wi-Fi 配网

PhotoPainter 只连 2.4 GHz Wi-Fi。首次启动可以二选一：

1. 连接设备创建的 `PhotoFrame - A1B2C3` 热点，然后打开 `http://192.168.4.1`。
2. 在 FAT32 microSD 卡根目录或 `config/` 目录放置 `wifi.txt`：

   ```text
   YourWiFiSSID
   YourWiFiPassword
   MyPhotoFrame
   ```

   第三行设备名可省略。v2.18.0 会读取该文件，但不会自动删除；配网成功后应自行从 microSD 移走或删除，避免 Wi-Fi 密码长期明文保留。

配网完成后，从路由器查看设备 IP，或访问 `http://photoframe.local`。设备深度睡眠时 Web API 不可达，先按 BOOT 唤醒再配置。URL 模式即使没有 SD 卡也可用；SD 卡主要用于持久化图库和可选的 `wifi.txt` 配网。

## 4. 先让主机 API 对局域网可达

`ai-quota-frame` 默认监听 `:8787`。显式配置一个高强度 token：

```bash
export LISTEN_ADDR=:8787
export FRAME_ACCESS_TOKEN="$(openssl rand -hex 32)"
```

`FRAME_URL` 必须写主机在同一 Wi-Fi/LAN 上的真实地址，例如：

```text
http://192.168.1.10:8787/api/v1/frame.png
```

不能写 `127.0.0.1`、`localhost` 或 `0.0.0.0`：这些地址从 ESP32 看到的不是主机服务。同时确认主机防火墙允许 LAN 访问 TCP 8787，Wi-Fi 没有开启 AP/client isolation。

`FRAME_ACCESS_TOKEN` 只是相框图片 API 的 token，不是 OAuth token，也不是 `CLIPROXY_MANAGEMENT_KEY`。后两者不应下发给 ESP32。

## 5. 一键配置 PhotoFrame

在本项目根目录执行：

```bash
export DEVICE_URL=http://192.168.1.50
export FRAME_URL=http://192.168.1.10:8787/api/v1/frame.png
export FRAME_TOKEN="$FRAME_ACCESS_TOKEN"
export ROTATE_CRON='*/10 * *'
./scripts/configure-photoframe.sh
```

其中 `FRAME_TOKEN` 必须与主机的 `FRAME_ACCESS_TOKEN` 完全一致。脚本不会盲目写入配置，它会依次：

1. 用 Bearer token 读取 `frame.png`，检查 800x480 请求、PNG magic、ETag 和 304。
2. 读取设备 `/api/system-info`，确认 board 为 `waveshare_photopainter_73` 且分辨率为 800x480。
3. 在 PATCH 之前读取并解析现有 `/api/config`。
4. PATCH 配置后再次 GET，逐字段确认已生效。

按当前已核对的上游实现，PhotoFrame 的 `/api/system-info`、`/api/config` 和 `/api/rotate` 都直接注册为无认证的局域网 API；这里不需要、也不应向设备 API 附加 `FRAME_TOKEN`。而且 `GET /api/config` 会返回已配置的 `access_token`。因此只能把相框放在受信 LAN/VLAN，不要做路由器端口映射，也不要把设备 AP/API 暴露给不受信用户。

脚本写入的精确 JSON 结构如下：

```json
{
  "display_orientation": "landscape",
  "display_rotation_deg": 180,
  "auto_rotate": true,
  "rotate_cron": ["*/10 * *"],
  "rotation_mode": "url",
  "image_url": "http://192.168.1.10:8787/api/v1/frame.png",
  "access_token": "same-as-FRAME_ACCESS_TOKEN",
  "http_header_key": "",
  "http_header_value": "",
  "save_downloaded_images": false,
  "deep_sleep_enabled": true
}
```

`rotate_cron` 是简化的 **3 字段** cron：`minute hour day-of-week`，不是 Linux 常见的 5 字段 cron。例如：

- `*/10 * *`：每 10 分钟检查一次。
- `0 8-22 *`：每天 08:00 到 22:00 整点检查。
- `0 9 1-5`：周一到周五 09:00 检查。

支持 `*`、单值、范围、`*/n`、`a-b/n` 和逗号列表；星期 `0`/`7` 都表示周日。由于屏幕每次真正刷新约需 25 秒，建议从 10 分钟或更长周期开始。

Cron 使用设备自身的 POSIX 时区设置，不跟随运行主机的 `TZ`；v2.18.0 出厂默认是 `UTC0`。`*/10 * *` 这类纯间隔规则不受时区影响；使用 `0 8-22 *` 或 `0 9 1-5` 前，应先在 PhotoFrame Web 设置中把 **Timezone (UTC offset)** 配为所在地时区（中国标准时间为 UTC+8，设备保存为 POSIX 字符串 `UTC-8`）。

## 6. Bearer + ETag/304 契约

PhotoFrame 每次 URL rotation 会发送：

```http
GET /api/v1/frame.png HTTP/1.1
Authorization: Bearer <FRAME_ACCESS_TOKEN>
X-Display-Width: 800
X-Display-Height: 480
X-Display-Orientation: landscape
If-None-Match: "<previous-etag>"
```

首次请求没有 `If-None-Match`，主机返回 `200 image/png` 和 `ETag`。固件持久化该 ETag，下次原样回传。图片未变时主机返回 `304 Not Modified`，固件会跳过下载、解码、抖动和电子纸刷新。Bearer 校验在 ETag 判断之前进行，所以条件请求也必须带 token。

本项目直接返回固件原生支持的 PNG。不要把自定义 `E6F1` 头或 `frame.e6` 配给相框；`esp32-photoframe` 不识别该格式。

## 7. 触发一次真机验收

配置脚本不会自动让 E6 全屏刷新。确认配置正确后，按相框 KEY 键，或手动调用：

```bash
curl --fail-with-body --request POST "$DEVICE_URL/api/rotate"
```

首次应下载 PNG 并进行约 25 秒的全刷。在主机数据不变的情况下再触发一次，应收到 304，屏幕不刷新。可通过以下命令查看设备上一次拉取错误：

```bash
curl --fail-with-body "$DEVICE_URL/api/config"
```

重点字段是 `last_fetch_error`。

## 8. 常见问题

- `401 unauthorized`：设备 `access_token` 与主机 `FRAME_ACCESS_TOKEN` 不同。
- `400 ... 800x480`：刷入了错误 board profile，或请求头报告了错误分辨率。
- `503 quota data is not ready`：主机服务已起，但 CLIProxyAPI 数据还没完成首次刷新。
- 主机 curl 成功、相框失败：检查 `FRAME_URL` 是否使用主机 LAN IP、防火墙、客人 Wi-Fi 隔离和 2.4/5 GHz 网段间路由。
- 设备 API 不可达：先按 BOOT 唤醒，再确认 IP 是否由 DHCP 变更。
- 配置 HTTPS 失败：固件会在修改 HTTPS `image_url` 时获取并 pin 服务器证书；自签名证书需要额外处理。MVP 建议只在受信局域网用 HTTP，不要把 8787 端口暴露到公网。
- PhotoPainter 无故重启：已知 AXP2101 版本同时接 Type-C 和锂电池可能不稳定，排查时只保留一种供电方式。
