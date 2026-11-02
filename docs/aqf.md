# aqf：常驻 BLE 会话与远程 shell

**尚未实机验证。** 离线单元测试、竞态测试和交叉编译已通过；真实 BLE 长连接、keepalive 与宿主 shell 需要带 `PP_BLE_SHELL` 的宿主固件实测。

`aqf` 的关系类似 `adb` 与 adb server：后台 `aqf server` 持有唯一一条已鉴权 BLE 会话，命令行通过本机 Unix socket 调用，不再为每条命令重新扫描、连接和鉴权。设备侧不需要额外守护进程，shell 由宿主固件的 `PP_BLE_SHELL` 操作提供。

```sh
aqf shell                 # 交互式 shell；exit / Ctrl+D 退出
aqf shell uname           # 单次命令，退出码取设备返回值
aqf devices               # 设备与连接状态（device / offline）
aqf status | aqf info     # 宿主状态 JSON / 已鉴权发现记录
aqf connect | disconnect  # 立即建立 / 释放 BLE 会话
aqf start-server | kill-server
aqf server --idle 10m --keepalive 20s   # 前台运行，便于看日志
```

设备选择顺序：`-s DEVICE_ID`、`PHOTOFRAME_DEVICE_ID`、最近一次配对绑定。server 只绑定一台设备；`-s` 与之不同会被拒绝，需先 `aqf kill-server`。设备命令在 server 未运行时自动以脱离终端的方式启动它，日志写入配置目录下 `ai-quota-frame/aqf/server.log`。

## 会话

- 首个请求触发扫描、连接和配对 v2 鉴权；之后的请求复用同一会话并在 radio 线程上串行执行。
- 宿主空闲 60 秒关闭会话，server 默认每 20 秒发送已签名 `INFO` 作为 keepalive。
- 无请求超过 `--idle`（默认 10 分钟，`0` 表示一直保持）主动断开，避免长期占用设备和耗电；下一条命令自动重连。
- 传输失败使会话序号失效，server 断开并在下一条命令重连。请求不会被自动重放；设备拒绝（例如 busy）不断开会话。
- 宿主单次会话最长 2 小时，到期后同样在下一条命令透明重连。

## 与自动推图后台的关系

`aqf server` 与 `ai-quota-frame daemon` 是不同进程。server 持有连接期间，设备通常停止广播，后台推图会扫描失败并按原有策略稍后重试。需要稳定推图时运行 `aqf kill-server` 或 `aqf disconnect`，或依赖空闲断开。把 aqf RPC 合并进 daemon 的唯一设备队列是后续工作。

## RPC

socket 位于 `ai-quota-frame/aqf/aqf.sock`（目录 0700，socket 0600），从不监听 TCP。每行一个 JSON 请求 `{"method","device_id","command"}`，回复 `{"error"}` 或 `{"result"}`；一个连接可连续发送多条请求。方法：`version`、`devices`、`connect`、`disconnect`、`info`、`status`、`shell`、`kill`。

## 设备 shell 线协议

`PP_BLE_SHELL = 28`，承载于已签名帧。请求 `u32 offset | command`：offset 为 0 时携带 1..256 字节可打印 UTF-8 命令，宿主只执行一次并把输出（最多 4095 字节）保存在会话内；offset 大于 0 时不带命令，只读取该次输出的后续页。回复 `u32 total | u32 offset | u8 exit | u8 flags | output`，每页最多 400 字节，`flags` bit0 表示截断；`exit` 0 成功、1 命令错误、2 用法错误、127 未知命令。旧宿主以 invalid request 拒绝未知操作，`aqf` 据此提示更新固件。

首版命令只读：`help`、`uname`/`version`、`uptime`、`free`、`apps`、`ps`/`current`、`status`、`wifi`。不输出令牌、配对密钥或 Wi-Fi 密码。
