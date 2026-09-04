# AI Quota Frame

> **AI / 开发者入口：**先阅读 [`AGENTS.md`](AGENTS.md)。空白电脑部署、三仓联调和真机诊断使用 [`develop-photopainter-stack`](https://github.com/M1nt-Ch0c0/photopainter-host/blob/main/.agents/skills/develop-photopainter-stack/SKILL.md)。

`ai-quota-frame` 在主机侧读取额度、生成 800×480 的 Spectra 6 六色 PNG，并主动 `POST` 原始 PNG 到常亮的 ESP32-S3 PhotoPainter。设备不再轮询主机，也不使用相册、WebUI、OTA、Home Assistant 或深睡眠流程。

## 刷写

设备固件由独立的 [`M1nt-Ch0c0/photopainter-host`](https://github.com/M1nt-Ch0c0/photopainter-host) 仓库构建；可加载组件位于 [`M1nt-Ch0c0/photoframe`](https://github.com/M1nt-Ch0c0/photoframe)。不要把本仓库、第三方相册/轮询固件或微雪整机工程当作设备底包。

刷写前必须先完整备份并校验设备的 16 MiB Flash；机内有 microSD 时也要关机取卡并制作整卡镜像。具体构建、刷写和 NVS 配网步骤以 `photopainter-host` 的 README 为准。备份、密钥文件和生成的 NVS 镜像都应放在仓库之外。

## 推图

复制配置并生成彼此不同的随机令牌：

```bash
cp .env.example .env
chmod 600 .env
openssl rand -hex 32
```

主动推送所需的核心配置如下：

```dotenv
CLIPROXY_BASE_URL=http://127.0.0.1:8317
CLIPROXY_MANAGEMENT_KEY=replace-with-management-key
PHOTOFRAME_PUSH_URL=http://192.168.1.50/api/push
PHOTOFRAME_PUSH_TOKEN=replace-with-generated-token
```

主机必须安装 Chromium 或 Google Chrome；也可用 `CHROME_BIN` 指向浏览器可执行文件。程序会在启动时检查浏览器是否可用，然后才开始刷新额度和推图。

构建并启动：

```bash
make check
set -a
. ./.env
set +a
./dist/ai-quota-frame
```

每次成功的额度刷新都会交给单工作线程处理；队列只保留最新快照。主机生成的图片必须不超过 5 MiB，并以 `Content-Type: image/png` 和独立 Bearer 码发送到完整的 `/api/push` URL。客户端不使用环境代理、不跟随 HTTP 重定向，且只有设备返回 `200` 才记为刷屏完成。

刷新失败会立即作废待推送的旧 LIVE 帧，并尽力取消尚未完成的旧请求；失败快照不会推到设备。单次 HTTP 总超时为 11 分钟，覆盖一次刷屏中最多五个串行的 120 秒 BUSY 等待，避免设备已刷完但客户端过早超时后重复刷屏。网络错误、`408`、`409`、`423`、`425`、`429` 和 `5xx` 可以重试，期间仍只保留最新成功快照。渲染错误不进入定时重算，只等下一次成功额度刷新；其他非 `200` 状态（包括 `401`、`413`、`422` 和重定向）会永久拒绝当前 PNG 摘要，图片内容变化后才允许再次尝试。

## 鉴权

`PHOTOFRAME_PUSH_TOKEN` 只用于设备的 `POST /api/push`，须为 32–128 个无空白可打印 ASCII 字节。启动校验会拒绝它与以下任何已配置密钥相同：

- `CLIPROXY_MANAGEMENT_KEY`
- `CPAMP_ADMIN_KEY`

设备未配置推图码时返回 `503`，缺失或错误码返回 `401`，请求体超过 5 MiB 返回 `413`；这些失败请求不得改变屏幕。主机不会在日志中输出推图码，也不会把 OAuth token、management key 或用量采集密钥发送到相框。

不要提交 `.env`、设备密钥、Flash/SD 备份或含密钥的命令输出。相框只应位于受信局域网，禁止把设备的 HTTP 端口映射到公网。
