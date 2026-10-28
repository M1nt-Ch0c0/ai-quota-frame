# 客户端配对 v2 离线验证记录

日期：2026-10-29（UTC+8）。**尚未实机验证。** 本次仅在本机运行单元测试、
隔离浏览器 fixture 和构建；未连接设备蓝牙、打开串口、刷写、推图或访问线上
CPA/CPM 服务，也未读取生产凭据、修改已安装服务或重启容器。

验证时客户端 checkout 基线为
`513fc5e54b8c119b297c35e9e68a6580f7d2da9f`，叠加本次配对 v2、数据源鉴权、
共享 Go/WASM 引擎及相关测试改动。下列记录来自提交前的离线验证；
最终提交、提交后重建产物和交付包的对应关系以[早上验收清单](https://github.com/M1nt-Ch0c0/esp32s3/blob/main/docs/MORNING-ACCEPTANCE.md)及其 manifest 为准，不能将旧基线
的二进制当作本次交付。

## 本轮修复与回归

- 每台设备的原生配对关系使用进程间文件锁。候选关系采用原子
  create-if-absent 发布，已有候选不能被另一个配对流程覆盖。
- 确认及清理同时核对客户端身份、绑定身份和协商密钥；迟到的确认不能
  复活已忘记的关系，旧流程清理不能删除新候选。Unix 使用 `flock`；
  Windows 使用 `LockFileEx` 和带 write-through 的 `MoveFileEx`。
- 未配对或 COMMIT 前的鉴权前置条件失败，只结束本轮推送，等待下一次成功
  额度刷新；不启动定时盲重试，也不永久拒绝该图片。完成配对后，同一张图
  可以正常发送。COMMIT 后未知结果仍保留原请求身份并停止重刷。
- 保持配对丢失确认后的候选恢复、存储失败/承诺篡改不武装设备、签名服务
  和完成回执校验、最新快照队列及去重等原有回归覆盖。

新增的配对文件锁回归使用实际子进程争用；队列回归覆盖外层 `Pusher`，
不仅验证底层 sender 的错误分类。测试密钥、设备 ID、额度及 HTTP 响应均为
测试数据。

## 已执行检查

环境为 macOS arm64、`go1.27.2 darwin/arm64`，Chrome 路径为
`/Applications/Google Chrome.app/Contents/MacOS/Google Chrome`。

```sh
CHROME_BIN='/Applications/Google Chrome.app/Contents/MacOS/Google Chrome' \
  make check BINARY=/tmp/night-ai-quota-frame-verified

GOOS=windows GOARCH=amd64 CGO_ENABLED=0 \
  go test ./internal/ble -run '^$' -exec=true

GOOS=windows GOARCH=amd64 CGO_ENABLED=0 \
  go build -buildvcs=false -trimpath -ldflags='-s -w' \
  -o /tmp/night-ai-quota-frame-windows.exe ./cmd/ai-quota-frame

python3 -m unittest discover -s deploy -p 'test_migrate_source_auth.py'

git diff --check
```

全部通过。`make check` 包含 `go test ./...`、`go test -race ./...`、
`go vet ./...` 及启用 CGO 的 macOS 原生构建；Chrome 渲染对照实际执行，
没有因找不到浏览器而跳过。构建只出现既有的重复 `-lobjc` 链接警告。
配置迁移工具的 3 项本地测试通过。

Windows 命令仅证明该目标的测试包和程序能够交叉编译；`-exec=true` 没有
执行 Windows 测试，不能用来证明 Windows BLE、文件 ACL 或系统权限行为。
原生 Windows 部署仍应使用当前用户的私有配置目录。

共享浏览器采集、渲染及字体边缘差异的具体验证边界见
[共享渲染验证](web-frame-parity.md)；浏览器配对、UI 和插件安装流程见
[浏览器说明](../web/README.md)与[插件说明](../plugins/photopainter/README.md)。

## 本轮构建产物

| 目标 | 构建输出 | 大小（字节） | SHA-256 |
| --- | --- | ---: | --- |
| macOS arm64 / CGO | `/tmp/night-ai-quota-frame-verified` | 12043842 | `9e4860a85aee1c231d2fad7b50fd393a531de2a290fa4a80d2bf22d5cb57e36c` |
| Windows amd64 | `/tmp/night-ai-quota-frame-windows.exe` | 12815360 | `6d98aef5cd05542b81f8d60e4daec3a7de39eb4c0f06f6331c681e9c99d85727` |

这两份是离线构建输出，尚未安装到本机常驻服务或线上服务器。最终发布包
应携带对应校验值；若之后修改源码或重新构建，应重新核验，不沿用旧摘要。

## 留待在场验收

设备首次配对、双方六位码一致、GPIO4 短按批准、至少 8 秒长按解绑、
刷新中锁存解绑、双方重启保持关系、浏览器与原生 owner 交接、实际图像完成
回执以及屏幕方向/颜色均未在本轮验证。只有匹配的认证完成回执和人的目视
确认才能完成刷屏验收。

原生 sender 的未知图片事务身份仍为进程内状态；不应重启进程来丢弃一次
未知结果。浏览器持久事务遵守先保存后 COMMIT，未知结果只查原请求。

本轮没有切换自动推图状态，也没有重新查询运行中的后台；实际状态应以
跨仓早上验收清单记录的最近一次已确认结果为准，执行测试前再次确认暂停。
