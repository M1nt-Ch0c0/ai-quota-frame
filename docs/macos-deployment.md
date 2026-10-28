# macOS BLE 服务部署记录

## 当前源码与夜间交付（2026-10-29）：尚未实机验证

新版原生客户端默认使用配对 v2，不再要求手填设备推图令牌。先在相框上
核对六位码并短按 GPIO4 功能键同意；关系持久保存后，后续连接自动鉴权。
长按功能键至少 8 秒释放可提出重置，即使正在刷新也会封住新工作，等待
当前物理刷新完成后再清除关系。BOOT 和电源键不参与。

原生与浏览器是独立 owner，不能把“暂停后恢复旧客户端”理解为它仍有授权。
转移前暂停旧端并物理重置，在新端完成配对。新原生凭据保存在用户配置目录
的 `ai-quota-frame/ble-bonds/`；文件为 0600，每设备跨进程互斥，候选不覆盖，
确认和取消只处理匹配关系。`pp paired` 仅显示公开摘要。

按最新夜间指令，本轮只检查代码、文档和本地 fixture，**不刷写、不连接设备
蓝牙、不推图、不修改运行服务或线上服务器**。固件实际更新、物理按键、
重启持久化、换绑、显示回执和目视验证留待用户在场。自动推图此前已暂停，
本轮不改变该设置；早上以主验收清单中的本地只读快照为准。

首次安装使用原应用身份，先保留暂停：

```sh
python3 deploy/install-macos.py --env-file /private/path/runtime.env --no-start
alias pp='python3 /absolute/path/ai-quota-frame/deploy/pp.py'
# 以下设备步骤仅在早上验收时执行。
pp scan
pp pair --device-id 001122334455 --name "PhotoPainter Mac"
pp paired
pp status
```

设备 ID 替换为实际扫描结果。CLI 启动不代表配对完成，必须核对已保存的关系
与设备签名状态。常驻服务只有在本客户端仍是设备 owner、没有未确认事务、
实体刷新和目视检查通过后，才明确恢复自动推图。当前验证边界也见
[BLE 指南](ble.md) 与项目 Wiki 状态页。

## 数据源鉴权修复（2026-10-29）

当前 Manager 已停用旧 Google 网关，Mac 采集端已通过受信工具迁移至 `SOURCE_AUTH_MODE=management-key`，并将管理域名加入 `NO_PROXY`，保留其余代理设置。旧 `127.0.0.1:7890` 代理在检查时不可达，Google Token 换取失败；静态管理密钥候选已验证额度和用量采集成功。配置与备份均为私有 `0600`，没有打印或提交凭据。模式、迁移工具和验证边界见 [数据源诊断](source-auth-diagnostics.md)。此结果仅证明采集修复，后台重新加载和物理刷新另行验证。

## 历史部署基线（2026-10-27）

Go后台继续使用既有PhotoPainter Renderer.app身份、私有配置和LaunchAgent；SwiftUI控制台源码迁移至独立 [photopainter-console](https://github.com/M1nt-Ch0c0/photopainter-console)。界面通过原Unix socket操作后台，关闭窗口不会停止采集或推图。

设备宿主ABI 2.2和photoframe v20已部署，Mac后台已通过CoC实际发送67,509字节，发送阶段2.477秒，01:49:21（UTC+8）收到最终显示完成回执。控制台实际通道/速率来自后台遥测，不凭能力声明推断。版本整理和新控制台能力以项目Wiki状态页为准。

下面保留的迁移过程和停服状态均为对应日期的历史记录，不是当前服务状态或新的操作授权。


**以下为 2026-10-23 历史路线：**用户当时要求改由 photoframe ABI 2.1 APP 接收 PNG，宿主保留通用通信、
鉴权与 OTA。本页记录已安装宿主通道的有效验证，不代表新路线已完成。暂不再扩展或刷写
专用 PNG 宿主实现。兄弟仓库 `esp32s3/MAC_BLE_APP_HANDOFF.md` 仅是历史索引，
不是当前任务或授权来源；上述旧路线不覆盖当前配对 v2。

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
[离线 BLE 图片验收记录](https://github.com/M1nt-Ch0c0/photopainter-host/blob/main/docs/validation-ble-images.md)。
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

## 历史运行配置和维护（2026-10-23）

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
该代理当时是此机器取数的依赖；当前模式已在页首修正，其他机器不应照抄端口。
BLE 设备通信不经过代理。

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
