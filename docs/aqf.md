# aqf：daemon 常驻 BLE 会话与远程 shell

**尚未实机验证。** 离线单元测试、竞态测试、交叉编译和 demo 模式进程冒烟已通过；真实 BLE 长连接、keepalive 与宿主 shell 需要带 `PP_BLE_SHELL` 的宿主固件实测。

`aqf` 与 `ai-quota-frame daemon` 的关系就是 `adb` 与 adb server：BLE 模式的 daemon 持有唯一一条已鉴权 BLE 会话，自动推图、macOS 控制台和 `aqf` 命令都排进同一个队列、复用同一会话；`aqf` 只是本机 socket 客户端，不自己连设备。设备侧不需要额外守护进程，shell 由宿主固件的 `PP_BLE_SHELL` 操作提供。

```sh
aqf start-server          # 启动 daemon（已安装 LaunchAgent 时用 launchctl kickstart）
aqf shell                 # 交互式 shell；exit / Ctrl+D 退出
aqf shell uname           # 单次命令，退出码取设备返回值
aqf devices               # 设备与连接状态（device / offline）
aqf status | aqf info     # 宿主状态 JSON / 已鉴权发现记录
aqf connect | disconnect  # 立即建立 / 释放 BLE 会话
aqf kill-server           # 停止 daemon（含自动推图；launchd/systemd 可能会再拉起）
```

daemon 只服务配置中的一台设备（`PHOTOFRAME_DEVICE_ID` 或最近一次配对绑定）；`-s` 只校验是否是该设备。启动 daemon 会同时开始自动采集与推图，因此设备命令不会隐式启动它，需显式 `aqf start-server`。没有 LaunchAgent 时，`start-server` 用当前环境在后台运行同目录的 `ai-quota-frame daemon`，日志写入 `ai-quota-frame/aqf/daemon.log`。HTTP 传输模式不开放 aqf socket。

## 会话

- 首个设备请求触发扫描、连接和配对 v2 鉴权；之后推图、控制台读写和 aqf 请求复用同一会话，在 radio 线程上串行执行。
- 宿主空闲 60 秒关闭会话，daemon 每 20 秒发送已签名 `INFO` 作为 keepalive。
- 无请求超过 `AQF_SESSION_IDLE`（默认 `10m`，`0` 表示一直保持）主动断开；下一次操作自动重连。
- 传输失败使会话序号失效，daemon 断开并在下一次操作重连。请求不会被自动重放；设备拒绝（例如 busy）不断开会话。
- 推图期间 aqf 请求排队；90 秒内未轮到时返回"设备正忙"。
- 扫描、配对、配对状态、指定设备 info、本机忘记配对、应用特征读写、Wi-Fi 管理、串口诊断和 USB 操作仍自行打开 radio 或会重置设备：执行前关闭常驻会话，期间不重连，结束后按需重连。

## RPC

socket 默认位于配置目录 `ai-quota-frame/aqf/aqf.sock`（目录 0700，socket 0600），从不监听 TCP；`AQF_SOCKET` 可让以其他用户运行的服务与 aqf 使用同一路径。每行一个 JSON 请求 `{"method","device_id","command"}`，回复 `{"error"}` 或 `{"result"}`；一个连接可连续发送多条请求。方法：`version`、`devices`、`connect`、`disconnect`、`info`、`status`、`shell`、`kill`。

## 设备 shell 线协议

`PP_BLE_SHELL = 28`，承载于已签名帧。请求 `u32 offset | command`：offset 为 0 时携带 1..256 字节可打印 UTF-8 命令（拒绝 C0/C1 控制字符，tab 除外），宿主只执行一次并把输出（最多 4095 字节）保存在会话内；offset 大于 0 时不带命令，只读取该次输出的后续页。回复 `u32 total | u32 offset | u8 exit | u8 flags | output`，每页最多 400 字节，`flags` bit0 表示截断；`exit` 0 成功、1 命令错误、2 用法错误、127 未知命令。旧宿主以 invalid request 拒绝未知操作，`aqf` 据此提示更新固件。

首版命令只读：`help`、`uname`/`version`、`uptime`、`free`、`apps`、`ps`/`current`、`status`、`wifi`。不输出令牌、配对密钥、SSID 或 Wi-Fi 密码。
