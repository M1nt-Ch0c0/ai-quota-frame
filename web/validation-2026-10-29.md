# 2026-10-29 夜间离线验证

**0.2.2 为未部署候选，尚未实机验证。** 本轮不连接蓝牙/串口/USB，不推图，
不访问线上 CPA/CPM，不启动或重启线上容器。前序 0.2.1 的上线记录不作为
0.2.2 或设备物理验证的替代。

## 本轮修正

- 缺少 Web Locks 时在 GATT 连接前失败关闭，不再静默使用无锁连接。
- 平台选择与原 Go `ParseDisplayProviders` 一致，限制为 1–5 行，界面和
  submit 入口同时检查；取消设置后重新打开也能恢复正确有效状态。
- 检查 Go/Web 固定配对向量镜像逐字节一致。
- 浏览器检查明确寻找 Chrome/Chromium，缺少时失败，不以跳过冒充通过。
- CI 显式准备 Go/Node/Chrome；先生成 WASM/HTML，再测试原生插件；Linux
  产物使用固定 Debian 12 builder，避免带入较新 GLIBC 依赖。
- 生成 WASM 与原生库使用 `-buildvcs=false`，源码提交身份在交付清单记录，
  避免同一源码因工作区 dirty 位变化而得到不同资源。

## 已执行检查

| 命令 | 结果 |
|---|---|
| `cd web && npm run check` | 32 项测试、TypeScript 与构建通过 |
| `node web/tools/check-frame.mjs` | 原生/WASM 采集、指纹和五主题×两场景对照通过 |
| `PHOTOFRAME_UI_CAPTURE=0 node web/tools/check-ui.mjs` | 71/71 断言通过，无 findings 或未捕获异常 |
| `cd plugins/photopainter && go test ./...` | 原生资源和 subject 投影测试通过 |
| 本机 `go build -buildmode=c-shared` + `verify-native.py` | 真实 C ABI 与三项内嵌资源逐字节比对通过 |
| Zig Linux/amd64 交叉 Cgo 构建 | ELF64 little-endian x86-64 DYN，所需导出符号齐全，GLIBC 最大需求 2.34 |
| `git diff --check` | 通过 |

浏览器使用新建的隔离 profile 和 loopback fixtures，未接触真实账号或设备。
配对、发送在 UI 检查中明确模拟。四个主题每张 384,000 像素全同；mint-skin
每个固定场景均有 3 个字形灰阶跨量化阈值的像素差，原始灰度差不超过 2/255。
原 HTML、数据、皮肤、图标和量化实现相同；未用手工像素补丁伪造一致。

## GitHub CI 环境修正

GitHub Actions [40771817743](https://github.com/M1nt-Ch0c0/ai-quota-frame/actions/runs/40771817743)
在提交 `32190b7` 上的首次运行未进入浏览器一致性断言：下载的 Chromium
`chrome-sandbox` 没有 root 所有权及 `4755` 权限，两个浏览器测试均在启动时失败。
工作流现仅配置该辅助程序的所有权/权限，并在独立临时 profile 中先执行 30 秒
限时启动检查；未加 `--no-sandbox`、跳过测试或放宽采集/像素一致性标准。

修正后本机再次执行 `node web/tools/check-frame.mjs` 通过，结果与上表一致；
`PHOTOFRAME_UI_CAPTURE=0 node web/tools/check-ui.mjs` 再次通过全部 71 项断言。
工作流 YAML 与新增 shell 段语法检查通过。Linux 沙箱修复的最终证据需以
后续 GitHub Actions 成功记录为准；本机 macOS 检查不能代替该项。

## 本地候选产物

工具链：Go 1.27.2、Node 26.12.1、Zig 0.17.0，交叉目标
`x86_64-linux-gnu.2.34`。产物及清单在忽略的 `plugins/photopainter/dist/`。

| 文件 | 字节数 | SHA-256 |
|---|---:|---|
| `photopainter-v0.2.2.so`（Linux amd64，最终本地重建） | 13,852,592 | `71fb89ae5572f7287d6098588efa54417e86622f214c774023418dbf47ebecbb` |
| `panel.html` | 82,445 | `3925ac9762b43b9744344684ea8559e5bec83356d7169d94f95aed096355678a` |
| `frame.wasm.gz` | 4,378,690 | `c584aab838f27be7fc2b0aa0c6763d481fc2d8a19e93c720a602f5f281a68ec3` |
| `wasm_exec.js` | 16,992 | `0c949f4996f9a89698e4b5c586de32249c3b69b7baadb64d220073cc04acba14` |

上述 Linux 库检查了精确内嵌资源字节、ELF 架构、动态导出和 GLIBC 需求，
但没有在 Linux CPA 中实际加载；Mac 的 C ABI 执行通过不能代替该项。
CI 的固定 Linux builder 会生成自己的候选及验证 JSON，哈希按其工具链记录。

## 早上仍须完成

- 在受控环境/维护窗口核对候选库与 CPA 的实际加载、菜单、三个资源摘要和
  subject 接口鉴权；保留配置 inode、旧库和回滚资料，不盲目重启整个栈。
- 设备备份条件确认后刷写最终 host，真实按功能键配对，并测试页面刷新和
  设备重启后的持久重连、长按重置、旧 owner 拒绝与新关系建立。
- 发送真实原版画面，取得完整匹配回执，并人工确认内容、方向和颜色。

夜间测试不能替代这些实机步骤。
