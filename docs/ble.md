# 原生蓝牙客户端

这一页讲 `internal/ble` 这个 Go 蓝牙客户端是怎么实现的：配对关系存在哪里、图片和应用包怎么发、各平台用什么蓝牙库、出错时怎么处理，以及怎么跑实机测试。读者是要修改客户端代码或排查原生客户端问题的开发者。

`ai-quota-frame ble` 各子命令的用法见[电脑端、控制台与网页插件](https://github.com/M1nt-Ch0c0/esp32s3/blob/main/docs/07-clients.md#ble-子命令)；配对 v2、签名命令和图片事务的协议本身见[蓝牙通信](https://github.com/M1nt-Ch0c0/esp32s3/blob/main/docs/06-bluetooth.md)。字节格式的权威定义在宿主仓库的 [ble-protocol.md](https://github.com/M1nt-Ch0c0/photopainter-host/blob/main/docs/ble-protocol.md) 和 [ble-pairing-v2.md](https://github.com/M1nt-Ch0c0/photopainter-host/blob/main/docs/ble-pairing-v2.md)，以及 photoframe 仓库的 [ble-image-input.md](https://github.com/M1nt-Ch0c0/photoframe/blob/main/docs/ble-image-input.md)。

## 配对关系的存储

配对成功后，客户端保存两类文件，都在操作系统的用户配置目录下的 `ai-quota-frame/` 里：

| 文件 | 内容 | 可改位置的变量 |
|---|---|---|
| `ble-device.json` | 最近一次确认配对的设备 ID，不含密钥 | `PHOTOFRAME_BLE_BINDING_FILE`（绝对路径） |
| `ble-bonds/` | 每台设备一份配对密钥 | `PHOTOFRAME_BLE_BOND_DIR`（绝对路径） |

写入规则：

- 在 Unix 上，文件权限 0600，写入后同步文件和目录。在 Windows 上，用带 write-through 的 `MoveFileEx` 发布文件，依靠用户目录本身的访问控制；自定义目录时要自己保证目录是私有的。
- 每台设备有一个跨进程的文件锁（Unix 用 `flock`，Windows 用 `LockFileEx`），同一时间只有一个进程能改这台设备的配对。
- 配对过程中，客户端**先**把候选密钥写到磁盘，**再**让相框显示六位码。相框同意的回复如果丢了，下一次连接时用候选密钥鉴权就能确认关系，不用重新按键。
- 候选文件只在不存在时创建，另一个配对流程不能覆盖它。确认和清理都核对客户端身份、配对身份和密钥，迟到的确认不会让已经忘记的关系复活，旧流程的清理也不会删掉新候选。
- `PHOTOFRAME_DEVICE_ID` 只用来选择设备，仍然需要这台设备的配对密钥。没有配置时默认用最近一次确认的设备。
- 命令行输出和状态 JSON 里从不包含密钥。`ble paired` 只打印公开的配对摘要。

没有配对关系时，后台直接报“需要配对”，不打开蓝牙，也不消耗图片的重试次数。

## 推图

后台和 `ble push` 都通过 `photoframe` 应用自己的收图服务发图，不使用宿主的旧图片命令（op 15/16/17）。

### 发送前检查

客户端在打开蓝牙前先完整检查 PNG：不超过 5 MiB、800×480、非隔行、完全不透明、只含六种颜色、各块 CRC 正确、没有尾随数据。相框收到后还会用自己的解码器再检查一次。

连接后，客户端读签名状态，核对前台应用是 `photoframe`、收图服务 UUID、运行代次（`generation`）和服务版本（`ble_service_revision`）。前台不是 `photoframe` 时直接报收图服务不可用，**不会**自动切换应用。

### 选择承载方式

| 条件 | 承载方式 |
|---|---|
| macOS（CGO 构建），且状态里 `coc_version=1`、`coc_psm` 非零 | L2CAP CoC |
| 其他情况（Windows、Linux、或相框不支持 CoC） | GATT：签名 op 14 写命令、op 13 读回执，每块 106 字节 |

原生客户端不使用 ABI 2.3 的通用消息通道（op 24–27），那条通道目前只有网页插件在用。

**CoC 路径**（`coc.go`、`coc_darwin.m`）：先在 GATT 上完成鉴权并核对应用，然后在同一个连接上打开 CoC。连接最长等 20 秒，相框的 hello 最长等 15 秒。hello 校验运行代次、会话和 HMAC 后，双方派生两个方向各自的密钥；之后每条记录带长度、序号和 HMAC。客户端用长度前缀处理 CoreBluetooth 字节流的拆分和合并。BEGIN 被接纳后连续发送 CHUNK，不逐块等回执，最后发 COMMIT。CoC 打开后如果失败，这一次尝试不会改走 GATT。

**GATT 路径**：每 106 字节一个 CHUNK，每块都要等回执。签名的“忙”（宿主错误码 3）证明命令没有被接纳，这种情况等 50 毫秒后重试；传输错误和鉴权错误不重试。Windows 上发图时会请求系统的高吞吐连接参数，不支持的适配器保持默认。

### 完成和失败

只有收到签名的完成回执，并且请求 ID、长度、SHA-256 都匹配、状态是 complete/OK，才算推送成功。这时相框已经刷新完屏幕并关闭了显示电源。`LAST_FRAME_PATH` 配置的 PNG 也只在这时保存。上传进度只表示数据送出去了多少，不代表已经显示。

| 情况 | 处理 |
|---|---|
| COMMIT 之前断开 | 下一次尝试用新的请求 ID 重新上传 |
| COMMIT 之后断开，或回复丢失 | 重新连接，只用 QUERY 查询原来那个请求的回执，绝不再发 COMMIT |
| 查询时发现相框重启过、应用代次变了、配对身份变了，或回执找不到、不匹配 | 把这张图标为“结果未知”，本进程不再自动重试它，即使中间来了新快照也不撤销这个决定 |
| 未配对或鉴权失败，且还没 COMMIT | 只结束这一轮，等下一次额度刷新；配对完成后同一张图可以正常发送 |

`photoframe` 在内存里保留最近 4 份回执，重启后清空。客户端这边的图片事务身份也只在进程内存里，后台进程重启后就查不到上一次未确认的结果了，这时只能人工看屏幕核对。所以不要用重启进程的方式“清掉”一个未知结果。

整个推图操作的超时是 90 分钟，照顾很慢的连接。

## 安装和管理应用

- `ble inspect` 和 `ble install` 先在本地检查应用包：不超过 1 MiB，4096 字节包头，格式 1 或 2、ABI 1 或 2，应用 ID 合法，载荷 SHA-256 和包头一致，载荷是 Xtensa ELF。应用包由宿主的 `tools/module.py package` 生成。ELF 的重定位、清单和导入由宿主在安装时检查。
- 上传时每块 400 字节，客户端在标准错误上报告相框已确认的字节数。整个包另有一个 SHA-256 保护传输。会话断了要从头重传整个包。
- BEGIN 收到签名的“忙”时，每 75 毫秒重试一次，最多约 2 秒。DATA 和 COMMIT 不按这个规则重试。出错时会说明失败发生在连接、BEGIN、DATA 还是 COMMIT 阶段，以及实际发出了多少字节。
- `install` 只把包写进备用槽并标记为待激活。要运行它，需要再执行 `activate`。ABI 2 应用在正常 START 并完成首屏后才被确认。
- `install` 和 `push` 的超时是 90 分钟，其他命令 11 分钟。宿主会释放空闲 60 秒的传输。

### 管理命令的回复丢失

`activate`、`switch`、`rollback`、`remove` 改变了相框上的 GATT 数据库，Windows 可能因此让旧的句柄失效，导致回复丢失。客户端在发命令前记下期望的结果（应用、存储区、槽位和版本）。回复不确定且还有时间时，它重新连接一次，读签名状态，看是否已经达到期望；达到就报告成功。命令本身**绝不重发**。超时时会提示先用 `status` 查看再决定。相框明确返回的错误（例如“忙”）直接报告。

## 应用自定义特征（ABI 2.1）

`ble app-read` 和 `ble app-write` 读写当前前台应用注册的自定义 GATT 特征，例如宿主示例应用 `ble-echo`：

```sh
ai-quota-frame ble app-write --app ble-echo --characteristic 0 --text hello
ai-quota-frame ble app-read --app ble-echo --characteristic 0
```

客户端先确认指定的应用正在运行，取得它的运行代次和服务版本，再通过签名的 op 14（写）和 op 13（读）访问，不信任未签名的 GATT 通知。特征序号 0–3，每个值最多 128 字节。读取输出 JSON，包含文本和精确的 Base64。写入在应用处理完事件后才返回，但不代表屏幕已经刷新。切换应用后，旧的代次和服务版本立即失效。

## 安全性

蓝牙链路本身不加密。配对协商出的根密钥从不在空中传输；每条命令和回复用随机数、分方向的 HMAC 和递增序号防篡改、防重放。图片、应用包和状态内容可以被旁听。数据源的管理密钥和 OAuth 凭据从不进入配对过程，也不会发给相框。

v2 宿主拒绝旧的固定令牌鉴权（op 1），旧的 HTTP 管理接口返回 410。原生后台在蓝牙模式下忽略残留的 `PHOTOFRAME_PUSH_TOKEN`，配对关系失效时也不会退回去用它。`PHOTOFRAME_PUSH_TRANSPORT=http` 只用于仍运行 v1 固件的设备。

## 平台适配

| 平台 | 实现 | 构建 |
|---|---|---|
| macOS | `tinygo.org/x/bluetooth` 的 CoreBluetooth 后端，加上自己的 CoC 桥接 `coc_darwin.m` | 需要 CGO 和 Xcode Command Line Tools，并在系统设置里允许蓝牙 |
| Linux | `tinygo.org/x/bluetooth` 的 BlueZ（D-Bus）后端 | `CGO_ENABLED=0` |
| Windows | 扫描用 `tinygo.org/x/bluetooth`，GATT 连接直接用 `winrt-go` | `CGO_ENABLED=0` |

- `tinygo.org/x/bluetooth` 固定在 `go.mod` 里的一个未发布提交上，它包含 Windows 广播 UUID 和扫描回复的修复，以及断开回调重复释放会话的修复。升级这个依赖时要确认这些修复仍在。
- Windows 上，每个操作都在一个锁定的系统线程上初始化和释放 WinRT，显式管理设备、会话、服务和特征的引用。清理时先释放特征、关闭服务，再关闭 `MaintainConnection`、会话和设备。服务发现不走缓存。
- 有些系统调用不能被库取消。调用方的超时照常返回，但这个阻塞的调用会一直占着一个适配器名额，直到它真正结束；在此之前不会再发起新的原生操作，避免阻塞的扫描或连接无限堆积。
- macOS 上用 `CGO_ENABLED=0` 构建会得到没有蓝牙的版本，只保留旧 HTTP 模式，蓝牙命令会报不可用。
- 在 macOS 上，蓝牙权限按应用授予。用 `deploy/install-macos.py` 安装成 `PhotoPainter Renderer.app` 后，用 `deploy/pp.py` 执行 `ble` 命令（它自动在参数前加 `ble`）。`pp.py` 通过 `open -W` 启动应用，并把输出转回终端；它的退出码只表示启动和等待是否成功，设备操作的结果要看命令输出。在等待中按 Ctrl+C 只停止等待，已经发出的设备操作可能还在执行，要用 `pp status` 核对。

## 测试

`make check` 覆盖的内容包括：与 C、OpenSSL、WebCrypto 共用的配对测试向量，候选密钥持久化和丢失确认后的恢复，被撤销的配对身份，伪造和篡改的回复，重放，应用包分帧和校验，传输中断，取消，显式激活，状态分页，图片事务的选服务、分块、丢回复、提交前取消、迟到的完成回执、失败和未知回执，以及快照替换后的身份保留。`make check` 不会打开蓝牙，也不会刷新屏幕。

### 实机测试

实机测试单独编译，需要加构建标签 `photopainter_hardware`：

```sh
go test -tags photopainter_hardware -c -o dist/photopainter-ble-hardware ./internal/ble
# Windows：
GOOS=windows GOARCH=amd64 CGO_ENABLED=0 go test -tags photopainter_hardware -c -o dist/photopainter-ble-hardware.exe ./internal/ble
```

运行时用 `PHOTOFRAME_HARDWARE_TEST` 选择一项，用 `PHOTOFRAME_DEVICE_ID` 指定设备，并用 `-test.run` 选中对应的测试函数。没设置对应模式的测试会跳过。

| 模式 | 测试函数 | 做什么 | 刷新屏幕 |
|---|---|---|---|
| `app-image-safety` | `TestHardwareAppImageSafety` | 要求相框没有连 Wi-Fi；检查应用的 5 MiB PSRAM 预留、断开后释放、坏 PNG 被拒、取消、60 秒空闲超时 | 否 |
| `app-image-display` | `TestHardwareAppImageDisplay` | 用 `PHOTOFRAME_HARDWARE_PNG` 提交一张图，故意丢掉 COMMIT 的回复，重连查询原回执，确认相框没有重启 | 一次 |
| `app-image-lifecycle` | `TestHardwareAppImageLifecycle` | 需要已确认的 ABI 2 应用 `color-test`。收图收到一半时切走，确认收图服务消失、旧代次的读写被拒，切回后恢复 | 否 |
| `image-offline` | `TestHardwareOfflineBLEImage` | 宿主自带图片命令：要求相框没有 IP，检查 PSRAM 预留和断开清理、坏 PNG 被拒，提交一次并丢掉回复后查询回执 | 一次 |
| `discovery-only` | `TestHardwareDiscoveryDeadline` | 两次鉴权发现，打印耗时 | 否 |
| `echo-probe`、`echo-absent` | `TestHardwareApplicationGATT` | 检查 `ble-echo` 的自定义 GATT 服务在激活后存在、切走后消失 | 否 |
| `idle-expiry` | `TestHardwareIdleExpiry`（仅 Windows） | 占着接收缓冲不提交，确认 60 秒后相框断开，并能重新鉴权、内存已释放 | 否 |
| `stage-only` | `TestHardwareBLEUpload` | 仅用于 v1 固件：用固定令牌和 `PHOTOFRAME_HARDWARE_PACKAGE` 上传 `ble-echo` 再删除 | 否 |
| `display-once`、`http-rejections` | `TestHardwareHTTPDisplay` | 仅用于 v1 固件的 HTTP 接口 | `display-once` 一次 |

运行实机测试前：

1. 停掉自动推图（暂停或停止 `daemon`、关掉网页的定时推送），避免两个客户端同时连相框。
2. 会刷新屏幕的测试只跑一次。结果不确定时先读状态和回执，不要直接重跑。
3. 刷新后人工确认屏幕上的画面、方向和颜色。
