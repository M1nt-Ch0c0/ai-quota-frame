# PhotoPainter 浏览器插件

浏览器端直接复用 ai-quota-frame 的 Go 数据和画面实现，设备连接使用按键批准的配对 v2。入口是 CPM 插件菜单“发送到相框”；原生资源包装见 [安装说明](../plugins/photopainter/README.md)。

**当前版本 0.3.2（新增开发者工具）。** 配合 photoframe v21 / ABI 2.3，手机 67,626 字节上传 7.9 秒，签名显示回执与人眼确认通过；前台定时也已用真实 1 分钟检查和自动发送回执验证。首次物理配对/长按换绑等完整矩阵仍待验收，范围见 [现场记录](../docs/validation-gatt-capacity-2026-10-29.md)。

## 画面与数据

`cmd/web-frame` 把原来的 collector、usage、service、平台聚合、主题模板、图标、皮肤、量化、PNG 编码和 `frame.ContentKey` 编译为 Go/WASM。浏览器负责同一份 HTML 的布局光栅化，不再维护简化 Canvas 卡片或另一套额度窗口规则。

默认与现用原生配置相同：

- `mint-skin` 主题；CLAUDE、CODEX、GROK 三行。
- 上海时区，15 分钟更新；可选择原有五种主题、1–5 个平台和时区，沿用原生上限。
- 主动额度采集、原有被动回退、重置时间、套餐和七日用量使用同一实现。
- 预览、下载和发送使用同一份 800×480、不透明、精确六色 PNG。

默认读取同源 CPM 已保存的登录（`cli-proxy-auth`），无需重复输入管理密钥；宿主未保存密钥时，可手动输入并选择“在此浏览器记住手动配置”。手动配置采用与 CPM 兼容的本地混淆格式保存，这不是加密；清除入口仅移除本插件配置，不退出 CPM。管理密钥仅用于同源管理请求，不进入相框或服务端插件配置。xAI 所需的账号 subject 由受管理鉴权保护的窄接口投影；浏览器不下载完整 OAuth 文件。失败采集不能把旧图作为新图发送；已有缓存可呈现其真实过期状态。

## 连接相框

1. 使用支持 Web Bluetooth 的 Chrome，通过 HTTPS 打开插件。
2. 点击“添加相框”，在浏览器选择器中选择设备。
3. 页面与相框显示同一组六位数字。数字相同时，**按一下相框的功能键（GPIO4）**。
4. 相框实际完成配对码显示、客户端保存候选关系、设备持久保存批准后，才显示配对完成。
5. 以后在同一浏览器中连接这台相框，不需要重新配对。即使浏览器要求重新选择设备，已有关系仍可认证。
6. 更换控制端时，在相框上长按功能键至少 8 秒并松开，再发起新的配对。BOOT 和电源键不参与。

设备只信任一个 owner；同一客户端可以记住多台相框。浏览器与原生程序是不同 owner，迁移由物理按键重置后完成。功能键重置只清除配对和会话，不清除 Wi-Fi 或已安装 APP。长按可在刷新期间提出重置；已经开始的物理刷新完整结束后，才执行持久清除，新操作会立即被挡住。

这是应用层的物理配对协议，不是操作系统 SMP bond。会话使用派生密钥、挑战和 HMAC 防重放；图片内容沿用现有传输格式。协议细节见宿主 `docs/ble-pairing-v2.md`。

## 发送与恢复

已连接后发送当前预览。进度来自相框确认的字节数，物理刷新完成必须有匹配 request ID、长度、SHA 和接收字节数的签名回执。

- COMMIT 前用 strict IndexedDB 事务保存非秘密图片身份；保存失败不提交。
- 提交前取消使用串行签名 CANCEL，只有取消确认后才清除该事务。
- 提交后中断保留记录，重连仅 QUERY 原请求，不自动重发 COMMIT。
- 配对 BID、APP generation/revision 改变或回执缺失时，结果不确定；先人工检查画面，再明确清除记录。
- 同源标签页在连接前取得会话锁；候选与待确认记录使用原子创建/匹配删除。
- 浏览器不支持 Web Locks 或 IndexedDB strict durability 时，连接/提交失败关闭，不以普通事务或无锁操作替代。
- 自动发送仅在用户开启后运行：周期采集、只保留最新成功快照、用原生内容指纹去重；失败与取消不会形成即时重试循环。

页面可导入原生完整 `quota.Snapshot` JSON 或已校验 PNG。浏览器结束后不会留下原生后台进程；再次打开时通过已保存的配对关系连接。

## 凭据与存储

设备没有需要手工填写的固定推图令牌。浏览器保存不可导出的 HMAC `CryptoKey` 及设备/CID/BID；原生程序保存于 0600 文件。候选关系先持久化，丢失批准回复可通过后续 BondAuth 恢复。管理密钥不进入设备、配对记录、URL 或日志。

“移除此相框”只删除本机记忆，不远程解除设备 owner，也不删除未确认图片身份。设备上的 owner 只能用功能键重置。清除浏览器数据后若遗失关系，需要重新进行物理配对。

## 开发

Node 22.18+、Go 1.26+ 和 Chrome/Chromium；本轮 Node 26.12.1 / Go 1.27.2，CI 使用 Go 1.27.3。运行：

```sh
cd web
npm ci --ignore-scripts
npm run check
node tools/check-frame.mjs
node tools/check-ui.mjs
```

检查工具会查找本机 Chrome，也可通过 `CHROME_BIN` 指定路径；缺少浏览器时直接失败，不把跳过渲染验证当成通过。只重跑软件断言可用 `PHOTOFRAME_UI_CAPTURE=0 node tools/check-ui.mjs`。这些工具只使用 loopback fixture 和新建的隔离浏览器 profile，不访问真实 CPM 或 BLE。

构建得到 `dist/panel.html`、`frame.wasm.gz` 和匹配的 `wasm_exec.js`。插件通过三个同源资源路由提供它们，WASM 资源发送 `Content-Encoding: gzip`。本机开发可用 localhost。CI 显式准备 Go、Node、Chrome，执行网页、共享画面对照及 UI fixture 检查；随后使用固定 Debian 12 builder 生成 Linux amd64 插件并比对 C ABI/嵌入资源。CI 上传的产物名称包含 `unverified-hardware`，不代表实机验收。

## 2026-10-29 验证范围

- 32 项浏览器协议、配对、strict 存储和图片事务测试通过；独立 OpenSSL 固定向量与 C/Go/WebCrypto 一致，并检查 Go/Web 镜像文件逐字节相同。
- 71 项隔离浏览器交互检查通过，包括主题/原生 PNG、浮窗/焦点、存储恢复、失败/过期数据、自动去重和取消。
- 同一 fixture 的五种主题 × 两种账号场景对照：四种主题全部 384,000 像素相同；mint-skin 有 3 个由 SVG/Chrome 字形灰度跨阈值产生的像素差（原始灰度差不超过 2/255）。HTML、模板、图标、数据和最终量化均共享 Go 实现，未用手工像素补丁伪造一致。
- 本轮界面方案实际由 Claude Code `claude-opus-5-5 --effort xhigh` 生成；协议与持久化建议经独立代码审阅后按冻结合同实现，不直接采用设计文本中的协议假设。
- 配对 UI/发送模拟明确标为模拟。真实按键、设备重启持久化、重置后旧 owner 拒绝及真实刷图，需在设备更新后另行验收；不能用测试浮窗宣称已完成。

详细证据：[共享画面与采集验证](../docs/web-frame-parity.md)。

### GATT write sizing (0.2.3)

Requires a host exposing read-only `7b9d0006-6c82-4f31-9a48-6f5061696e74`:
`version:u8=1, write_capacity:u16le`. The browser probes connection capacity and reuses a negotiated value above the
minimum; an initial 20-byte capacity is rechecked on subsequent exchanges and reserves four bytes for fragment headers.
MTU 247 uses 240 data bytes per acknowledged write; MTU 23 uses 16. Deploy the
matching host first. Application image chunks remain 106 bytes under ABI 2.2's
128-byte value contract; pairing, signatures and final image receipts are unchanged.


### 定时推送（0.3.1）

主界面的“定时推送”可设置 1–1440 分钟间隔，默认 15 分钟。开启时连接已配对
相框并立即获取一次新用量；之后每轮先采集、生成画面，内容有变化才发送。
设置保存在当前浏览器；卡片显示下一次检查和本页最近的确认成功时间。
暂停不会中断已经开始的显示，但不再启动下一次自动发送。失败数据不排队，
有待确认回执时只允许核对原事务，不自动重复 COMMIT。

网页需保持前台运行。手机可自行勾选“定时期间保持屏幕唤醒”，使用浏览器
Screen Wake Lock，不修改系统常亮设置；不支持/被拒绝或释放时显示原因。
隐藏页面暂停定时自动推送，回到前台后继续；错过的轮次不逐条补发。
锁屏、切换应用、浏览器休眠或关闭页面后不能保证准时运行，服务器也不能
通过访问者手机的蓝牙发送。需要全天后台调度时应使用手机原生后台服务。

### 开发者工具（0.3.2）

相框卡片下方的“开发者工具”默认折叠，使用已保存的物理配对关系直接管理相框应用槽：

- **读取槽位**：显示每个应用 A/B 两槽的版本，以及当前、可回退、待激活、运行中标记。
- **选择安装包**：在本机校验 `.pkg` 的格式、应用标识、载荷 SHA-256 和 Xtensa ELF 头，不合格不上传。
- **上传并激活 / 仅上传**：写入非当前槽（宿主规则），激活后旧槽成为可回退槽。两个槽都换成同一版本时，同一安装包“上传并激活”两次。
- **激活待定版本、切换到此应用、回滚、删除此应用**：对应宿主 ACTIVATE / SWITCH / ROLLBACK / REMOVE；删除只能用于非运行中的应用，需在 5 秒内再次确认。

有待核对的图片回执、配对或发送进行中时这些按钮不可用。每个变更指令只发送一次；回复丢失时重新连接并只读取签名状态，不重复发送。宿主没有单槽擦除指令，不能只删除某一个槽。
