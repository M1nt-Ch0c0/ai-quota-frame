# PhotoPainter 7.3 开发固件：个人验证指南

**仅适用于微雪 ESP32-S3-PhotoPainter 7.3 英寸、800×480 六色屏、16MB Flash / 8MB PSRAM。未完成真机验收。先备份，再刷写。**

本指南命令在下载的 `photopainter73-sd-wifi-push-dev.zip` 解压目录运行。包内 `preview.png` 是演示数据，用来验证画面，不代表你的实时额度。

## 1. 准备刷写工具和备份

macOS/Linux 需要 Python 3.10+。终端进入解压目录后安装工具：

```bash
python3 -m venv .venv
source .venv/bin/activate
python3 -m pip install 'esptool==5.4.0'
ls /dev/cu.*
```

在 macOS 上用 USB **数据线**连接相框，从串口列表找新出现的 USB 设备。Linux 可检查 `/dev/ttyACM*` 或 `/dev/ttyUSB*`。下文的 `/dev/cu.YOUR_DEVICE` 必须换成你的真实串口；不能选择 Bluetooth 或 debug-console。

关闭其他占用串口的工具，执行只读识别：

```bash
python3 -m esptool --port /dev/cu.YOUR_DEVICE chip-id
python3 -m esptool --port /dev/cu.YOUR_DEVICE flash-id
```

确认 ESP32-S3、16MB Flash，并人工核对上述硬件型号。无法自动进入下载模式时，依照板子的 BOOT/RESET 标识进入下载模式：按住 BOOT，点按 RESET，再松开 BOOT；重新查看串口，它可能变化。若识别结果不同，停止，不要强刷。

读取完整 Flash 备份，并另行复制 SD 卡全部内容：

```bash
backup_dir=$(mktemp -d "$PWD/photoframe-backup.XXXXXX")
python3 -m esptool --chip esp32s3 --port /dev/cu.YOUR_DEVICE read-flash 0 0x1000000 "$backup_dir/original-flash.bin"
```

确认备份成功且文件为 16,777,216 字节。备份含敏感配置，只留在你自己的设备上，不要上传 GitHub 或发给他人。

## 2. SD 卡写入多组 Wi-Fi 和鉴权码

关机，取出 FAT32 SD 卡，在卡根目录创建 `config` 文件夹。复制包内 `wifi.example.json` 为卡上的 `config/wifi.json`，编辑为自己的网络，例如：

```json
{
  "version": 1,
  "networks": [
    {"ssid": "Home-2.4G", "password": "replace-home-password"},
    {"ssid": "Phone-2.4G", "password": "replace-phone-password"}
  ]
}
```

只支持 2.4GHz，最多 10 组；数组顺序就是优先级。每组最多尝试 20 秒，成功后停止；全部失败会进入恢复配网 AP，不删除列表。空密码代表开放网络；一般密码要求 8–63 个可打印 ASCII 字符（也支持 64 位十六进制 PSK）。

生成一个独立随机推图码：

```bash
openssl rand -hex 32
```

将输出的一行保存为 SD 的 `config/push-token.txt`，同时安全保存这个码供电脑推图使用。不要把示例文字当作真正的码，不要使用 CLIProxyAPI 管理密钥或主机 `FRAME_ACCESS_TOKEN`。安全弹出 SD 并插回相框。

## 3. 刷写

先验证解压包：

```bash
shasum -a 256 -c SHA256SUMS
```

Linux 没有 `shasum` 时可用 `sha256sum -c SHA256SUMS`。必须所有条目均为 OK；失败时重新下载，不要刷写。

完成备份、确认板型后，用包内分段地址刷写：

```bash
python3 -m esptool --chip esp32s3 --port /dev/cu.YOUR_DEVICE --baud 460800 write-flash @flash_args
```

不需要执行 `erase-flash`。不要把 merged 镜像作为上游网页 OTA 文件上传；也不要点击上游在线刷写器的默认 Install，那会刷回上游版本。本包虽提供 `photoframe-merged.bin`，推荐使用上面的分段刷写命令，以避免 merged 填充区覆盖 NVS。

新分区表和 OTA 初始状态仍会替换原设置，备份必不可少。传输不稳定时可降到 `--baud 115200`。完成后按 RESET 正常启动，不要继续按住 BOOT。

## 4. 找到相框 IP 并推图

从路由器设备列表、原设备 `.local` 地址或串口启动日志查相框 IP。首次按列表依次连接可能花几十秒。若全部失败，可连接相框恢复配网 AP，并访问 `http://192.168.4.1`；成功保存后会重启。

在解压目录的终端设置推图码，替换下面的 IP 和占位符：

```bash
export PHOTOFRAME_PUSH_TOKEN='SD 中保存的独立推图码'
bash push-photoframe.sh http://192.168.1.50 preview.png
```

脚本发送 `POST /api/push`，请求头为 `Authorization: Bearer <码>`，body 是 PNG/JPEG 二进制，最多 5MiB。电子纸刷新需要时间，脚本会等待最多 90 秒。返回 200 表示显示处理流程完成；仍要肉眼确认屏幕。

若首次请求超时，不要立即连发多次，先检查画面和设备是否仍在刷新。401：码错误或未提供；503：未配置码、初始化或忙；413：图片太大；415：类型不支持；400：空数据或格式不匹配。

真实额度可把主机已生成的六色 PNG 保存为本地文件，然后用同一脚本推送。这个固件包本身不读取 CLIProxyAPI，也没有自动替换主机现有的拉图/轮播调度；首次先用演示图验收。

配置推图码后，固件启动默认关闭深睡和自动轮播，建议 USB 供电。码导入 NVS 后取走 SD 仍保持鉴权；但多组 Wi-Fi 列表来自 SD，没有 SD 时只能回退到原 NVS 单组信息。更新 SD 中的推图码并重启即可轮换。

## 5. 你需要验证的结果

先做拒绝请求检查（不主动上传有效图片）：

```bash
python3 verify_device.py http://192.168.1.50
```

再明确允许换图，检查成功推图和 SD 当前图片的精确读回：

```bash
python3 verify_device.py http://192.168.1.50 --image preview.png --allow-display
```

此外人工验证：

- 两个网络都可用时连接第一组；关闭第一组后重启，自动连接第二组；SD 列表仍完整。
- 两个都不可用时进入恢复 AP，恢复或新增后能连接，旧记录不丢失。
- 错码/无码不能推图，也不能调用旧 rotate/display/config 写接口，屏幕不变。
- 正确码推图后面板内容正确；断电重启后仍能推图。
- 移除 SD 并重启后，鉴权仍有效（需有可用的 NVS 单组 Wi-Fi）；重新插卡后可正常使用完整列表。

请反馈板型、启动日志、验收脚本输出和屏幕照片，**不要反馈密码或推图码**。这版只有软件测试和编译通过，以上真机结果尚未确认。

## 回退与安全

如果需要回退，先确认仍是原来那台 16MB ESP32-S3，再用备份恢复；这会覆盖当前固件和当前设置：

```bash
python3 -m esptool --chip esp32s3 --port /dev/cu.YOUR_DEVICE write-flash 0x0 /实际备份目录/original-flash.bin
```

恢复原 SD 内容后重启。HTTP Bearer 不加密传输；只在受信局域网使用，不映射公网。SD 中密码和推图码为明文，请妥善保管。
