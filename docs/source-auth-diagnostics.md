# 数据源鉴权与只读诊断

核实日期：2026-10-29（UTC+8）。数据源采集与设备 BLE/Wi-Fi 可达性独立；后台进程存在或屏幕保留画面，都不证明采集成功。

**配对 v2 尚未实机验证。** 本页的数据源检查不会验证设备配对或显示。夜间
交付仅运行本地测试，不重跑本页真实服务器诊断、不加载生产凭据或修改服务。

## 鉴权模式

- `SOURCE_AUTH_MODE=management-key`：用 `CLIPROXY_MANAGEMENT_KEY` 访问 CPA 管理接口，用 `CPAMP_ADMIN_KEY` 访问 CPM 用量接口。允许保留 Google 服务账号文件作为历史材料，但不会读取它来发起 ID Token 鉴权。
- `SOURCE_AUTH_MODE=google-id-token`：显式使用 `GOOGLE_SERVICE_ACCOUNT_FILE` 和 `GOOGLE_ID_TOKEN_AUDIENCE`，要求目标网关仍验证 Google ID Token 并代理管理鉴权。

不指定模式时兼容原来的单一凭据配置。Google 文件和静态密钥同时存在时拒绝启动，要求明确选择，避免静默覆盖。Google 登录被停用后，不能把 Google ID Token 当成原 CPM 管理密钥使用。

## 只读检查

在可信启动环境中运行 `ai-quota-frame source-check`。此命令只构建数据源客户端、读取额度和用量，不检查 Chrome、不创建 BLE 客户端、不生成图片、不启动 daemon，也不发送相框请求。输出鉴权模式、凭据是否已配置、采集是否成功、账号数量及按 provider 分类的状态/错误；不输出账号名称、管理密钥、设备令牌、Google 凭据或 HTTP 响应正文。检查会访问已有 provider 的额度查询端点，不发起模型生成。

`source-check --static` 可临时测试已有静态密钥，不修改配置；持久模式切换仍应通过 `SOURCE_AUTH_MODE`。不要把秘密作为命令行参数或复制到聊天中。

额度读取成功不代表每个账号都正常。`quota_ok=true` 表示账号清单和采集流程成功，`quota_fresh` 与 `fresh_account_count` 才说明本轮是否取得可用新鲜额度；仍须检查 `account_states` 和 `account_issues`，停用账号、上游限额和账号 OAuth 故障分别报告。迁移工具要求新鲜额度验证通过才保存候选配置，不能只用旧观测回退证明恢复。

## 已退役网关迁移

`deploy/migrate-source-auth.py` 面向已有服务器管理密钥的受信部署：

```sh
python3 deploy/migrate-source-auth.py \
  --ssh-host root@your-server.example \
  --manager-url https://manager.example \
  --probe-binary /absolute/path/ai-quota-frame
```

默认只验证候选配置；追加 `--apply` 才保存。工具通过 SSH 私下读取服务器 `/etc/cpa-manager-plus/config.json` 指定的现有 `adminKeyFile`，先验证其在 CPA 与 CPM 上均可鉴权，再用 `source-check` 验证本机到 HTTPS 管理入口的完整采集。不显示或更新服务器密钥，不修改服务端配置。

保存前要求运行配置为当前用户所有的普通 `0600` 文件；原样备份到同目录带时间戳的 `.env.before-source-auth-*`，仅替换模式、两个来源地址/管理密钥及 `NO_PROXY`/`no_proxy`。历史设备令牌字段（新版 BLE 不使用）、主题、展示行、刷新间隔等无关字节保持不变。写入使用原子替换，校验期间配置变化则拒绝保存。原代理设置保留，只将管理入口域名加入绕过列表。凭据不放入 argv、工具输出或仓库。

工具不重启后台，也不改变自动推图暂停设置。运行进程不会自动加载新环境，应在设备维护结束后协调重启，并先在 `automatic=false` 下确认采集状态，再单独验证图片完成回执。

## 2026-10-29 夜间离线任务开始前的现场结果

原配置仍使用 Google ID Token，但服务器 Manager 已改为原管理密钥直接鉴权。本机 `HTTP_PROXY` / `HTTPS_PROXY` 指向 `127.0.0.1:7890`，检查时该端口不可达；只读检查实际在换取 Google Token 时失败，尚未到达管理 API。恢复该代理也不能重新启用已退役的服务端 Google 鉴权。

候选改为普通管理密钥并绕过该管理域名的本机代理后，账号清单和用量读取成功，但首次完整诊断发现主动额度查询仍返回管理 API `403`。Claude/Codex 当时仅使用旧响应头观测回退，不能据此宣称新鲜额度恢复。根因是入口要求 POST 的 `Origin` 匹配管理站点，而旧 Go 客户端未带该头。客户端现为管理写请求使用已校验来源 URL 的 origin，保留原 Bearer 鉴权与服务器防护。

已保存私有配置及 `0600` 备份，未由迁移工具重启服务或触发设备操作。最终后台加载、自动推图和实体刷新证据由项目状态页单独记录，不将数据源验证当作设备刷新成功。

00:28:41 最终只读检查确认：`quota_ok=true`、`quota_fresh=true`、`fresh_account_count=4`、`usage_ok=true`、`warning_count=0`。Claude、Codex、Antigravity 取得正常额度，xAI 返回耗尽状态，Kimi/Qoder 保持原停用。最终修复构建通过 `make check`（单元、race、vet、macOS 原生 CGO 构建）；迁移工具的配置保留、重复赋值拒绝和私有备份保护测试 3 项通过。该验证没有调用蓝牙或刷新屏幕。
