# 额度数据来源与边界

本文记录本项目实现时实际核验的源码版本，并区分官方管理能力、供应商内部接口与被动观测。硬件板型和显示规格不在本文作推断，以项目 [README](../README.md) 的最终硬件章节为准。

核验日期：2026-09-02。

## 结论

CLIProxyAPI 没有一个可以主动返回所有供应商订阅余额的统一 `GET /quota`。可靠的主机端实现需要组合三层数据：

1. 通过 CLIProxyAPI 管理 API 枚举本地 OAuth 凭据（明确要求 `account_type=oauth`）并代表指定凭据发起请求；API-key 条目不会被送往 OAuth 私有额度端点。
2. 对已核验的供应商使用固定 host、path、method 和 headers 调用其内部额度接口。
3. 主动接口失败时，仅对 Claude/Codex 回退到 CLIProxyAPI 最近一次上游响应携带的被动额度 Header，并明确标记数据来源和时间。

“OAuth 已连接”“CLIProxyAPI 接受了请求”“主动额度获取成功”“数据仍然新鲜”是四件不同的事，不应合并为一个成功状态。

## 核验版本与许可证

| 项目 | 核验 commit | 许可证 | 在本项目中的用途 |
|---|---|---|---|
| [CLIProxyAPI](https://github.com/router-for-me/CLIProxyAPI) | `f0de1d008fe8881dcb7431cf97b147295874c2b2`；发布版 `v7.2.145` 为 `d9cea8904b14fbbebb77ef26e98ef08f6b48a724` | MIT | 管理 API、OAuth 凭据索引、`api-call`、被动 quota signals 的主证据 |
| [Gemini CLI](https://github.com/google-gemini/gemini-cli) | `0bd1d439751478771c45d3d0895a6a9760554bf4` | Apache-2.0 | Google Code Assist `retrieveUserQuota` 请求与 bucket 字段的官方客户端证据 |
| [CLI Proxy API Management Center](https://github.com/router-for-me/Cli-Proxy-API-Management-Center)（CPAMC） | `d249ff008e0bc2803deb23fb3e2c62418a1e8d17` | MIT | Claude、Codex、Antigravity 多端点调用和解析的交叉证据 |
| [CLIProxyAPI Quota Inspector](https://github.com/AllenReder/CLIProxyAPI-Quota-Inspector) | `1895bc54d0cbdd3b73ac85b3e535b23b7481a1a0` | MIT | CLIProxyAPI `api-call` 错误处理及 Gemini 调用的社区参考 |
| [Grok Build](https://github.com/xai-org/grok-build) | `bb7f39d5858cbf5e00de639367f59debbdcb0138` | Apache-2.0 | xAI 官方客户端 billing URL、headers 与周额度响应字段的主证据 |

这些仓库用于阅读契约和交叉验证。本项目不因引用其行为说明而获得供应商内部接口的稳定性保证；若后续直接复制源码或资源，必须同时保留对应许可证和版权声明。

## CLIProxyAPI：官方管理能力

核验仓库当前 `main` 为 `f0de1d0`，最新发布版为 `v7.2.145`。两者间相关 Go 源码一致，差异只涉及 README。

### 凭据枚举

`GET /v0/management/auth-files` 返回运行时凭据条目，包括 `auth_index`、provider、状态、标签以及被动 quota 观测。源码位置：

- [`internal/api/server_management.go`：管理路由](https://github.com/router-for-me/CLIProxyAPI/blob/v7.2.145/internal/api/server_management.go#L166-L183)
- [`internal/api/handlers/management/auth_files.go`：auth-file 响应](https://github.com/router-for-me/CLIProxyAPI/blob/v7.2.145/internal/api/handlers/management/auth_files.go#L329-L350)
- [`internal/api/handlers/management/auth_files.go`：quota payload](https://github.com/router-for-me/CLIProxyAPI/blob/v7.2.145/internal/api/handlers/management/auth_files.go#L448-L485)

该响应是主机内部输入，不能原样转发给相框。它可能包含本地路径、账号声明和其他不属于显示协议的信息。

### 代表凭据调用上游

`POST /v0/management/api-call` 接收 `auth_index`、`method`、绝对 `url`、headers 与可选 body；header 中的 `$TOKEN$` 会替换为所选凭据的 token。返回外层 JSON：

```json
{
  "status_code": 200,
  "header": {},
  "body": "{...upstream JSON...}"
}
```

证据：

- [请求/响应字段及示例](https://github.com/router-for-me/CLIProxyAPI/blob/v7.2.145/internal/api/handlers/management/api_tools.go#L29-L98)
- [任意绝对 URL 与 token 替换](https://github.com/router-for-me/CLIProxyAPI/blob/v7.2.145/internal/api/handlers/management/api_tools.go#L99-L163)
- [上游状态封装在内层 `status_code`](https://github.com/router-for-me/CLIProxyAPI/blob/v7.2.145/internal/api/handlers/management/api_tools.go#L187-L214)

管理 API 自身返回 HTTP 200 不代表供应商调用成功；必须再次检查响应 JSON 的 `status_code`。

### 被动额度观测

CLIProxyAPI 当前只为 `claude` 与 `codex` 保存 quota response headers。观测是“最近一次带额度信号的上游响应快照”，不是主动余额查询：

- [支持 provider 的白名单](https://github.com/router-for-me/CLIProxyAPI/blob/v7.2.145/sdk/cliproxy/auth/quota_signals.go#L15-L24)
- [新快照替换旧快照；无信号响应保留旧值](https://github.com/router-for-me/CLIProxyAPI/blob/v7.2.145/sdk/cliproxy/auth/quota_signals.go#L26-L53)
- [Claude/Codex Header 选择规则](https://github.com/router-for-me/CLIProxyAPI/blob/v7.2.145/sdk/cliproxy/auth/quota_signals.go#L149-L215)

因此，`quota.signals` 必须与 `observed_at` 一起使用，并在 UI/API 中标为 `cliproxy_headers`。它不能证明当前时刻仍有相同额度。

`POST /v0/management/reset-quota` 只清除 CLIProxyAPI 路由 cooldown/quota-error 状态，不会查询或重置供应商订阅额度：[源码](https://github.com/router-for-me/CLIProxyAPI/blob/v7.2.145/internal/api/handlers/management/quota.go#L26-L68)。

## 供应商主动接口：内部契约

下列接口都不是本项目控制的版本化公开 API。即便调用源来自官方客户端，`v1internal`、ChatGPT backend 或 OAuth usage 路径仍可能无通知漂移。

### Codex

当前实现使用：

```text
GET https://chatgpt.com/backend-api/wham/usage
Authorization: Bearer $TOKEN$
ChatGPT-Account-Id: <account id>
```

主要字段为 `plan_type`、`rate_limit.primary_window`、`secondary_window`、`used_percent`、`reset_at`、`reset_after_seconds` 与可能出现的 `additional_rate_limits`。

CPAMC 在 [`src/utils/quota/constants.ts`](https://github.com/router-for-me/Cli-Proxy-API-Management-Center/blob/d249ff008e0bc2803deb23fb3e2c62418a1e8d17/src/utils/quota/constants.ts#L124-L135) 使用同一接口。它是 ChatGPT 私有 backend 契约，不应称为 OpenAI 公开额度 API。

OpenAI 官方的 [Usage API / Costs API 示例](https://developers.openai.com/cookbook/examples/completions_usage_api#setup-api-credentials-and-parameters) 要求组织 Admin Key，并查询 `/v1/organization/usage/...`；它统计 OpenAI API 组织活动与成本，不等同于个人 ChatGPT/Codex 订阅的剩余额度。因此本项目没有用它替代上述 Codex 私有端点。

### Claude

当前实现使用：

```text
GET https://api.anthropic.com/api/oauth/usage
Authorization: Bearer $TOKEN$
anthropic-beta: oauth-2025-04-20
```

解析 `five_hour`、`seven_day`、模型专属窗口中的 `utilization` 与 `resets_at`。新版响应还可能在 `limits[]` 返回 scoped quota；当前实现只把同时满足以下条件的条目识别为现代 Fable 周额度：

- `kind` 为 `weekly_scoped`；
- `scope.model.display_name` 为 `Fable` 或 `Fable 5`；
- `percent` 是有效百分比；多个候选中优先选择 `is_active=true` 的条目，否则使用第一个合格候选。

一旦选到上述现代 Fable limit，解析器会输出稳定 ID `fable-7d`，并忽略同一响应中的旧版顶层 `iguana_necktie`，避免同一模型额度重复显示。其他模型的 `weekly_scoped` 条目以及其他 `kind` 不会被误归类为 Fable。若没有合格的现代 Fable limit，旧版 `iguana_necktie` 仍按兼容路径解析。

CPAMC 的基础 Claude 契约见 [`constants.ts`](https://github.com/router-for-me/Cli-Proxy-API-Management-Center/blob/d249ff008e0bc2803deb23fb3e2c62418a1e8d17/src/utils/quota/constants.ts#L99-L122)；其 [`data.ts`](https://github.com/router-for-me/Cli-Proxy-API-Management-Center/blob/d249ff008e0bc2803deb23fb3e2c62418a1e8d17/src/features/quota/providers/claude/data.ts#L40-L95) 也采用相同的 `weekly_scoped`、Fable 名称、active 优先和 legacy 抑制规则。该路径属于 OAuth 内部接口，而非公开稳定的计费 API；上述 scoped 字段同样可能继续漂移。

### Gemini CLI / Google Code Assist

Google 官方 Gemini CLI 在核验 commit 中直接调用 `retrieveUserQuota`：

- [`server.ts`：请求方法](https://github.com/google-gemini/gemini-cli/blob/0bd1d439751478771c45d3d0895a6a9760554bf4/packages/core/src/code_assist/server.ts#L367-L373)
- [`types.ts`：project 请求与 buckets 响应](https://github.com/google-gemini/gemini-cli/blob/0bd1d439751478771c45d3d0895a6a9760554bf4/packages/core/src/code_assist/types.ts#L250-L264)
- [`config.ts`：按 `remainingFraction`、`remainingAmount`、`resetTime` 归一化](https://github.com/google-gemini/gemini-cli/blob/0bd1d439751478771c45d3d0895a6a9760554bf4/packages/core/src/config/config.ts#L2307-L2352)

这是官方客户端行为证据，但 endpoint 仍位于 Google Code Assist 的 `v1internal` 面，不能据此承诺长期兼容。

### Antigravity

CPAMC 当前尝试三路 `retrieveUserQuotaSummary`，并在需要时调用 daily `loadCodeAssist`：[`constants.ts`](https://github.com/router-for-me/Cli-Proxy-API-Management-Center/blob/d249ff008e0bc2803deb23fb3e2c62418a1e8d17/src/utils/quota/constants.ts#L59-L97)。它解析 groups/buckets、`remainingFraction`、window 和 reset time，是当前实现的重要交叉证据。

Quota Inspector 的核验版本仍主要使用 `fetchAvailableModels` 处理 Antigravity，而非 CPAMC 的 `retrieveUserQuotaSummary`；因此它在 Antigravity 契约上已经落后，只能作为 Gemini 与 CLIProxyAPI 错误处理参考。相关实现见 [`providers.go`](https://github.com/AllenReder/CLIProxyAPI-Quota-Inspector/blob/1895bc54d0cbdd3b73ac85b3e535b23b7481a1a0/providers.go#L489-L605)。

### xAI / Grok

xAI 官方 Grok Build 客户端通过 CLI chat proxy 查询当前共享额度：

```text
GET https://cli-chat-proxy.grok.com/v1/billing?format=credits
Authorization: Bearer $TOKEN$
X-XAI-Token-Auth: xai-grok-cli
x-userid: <OAuth subject>
```

官方客户端优先读取 `config.creditUsagePercent`，并从 `config.currentPeriod.type/start/end` 判断 weekly 或 monthly 周期；旧响应才回退到 `monthlyLimit` / `used`。证据见 Grok Build 的 [`billing.rs`](https://github.com/xai-org/grok-build/blob/bb7f39d5858cbf5e00de639367f59debbdcb0138/crates/codegen/xai-grok-shell/src/extensions/billing.rs) 与 [`config.rs`](https://github.com/xai-org/grok-build/blob/bb7f39d5858cbf5e00de639367f59debbdcb0138/crates/codegen/xai-grok-shell/src/agent/config.rs)。CLIProxyAPI 自身也把 OAuth 默认流量指向同一 `https://cli-chat-proxy.grok.com/v1` 基址，见 [`types.go`](https://github.com/router-for-me/CLIProxyAPI/blob/v7.2.145/internal/auth/xai/types.go)。

CLIProxyAPI 的 auth-file 列表不会暴露 xAI `sub`，因此本项目通过同一受保护 management API 的 auth-file download 读取该非秘密标识；access token 仍只由 `api-call` 的 `$TOKEN$` 占位符注入，不进入归一化快照或相框请求。该 billing 路径属于官方客户端使用的内部契约，不是公开稳定的 xAI 计费 API。

## 漂移与降级策略

- 上游 host、path、method 和必要 headers 必须由主机代码硬编码白名单；相框请求不得提供或覆盖它们。
- 先检查 CLIProxyAPI 管理调用，再检查内层 `status_code`，最后验证供应商 JSON 是否含有可识别窗口；任一阶段失败都不得制造 `0%`。
- 解析器应容忍未知字段，但必须要求已知额度字段类型正确；缺字段时返回 `unknown` 或使用带时间戳的被动观测。
- 主动结果标记为 `oauth_internal_endpoint`，Header 回退标记为 `cliproxy_headers`，并保留 `data_updated_at`、`observed_at` 或 stale 信息。
- 对多 host fallback 只允许预先审计过的固定列表，不接受相框传入或覆盖的上游 URL。当前主机到 CLIProxyAPI 的 HTTP client 会拒绝 management endpoint 重定向；但 CLIProxyAPI `api-call` 内部仍使用 Go 默认 redirect policy，响应也不返回最终 URL，因此本服务无法观察或强制供应商侧“完全不跟随重定向”。Go 会阻止向无关域名转发 `Authorization` 等敏感 Header，但同 host/子域规则仍由 CLIProxyAPI 执行。高安全部署应给 CLIProxyAPI 增加 `CheckRedirect: http.ErrUseLastResponse`（或等价开关），并用出站防火墙只允许本文列出的供应商 host。
- 对接口路径、User-Agent、请求 metadata、bucket schema 建立固定 fixture 测试；升级上述依赖 commit 时重新核验，不从旧文档推断新版本行为。
- 尚无本项目已核验契约的 provider 应显示“OAuth 已连接，额度不可用”，而不是静默伪造百分比。

## 安全边界

`/v0/management/api-call` 能把选中 OAuth token 注入任意绝对 URL，本质上同时具备出站请求和 token delegation 能力。管理密钥泄漏可能导致 token 被发送到攻击者控制的地址。

推荐边界：

```text
相框
  -> 独立 FRAME_ACCESS_TOKEN
  -> 主机只读归一化 API
  -> 固定 provider allowlist
  -> localhost CLIProxyAPI management API
  -> 供应商内部接口
```

具体要求：

- CLIProxyAPI management 保持 `allow-remote: false`，优先绑定 localhost，所有管理调用使用强随机密钥。
- 相框只保存独立的 `FRAME_ACCESS_TOKEN`，绝不保存 management key、access token、refresh token 或 auth-file。
- 主机 API 只返回 provider、脱敏账号别名、额度、重置时间、来源、新鲜度与安全错误码。
- 不把供应商原始 body、本地文件路径、`auth_index`、ID token claims 或响应 headers 转发给相框。
- `ALLOW_INSECURE_NO_TOKEN` 仅用于隔离的开发环境，不用于实际局域网部署。
- OAuth 登录、刷新和凭据文件生命周期全部留在主机上的 CLIProxyAPI；相框不参与 OAuth flow。
