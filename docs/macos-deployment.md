# macOS BLE 服务部署记录

**后续路线已调整：**用户要求改由 photoframe ABI 2.1 APP 接收 PNG，宿主保留通用通信、
鉴权与 OTA。本页记录已安装宿主通道的有效验证，不代表新路线已完成。暂不再扩展或刷写
专用 PNG 宿主实现；接力任务见兄弟仓库 `esp32s3/MAC_BLE_APP_HANDOFF.md`。

2026-10-23，在 Apple Silicon Mac、macOS 26.3.1 上将设备通道切换为纯 BLE。
相框位于 Mac 旁，独立 USB 供电，没有电池或 microSD。电脑仍需联网取额度，
相框不需要连接 Wi-Fi。2026-10-22 的“设备不在现场、蓝牙权限待授予”状态
已经结束；现场查询通过了应用身份的蓝牙权限和设备鉴权。

## 版本和构建

- 分支：四个仓库均使用 `codex/ble-image-push`。
- 宿主代码 `b4709c0`，固定 ESP-IDF
  `5e6f53cdb31fe5708eae3f55af9737be2822db22`、Registry elf_loader 1.3.3。
- 电脑端推图实现 `0e94fab`，Go 1.26.0 原生 CGO 构建；程序 SHA-256：
  `8c9251a94b7564465ccf897c78faafd9ad4ae3f825d9d50b1cf5f439645f1bc9`。
- Mac `make check` 的单元、race、vet、Chrome 渲染测试和原生构建通过；
  Linux 同一套检查通过，Windows amd64 交叉构建通过。
- 宿主 86 项测试通过，包含真实 ELF 校验及实际 C 协议的 ASan/UBSan 检查。
- 相框源代码和 SDK 为 `d2dd482`，本次无需修改应用二进制。设备保留原有
  ABI 1 `photoframe v15`，其备用槽 v16 和原 `color-test` 均未被替换。

## 宿主更新

用户授权串口更新并连接设备后，核对 ESP32-S3 身份，完整备份 16 MiB Flash，
按 256 KiB 分块校验设备摘要，再保存完整备份的 SHA-256。首次整体读取中断，
当时没有写入；分块备份成功后才继续。

只写 `0x10000` 的宿主二进制，大小 1106048 字节，SHA-256：
`17a546d0022f8a6b3fcab48bd22b3c38cd95e954b8dbf83f2581d62d6786ef63`。
写入校验通过，factory 以外区域的设备摘要前后一致，启动输出确认 `b4709c0`。
没有修改分区表、provision 或整机 OTA。备份、旧宿主回退产物和摘要保存在
Mac 仓库外的私有目录；不提交或打印原始备份。

随后用户断开 Mac 数据线，恢复独立供电。首次 BLE 查询得到 IP `0.0.0.0`、
原相框 v15，内部空闲 RAM 163875 字节、PSRAM 8284376 字节。新 bin 比此前
BLE 宿主增加 1184 字节，静态 DIRAM 增加 384 字节。

## 离线实机检查

- BLE 安装 5980 字节 `ble-echo v1` 包并显式激活，状态确认为 v1。
- BLE 上传 v2 至备用槽并激活，状态确认为 v2、previous 为 v1。
- BLE 回滚到 v1，状态确认完成；设备启动时间持续增加、复位原因没有变化。
- ABI 2.1 鉴权写入并读回 `mac-offline-ble`，内容一致。
- 切回原 `photoframe v15` 后，自定义 GATT 特征数回到 0；图像确认原相框后
  已删除测试目录项，原有两个应用和它们的版本保留。
- `TestHardwareOfflineBLEImage` 在真实 CoreBluetooth 上通过，用时 55.61 秒。
  每个连接均要求 IP 为 `0.0.0.0`；5 MiB 接收缓冲分配在 PSRAM，发送部分数据
  后断开，内存恢复；坏 PNG 返回错误 12，没有改变应用就绪状态。
- 12915 字节真实额度图在 8.9 秒内完成上传；测试故意丢弃物理完成后的回复，
  重连查询原凭据，在图像操作开始后 41.4 秒确认成功。计数严格为一次 COMMIT、
  一次凭据查询，没有重复刷新，设备没有重启。

完整命令语义、内存采样和范围见宿主的
[离线 BLE 图片验收记录](https://github.com/M1nt-Ch0c0/photopainter-host/blob/codex/ble-image-push/docs/validation-ble-images.md)。
测试预约了最大接收内存，但没有声称完整传输了 5 MiB 图片或完成长期耐久测试。

## 显示配置迁移纠正

第一张测试图用了 Mac 未设置主题时的默认 classic 页面，用户指出样式变旧。
原因是迁移遗漏显示配置，不是相框 ELF 降级。已对齐原 Windows 的配置：

```dotenv
FRAME_THEME=mint-skin
DISPLAY_PROVIDERS=claude:CLAUDE,codex:CODEX,xai:GROK
REFRESH_INTERVAL=15m
TZ=Asia/Shanghai
PHOTOFRAME_PUSH_TRANSPORT=ble
```

重新生成的 mint-skin 图为 59124 字节，包含 5 个采集账号、3 行展示，
`stale=false`、含用量统计。采集端有 1 条警告，不代表 BLE 传输错误。
迁移到新电脑时必须同时核对主题、展示行、时区和刷新间隔，不能仅验证取数成功。

这张 59124 字节图片已通过 BLE 返回物理完成凭据，用户确认“已恢复，显示正常”。
随后用户调整实现路线，Mac 常驻服务继续保持禁用、未加载、进程数 0，尚未恢复自动推图。

## 运行配置和维护

应用位于 `~/Applications/PhotoPainter Renderer.app`，使用 CoreBluetooth，
CLI 和 LaunchAgent 使用同一应用身份。安装器声明蓝牙和本地网络用途；没有
修改 TCC 数据库或绕过系统隐私权限。Mac 本机需要允许此应用使用蓝牙。

运行目录为 `~/Library/Application Support/ai-quota-frame`。可信启动器私下
加载 `.env`，设备模式为 `PHOTOFRAME_PUSH_TRANSPORT=ble`，配置设备 ID
和已有独立令牌，不需要推图 IP。发现代码不改写用户配置或把 IP 写回 `.env`。
图片内容没有 BLE 链路加密，HMAC 提供鉴权、完整性和防重放，令牌不发送。

数据源使用已有 HTTPS 网关和专用 Google 服务账号 ID Token，不再运行旧源站
SSH 隧道。凭据仅通过加密 SSH 传入仓库外目录；配置和服务账号文件权限 0600，
没有复制 SSH 私钥、NVS 或源码中的秘密文件。原配置及程序的回退副本也保存在
私有运行目录，不要打印或提交。

此 Mac 直连 Google 鉴权接口不可用，使用已有的 `127.0.0.1:7890` 代理；
服务配置了 `HTTP_PROXY`、`HTTPS_PROXY` 和本地地址的 `NO_PROXY`。
该代理是此机器取数的依赖，其他机器不应照抄端口。BLE 设备通信不经过代理。

Windows 计划任务、看门狗和推图进程已停用，避免两个主机同时连接相框。
Mac 使用 `com.m1ntch0c0.ai-quota-frame` LaunchAgent，登录启动、退出后重启，
只检查进程存活，不新增监听端口。硬件检查期间保持卸载和禁用，完成物理及
目测验收后再启动。

```sh
open -n -W "$HOME/Applications/PhotoPainter Renderer.app" --args ble status
launchctl print gui/$(id -u)/com.m1ntch0c0.ai-quota-frame
# 独立操作或硬件检查前先停止常驻推图
launchctl bootout gui/$(id -u)/com.m1ntch0c0.ai-quota-frame
```

`last-frame.png` 只在服务收到 HTTP 200 或经过鉴权的 BLE 物理完成凭据后保存。
`validation/ble-image-*.png` 是测试输入，不能仅凭文件存在宣称刷新成功。
完成回复丢失时，原发送器只查询原请求凭据；重启、缺失或不匹配的凭据会停止
该图片的自动重试。凭据只存在 RAM，不保证跨电脑进程重启的永久去重。
查看服务状态优先使用 LaunchAgent、进程和完成文件元数据，不要打开可能含
敏感源端内容的运行日志。
