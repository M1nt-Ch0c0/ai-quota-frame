# PhotoPainter 定制固件：SD 多 Wi-Fi + 主动推图

目标硬件：微雪 ESP32-S3-PhotoPainter，7.3 英寸、800×480 Spectra 6、16MB Flash / 8MB PSRAM。
复用已核对的上游 board HAL 和显示驱动，不修改屏幕引脚。

当前状态：已完成源码实现、主机单元测试及 ESP32-S3 编译；尚未刷入用户设备验证真实 Wi-Fi 切换和刷屏。不要把编译通过视为真机验收通过。

详细测试范围、镜像校验值和未完成项见 [验证记录](VERIFICATION.md)。

## 直接下载刷写包（推荐用于个人验证）

[下载开发预发布包](https://github.com/M1nt-Ch0c0/ai-quota-frame/releases/tag/firmware-sd-wifi-push-v0.1.0-dev.1)，选择 `photopainter73-sd-wifi-push-dev.zip`，不要误选 GitHub 自动生成的 Source code。

解压后按包内 `README.md` 操作；同一份说明也可在线查看：[备份、刷写、SD 配置与验证](FLASHING.md)。刷写包包含已编译镜像、校验文件、演示 PNG、推图和验收脚本，不需要安装 ESP-IDF 来重新编译。

这是未完成真机验收的预发布版，不修改主干，不建议无人值守部署。验证后请反馈硬件型号、启动日志、两组 Wi-Fi 切换结果、HTTP 状态码和屏幕照片；请勿发送 Wi-Fi 密码或推图码。

## SD 卡配置

在 FAT32 SD 卡创建 `config/wifi.json`，格式参见 `wifi.example.json`：

```json
{"version":1,"networks":[
  {"ssid":"Home-2.4G","password":"replace-home-password"},
  {"ssid":"Office-2.4G","password":"replace-office-password"}
]}
```

- 最多 10 组；数组顺序就是连接优先级。重启后按顺序尝试，每组最多等待 20 秒，一旦成功就停止。
- 失败不删除记录；全部失败进入恢复配网页面。配网页面成功保存会追加新 SSID，或更新同名记录的密码，其他记录保留。
- 删除/调整优先级：关机后编辑 SD 文件，再启动。已有合法 SD 列表是权威来源，空列表不会偷偷从旧 NVS 恢复。
- 首次没有 JSON 时，从原来的 NVS 单组信息或旧 `wifi.txt` 迁移。没有 SD 时保留原 NVS 单组连接兼容路径。
- SSID 支持 1–32 字节；密码为 8–63 个可打印 ASCII 字符，或 64 位十六进制 PSK。空密码表示开放网络；只支持 2.4GHz。
- 文件保存采用 `.tmp` 写入、同步、`.bak` 备份和发布；启动时可从中断重命名留下的 `.bak` 恢复。损坏文件不会被静默覆盖。
- SD 中密码是明文，这是可移动卡配置的代价；请保护卡片，不要把真实文件提交到 GitHub。

另在 SD 创建 `config/push-token.txt`，内容是一行随机鉴权码，推荐用 `openssl rand -hex 32` 生成的 64 位十六进制字符串。不要复用 OAuth token 或 CLIProxyAPI 管理密钥。

- 支持 32–128 个非空白可打印字符；码只用于本设备入站 API，和主机 `FRAME_ACCESS_TOKEN` 不同。
- 启动时导入 NVS；取走 SD 后鉴权仍有效。更新卡上的码并重启即可轮换。没有任何 HTTP GET 会返回这个码。
- 无码时 `/api/push` 禁用；码损坏或保存失败时拒绝所有受保护请求，不能降级为无鉴权。
- 配置推图码后，每次启动默认关闭自动轮播和深睡，让设备持续在线。建议 USB 供电；电池耗电会明显增加。鉴权后的配置操作仍可显式调整设置。
- 原 `/api/rotate`、`/api/display-image`、配置写入、OTA、恢复出厂等所有修改状态的 API，以及读取完整配置/调试日志，都要求同一个 Bearer 码。Web 界面遇到 401 会要求输入码，仅保存在当前页面内存中。

## 主动推图

```http
POST /api/push
Authorization: Bearer <device-push-code>
Content-Type: image/png

<PNG binary bytes>
```

PNG/JPEG 原始二进制 body，最多 5MiB；不接受 URL、JSON 包装或 multipart。推荐直接发送主机生成的 800×480 六色 PNG，继续使用无二次抖动的 processed-PNG 路径。

```bash
export PHOTOFRAME_PUSH_TOKEN='设备的独立推图码'
bash scripts/push-photoframe.sh http://192.168.1.50 docs/preview.png
```

成功响应 200 表示显示流程已完成，不是仅仅收到数据；电子纸全刷约 25 秒，客户端应允许 90 秒。401 表示鉴权失败，413 超过 5MiB，415 类型不支持，400 空数据/格式不匹配，503 未配置鉴权、设备初始化或忙。上传超时和 SD 写入失败会清理临时文件；不要对未知结果的请求无限重试。

HTTP 鉴权不提供传输加密，只用于受信 LAN/VLAN；不要端口映射或直接暴露公网。设备深睡、关机或离线时无法接收 POST。

## 从源码重建

`upstream.env` 固定上游和 ESP-IDF revision；`patches/0001-sd-wifi-auth-push.patch` 包含全部定制代码和测试，`dependencies.lock` 固定组件依赖。上游 MIT 许可不变。

1. 安装 Git、Node.js（上游建议 20+）、CMake、Ninja、Python 3.10+。macOS 的启动画面生成需要 `brew install pkgconf cairo pango librsvg libpng`。
2. 检出 `upstream.env` 指定的 ESP-IDF revision，递归初始化 submodules，运行 `./install.sh esp32s3`，然后 `. ./export.sh`。在该虚拟环境安装 `qrcode`。
3. 在项目根运行 `bash firmware/build.sh`。输出在 `dist/firmware/`，包括 app、merged.bin 与 SHA256SUMS。npm 若提示阻止 canvas 安装脚本，需要审核并允许该依赖的安装脚本后再生成启动画面。

`bash firmware/prepare.sh` 仅准备源码，可设置 `FIRMWARE_SOURCE_DIR` 到独立位置。脚本验证 revision、补丁可逆性和依赖锁；不会重置或覆盖其他用户改动。

主机测试：准备源码后运行 `cmake -S host_tests -B build-host -DCMAKE_POLICY_VERSION_MINIMUM=3.5`、`cmake --build build-host`、`ctest --test-dir build-host --output-on-failure`。需要系统 libpng；cJSON 与 GoogleTest 使用固定版本。

验收客户端自身的测试：在本项目根目录运行 `python3 -m unittest discover -s firmware -p 'test_*.py'`。这些测试只验证客户端停止条件，不能代替真机结果。

## 真机验收（尚待执行）

刷写前先核对串口和 SKU，备份原 Flash 与 SD。新分区表/merged 镜像可能覆盖旧 NVS；不要在未备份时盲刷。不要把此开发包当作上游 OTA 更新安装。

在 ESP-IDF 环境中，把下面的串口占位符替换为实际设备串口。先执行只读识别，确认 ESP32-S3、16MB Flash，并人工核对 PhotoPainter 7.3 SKU。不要把其他板型当作同一设备：

```bash
python3 -m esptool --port /dev/cu.YOUR_DEVICE chip-id
python3 -m esptool --port /dev/cu.YOUR_DEVICE flash-id
```

创建一个新的私有备份目录（Flash 内包含 Wi-Fi 等敏感配置），读取完整 16MB；另行复制 SD 卡备份。以下是操作说明，开发过程中尚未对用户设备执行：

```bash
backup_dir=$(mktemp -d "$PWD/photoframe-backup.XXXXXX")
python3 -m esptool --chip esp32s3 --port /dev/cu.YOUR_DEVICE read-flash 0 0x1000000 "$backup_dir/original-flash.bin"
```

确认备份成功后才刷写。使用构建产物中原始分段地址（不要自行猜偏移），不需要全片擦除：

```bash
cd dist/firmware
shasum -a 256 -c SHA256SUMS
python3 -m esptool --chip esp32s3 --port /dev/cu.YOUR_DEVICE --baud 460800 write-flash @flash_args
```

复原时使用同一已确认设备和原 Flash 备份；完整恢复会覆盖这次固件和当前设置。分段刷写仍会替换分区表及 OTA 初始状态，必须保留备份。

- 两个真实 2.4GHz 网络：优先网络可用时连接它；关闭优先网络并重启，自动连接第二个；再重启，列表仍完整。
- 两个都不可用时进入配网 AP；恢复或追加网络后可重新启动连接，原列表没有被清空。
- 错误或缺失 Bearer 推图、旧 rotate、配置写入都返回 401，屏幕不刷新。
- 正确码推送标准六色 PNG 后屏幕显示该图；SD 上的当前图可由 `/api/current_image` 读回比对。
- 断电重启、移除 SD 后，原码仍有鉴权效力；错误码不能调用受保护 API。
- 超大、损坏和传输中断的图片不能被当作成功显示；上一次可读取图片应保留。

设备启动且 SD 已配置后，可在项目根运行自动接口验收。它先确认配置读取受到保护，才检查旧写接口；不跟随重定向、不打印密钥或配置正文。第一条仅做拒绝路径检查；第二条明确授权用指定图片替换当前屏幕：

```bash
export PHOTOFRAME_PUSH_TOKEN='设备的独立推图码'
python3 firmware/verify_device.py http://192.168.1.50
python3 firmware/verify_device.py http://192.168.1.50 --image docs/preview.png --allow-display
```

验收会检查 401/413/415/400、失败请求保留原图，以及正确推图后的精确字节读回；仍需肉眼确认电子纸画面，并人工验证两个网络的断电重启切换。若服务响应不符合预期会立即停止，不自动重试推图。

当前开发机未发现相框 USB 串口，因此上述硬件条件还没有被验证。
