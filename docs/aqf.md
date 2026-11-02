# aqf：常驻蓝牙会话和调试命令

`aqf` 是一个调试用的命令行客户端，用法类似 Android 的 `adb`。蓝牙模式下运行的 `ai-quota-frame daemon` 就是它的服务端：`daemon` 保持一条已鉴权的蓝牙会话，自动推图、macOS 控制台和 `aqf` 命令都排进同一个队列，共用这条会话，不用每次重新连接和鉴权。

这一页写给需要直接查看相框状态、或在相框上执行只读 shell 命令的开发者。`aqf shell` 需要宿主固件支持 op 28（`PP_BLE_SHELL`）。

## 命令

`make build` 会同时生成 `dist/aqf` 和 `dist/ai-quota-frame`，两个程序放在同一目录。

```text
aqf [-s DEVICE_ID] <命令> [参数]
```

| 命令 | 作用 |
|---|---|
| `start-server` | 启动 `daemon`。已安装 macOS LaunchAgent 时用 `launchctl kickstart`；否则在后台运行同目录的 `ai-quota-frame daemon`，继承当前环境变量 |
| `kill-server` | 停止 `daemon`，自动推图也随之停止。由 launchd 或 systemd 管理的服务可能会被重新拉起 |
| `devices` | 列出 `daemon` 服务的设备和连接状态：`device` 表示已连接，`offline` 表示未连接 |
| `connect` / `disconnect` | 立即建立或释放蓝牙会话 |
| `info` | 已鉴权读取的设备发现记录 |
| `status` | 宿主的完整状态 JSON |
| `shell [命令]` | 带命令时执行一次，退出码就是设备返回的退出码；不带命令时进入交互模式，`exit` 或 Ctrl+D 退出 |
| `version` | 显示 `aqf` 和 `daemon` 的版本、进程号和设备 ID |

启动 `daemon` 就会开始自动采集和推图，所以设备命令在 `daemon` 没运行时直接报错，不会替你启动它，需要先显式执行 `aqf start-server`。没有 LaunchAgent 时，`daemon` 的输出写到 socket 所在目录的 `daemon.log`。

`daemon` 只服务一台设备：`PHOTOFRAME_DEVICE_ID` 指定的设备，没有指定时用最近一次配对的设备。`-s` 只用来核对目标是不是这台设备，不能切换设备。要换设备，修改 `PHOTOFRAME_DEVICE_ID` 后重启 `daemon`。

只有 `PHOTOFRAME_PUSH_TRANSPORT=ble` 时 `daemon` 才会打开 `aqf` socket。

## 会话怎么维持

- 第一个设备请求触发扫描、连接和配对 v2 鉴权。之后的推图、控制台操作和 `aqf` 请求都在同一个会话上依次执行。
- 宿主会关闭空闲 60 秒的会话，所以 `daemon` 每 20 秒发一次签名的 `INFO` 请求保活。
- 超过 `AQF_SESSION_IDLE` 没有请求时，`daemon` 主动断开，下一次操作再自动重连。默认 `10m`；设为 `0` 表示一直保持。
- 传输出错时，`daemon` 断开会话，下一次操作重新连接。出错的请求不会自动重发。设备明确拒绝（例如“忙”）时会话保持不变。
- 每个 `aqf` 请求最多等 90 秒。推图期间 `aqf` 请求排队等待，超时后返回“设备正忙”。
- 扫描、配对、读配对状态、读设备信息、忘记配对、读写应用特征、Wi-Fi 管理、串口诊断和 USB 维护这些操作会自己占用蓝牙或重置设备。执行它们之前，`daemon` 先关闭常驻会话，执行期间不重连，结束后按需再连。

## 本机 socket 协议

| 项目 | 值 |
|---|---|
| 路径 | 配置目录下的 `ai-quota-frame/aqf/aqf.sock`（macOS 是 `~/Library/Application Support/ai-quota-frame/aqf/aqf.sock`）；`AQF_SOCKET` 可以指定其他路径，例如服务以另一个用户运行时 |
| 权限 | 目录 0700，socket 0600；只用 Unix socket，不监听 TCP |
| 请求 | 每行一个 JSON：`{"method": "...", "device_id": "...", "command": "..."}`，`device_id` 和 `command` 可省略 |
| 回复 | 每行一个 JSON：成功时 `{"result": ...}`，失败时 `{"error": "..."}` |
| 方法 | `version`、`devices`、`connect`、`disconnect`、`info`、`status`、`shell`、`kill` |

一个连接可以连续发送多条请求，交互式 shell 就是这样用的。

## 设备 shell

shell 走宿主的签名命令 op 28（`PP_BLE_SHELL`）。字节格式的权威定义在宿主的 [BLE 协议](https://github.com/M1nt-Ch0c0/photopainter-host/blob/main/docs/ble-protocol.md)里，这里列出客户端用到的部分：

| 方向 | 格式 |
|---|---|
| 请求 | `offset:u32`，`offset` 为 0 时后面跟 1–256 字节的命令（UTF-8，不能有控制字符，制表符除外） |
| 回复 | `total:u32 | offset:u32 | exit:u8 | flags:u8 | 输出`，每页最多 400 字节 |

- `offset` 为 0 的请求让宿主执行一次命令，把输出（最多 4095 字节）保存在会话里。之后用 `offset > 0` 的请求只读取后续页，不会再执行一次。
- `flags` 第 0 位为 1 表示输出被截断。
- `exit`：0 成功，1 命令出错，2 用法错误，127 未知命令。
- 不支持 op 28 的旧宿主会以“请求无效”拒绝，`aqf` 据此提示更新固件。

可用的命令都是只读的：

| 命令 | 输出 |
|---|---|
| `help` | 命令列表 |
| `uname`、`version` | 宿主固件、ABI、ESP-IDF 版本和芯片 |
| `uptime` | 开机时长和复位原因 |
| `free` | 内部 RAM 和 PSRAM 的使用情况 |
| `apps` | 已安装的应用和槽位 |
| `ps`、`current` | 前台应用的状态 |
| `status` | 和 `aqf status` 相同的状态 JSON |
| `wifi` | Wi-Fi 连接状态和 IPv4 地址 |

shell 不输出配对密钥、Wi-Fi 名称和密码。
