# 数据源

这一页讲 `ai-quota-frame` 从哪里读取额度和用量、两种鉴权方式怎么选、怎么用 `source-check` 检查采集是否正常，以及两个和数据源有关的部署脚本。配置项的完整列表在[电脑端、控制台与网页插件](https://github.com/M1nt-Ch0c0/esp32s3/blob/main/docs/07-clients.md#数据源)里，这里不重复。

## 读哪些接口

| 数据 | 服务 | 地址变量 | 密钥变量 |
|---|---|---|---|
| 账号列表和额度 | CLIProxyAPI（CPA）管理接口 | `CLIPROXY_BASE_URL` | `CLIPROXY_MANAGEMENT_KEY` |
| 今日和近 7 日 token 用量（可选） | cpa-manager-plus（CPAMP） | `CPAMP_BASE_URL` | `CPAMP_ADMIN_KEY` |

程序用到的管理接口是 `GET /v0/management/auth-files`、`POST /v0/management/api-call`（代各账号查询额度）和 CPAMP 的 `GET /v0/management/dashboard/summary`。xAI 账号缺少 subject 时，原生程序还会通过 `/v0/management/auth-files/download` 读取认证文件。

对 POST 请求，客户端会带上和目标地址一致的 `Origin` 请求头，因为有的管理网关要求写请求的来源匹配。

## 两种鉴权方式

用 `SOURCE_AUTH_MODE` 选择：

| 模式 | 什么时候用 | 需要的变量 |
|---|---|---|
| `management-key` | 程序能直接访问 CPA 和 CPAMP 的管理接口（本机、内网或 SSH 隧道） | `CLIPROXY_MANAGEMENT_KEY`；配了 CPAMP 时还要 `CPAMP_ADMIN_KEY` |
| `google-id-token` | 管理接口放在接受 JWT bearer 的 oauth2-proxy 后面，由网关代为注入管理密钥 | `GOOGLE_SERVICE_ACCOUNT_FILE`、`GOOGLE_ID_TOKEN_AUDIENCE`（oauth2-proxy 的 client_id）；两个 `BASE_URL` 指向网关的 HTTPS 地址 |

`google-id-token` 模式下，程序用服务账号私钥签一个断言，向 `oauth2.googleapis.com` 换取一小时有效的 ID Token，并在过期前 5 分钟刷新。私钥文件只在本机读取，权限应为 0600，放在仓库之外。网关只需为这个服务账号放行上面列出的几个管理接口。

不写 `SOURCE_AUTH_MODE` 时，程序按已配置的凭据推断：只有服务账号文件就用 `google-id-token`，否则用 `management-key`。两种凭据都配了会报错退出，要求明确选择。选了 `management-key` 时，即使目录里还留着服务账号文件，程序也不会读它。

## 用 source-check 检查采集

`source-check` 只采集一次额度和用量，然后输出一行 JSON 并退出。它不启动 Chrome、不打开蓝牙、不生成图片，也不访问相框。

```sh
set -a; . ./.env; set +a
./dist/ai-quota-frame source-check            # 按配置的模式检查
./dist/ai-quota-frame source-check --static   # 临时改用已配置的管理密钥检查，不改配置
```

整次检查最长 2 分钟。采集失败或配置无效时退出码为 1。输出字段：

| 字段 | 含义 |
|---|---|
| `auth_mode` | 实际使用的鉴权模式 |
| `management_key_configured`、`usage_key_configured`、`usage_configured` | 各项是否已配置（只给布尔值） |
| `quota_ok` | 账号列表和额度采集流程成功 |
| `quota_fresh`、`fresh_account_count` | 这一次是否拿到了新鲜额度，以及有几个账号是新鲜、无错误、未停用的 |
| `account_count`、`warning_count` | 账号数和警告数 |
| `account_states` | 按状态统计的账号数 |
| `account_issues` | 有错误或警告的账号，只列服务商和错误描述 |
| `usage_ok`、`usage_error` | 用量采集结果（配置了 CPAMP 时） |
| `quota_error`、`error` | 失败原因 |
| `observed_at`、`elapsed_ms` | 检查时间和耗时 |

`quota_ok=true` 只说明流程跑通了。某个账号被停用、上游限额或 OAuth 失效时，`quota_ok` 仍可能为 `true`，要看 `quota_fresh` 和 `account_issues` 判断每个账号的情况。输出里没有密钥、账号名、额度数值和接口原文，可以直接贴给别人看。

## 部署脚本

### SSH 隧道（macOS）

数据源在远程服务器上、只监听服务器本机端口时，可以用 SSH 本地转发访问。`deploy/install-source-tunnel.py` 把隧道装成一个 LaunchAgent（`com.m1ntch0c0.photopainter-source-tunnel`），登录时启动，断线后自动重连：

| 本机地址 | 转发到服务器的 |
|---|---|
| `127.0.0.1:28317` | `127.0.0.1:8317`（CPA） |
| `127.0.0.1:28318` | `127.0.0.1:18317`（CPAMP） |

1. 在 `~/.ssh/config` 里建一个主机别名（例如 `photopainter-source`），写好服务器地址、用户和密钥，并确认已经核对过服务器的主机指纹。
2. 确认不需要交互就能登录：

   ```sh
   ssh -o BatchMode=yes -o StrictHostKeyChecking=yes photopainter-source true
   ```

3. 安装隧道（加 `--dry-run` 只打印将要写入的 plist）：

   ```sh
   python3 deploy/install-source-tunnel.py --ssh-host photopainter-source
   ```

4. 在 `.env` 里把 `CLIPROXY_BASE_URL` 设为 `http://127.0.0.1:28317`，`CPAMP_BASE_URL` 设为 `http://127.0.0.1:28318`，并填写远端的管理密钥。

脚本不创建也不复制 SSH 密钥，保持主机指纹校验，只监听本机回环地址。日志在 `~/Library/Logs/photopainter-source-tunnel.log` 和 `photopainter-source-tunnel.error.log`。查看状态：

```sh
launchctl print gui/$(id -u)/com.m1ntch0c0.photopainter-source-tunnel
```

### 从 Google 网关迁移到管理密钥

`deploy/migrate-source-auth.py` 用于这种情况：原来走 `google-id-token` 网关，现在服务器改为直接用管理密钥鉴权。它通过 SSH 从服务器上读取现有的管理密钥，验证通过后改写本机配置。

```sh
python3 deploy/migrate-source-auth.py \
  --ssh-host user@server.example \
  --manager-url https://manager.example \
  --probe-binary "$PWD/dist/ai-quota-frame" \
  [--env-file 配置文件] [--apply]
```

| 参数 | 说明 |
|---|---|
| `--ssh-host` | 能登录数据源服务器的 SSH 目标 |
| `--manager-url` | 管理中心的 HTTPS 地址，同时用作 CPA 和 CPAMP 的地址 |
| `--probe-binary` | 用来执行 `source-check` 的 `ai-quota-frame` 程序 |
| `--env-file` | 要改写的配置，默认 `~/Library/Application Support/ai-quota-frame/.env` |
| `--apply` | 验证通过后保存。不加时只验证，不改任何文件 |

执行过程：

1. 通过 SSH 读取服务器 `/etc/cpa-manager-plus/config.json` 里 `adminKeyFile` 指向的密钥，并在服务器本机确认它能访问 CPA 和 CPAMP。
2. 在内存里生成候选配置：`SOURCE_AUTH_MODE=management-key`，两个 `BASE_URL` 改为 `--manager-url`，两个密钥改为读到的密钥，并把管理中心域名加入 `NO_PROXY` 和 `no_proxy`。
3. 用候选配置运行一次 `source-check`。只有 `quota_ok`、`usage_ok`、`quota_fresh` 都为 `true` 才算通过。
4. 加了 `--apply` 时，先把原配置备份到同目录的 `.env.before-source-auth-<时间>`（权限 0600），再原子替换配置文件。验证期间配置被别人改过就放弃保存。

配置里其他行（主题、显示行、刷新间隔、其他代理设置等）保持原样。密钥不会出现在命令行参数和输出里。脚本不重启后台；新配置要在下次启动 `daemon` 时生效。
