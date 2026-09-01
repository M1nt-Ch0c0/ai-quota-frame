# 主机只读 API

主机服务只向局域网相框暴露三个只读端点。CLIProxyAPI 管理密钥、OAuth access token、refresh token、`auth_index` 和上游原始响应均不得出现在这些响应中。

默认地址为 `http://<host>:8787`。除健康检查外，请求必须携带独立的相框访问令牌：

```http
Authorization: Bearer <FRAME_ACCESS_TOKEN>
```

缺少或使用错误令牌时返回 `401 Unauthorized`，并携带：

```http
WWW-Authenticate: Bearer realm="ai-quota-frame"
```

服务统一返回 `X-Content-Type-Options: nosniff` 与 `Referrer-Policy: no-referrer`。

## `GET /healthz`

无需 Bearer，用于局域网探活，不返回账号或额度数据。

首次刷新尚未完成时返回 `503`：

```json
{
  "status": "starting",
  "stale": false
}
```

存在可用快照时返回 `200`。正常状态为：

```json
{
  "status": "ok",
  "stale": false
}
```

没有任何启用账号拥有新鲜额度值时，状态为 `degraded` 且 `stale` 为 `true`。

## `GET /api/v1/quota`

返回经过归一化的额度快照。该接口需要 Bearer，不会返回 CLIProxyAPI 的原始 auth-file。

```bash
curl -sS \
  -H "Authorization: Bearer ${FRAME_ACCESS_TOKEN}" \
  http://192.168.1.10:8787/api/v1/quota
```

示例响应：

```json
{
  "schema_version": 1,
  "refreshed_at": "2026-08-30T02:10:00Z",
  "data_updated_at": "2026-08-30T02:05:00Z",
  "last_fresh_at": "2026-08-30T02:00:00Z",
  "next_refresh_at": "2026-08-30T02:15:00Z",
  "stale": true,
  "accounts": [
    {
      "provider": "codex",
      "name": "j***@example.com",
      "plan": "plus",
      "status": "ok",
      "stale": true,
      "last_fresh_at": "2026-08-30T02:00:00Z",
      "windows": [
        {
          "id": "5h",
          "label": "5h",
          "used_percent": 38,
          "remaining_percent": 62,
          "resets_at": "2026-08-30T05:00:00Z",
          "observed_at": "2026-08-30T02:04:35Z",
          "source": "cliproxy_headers"
        },
        {
          "id": "7d",
          "label": "7d",
          "used_percent": 57,
          "remaining_percent": 43,
          "resets_at": "2026-09-03T00:00:00Z",
          "observed_at": "2026-08-30T02:04:35Z",
          "source": "cliproxy_headers"
        }
      ],
      "warning": "active refresh failed; showing the latest CLIProxyAPI response-header observation"
    }
  ]
}
```

字段语义：

- `schema_version`：当前固定为 `1`；客户端应在解析其他字段前检查该版本。
- `refreshed_at`：主机最近一次尝试刷新快照的时间。
- `data_updated_at`：会影响显示的数据最近一次发生变化的时间。
- `last_fresh_at`：最近一次至少取得一个新鲜、可用额度值的时间；全量刷新失败或只剩被动/保留数据时不会前移。首次从未取得新鲜值时省略。
- `next_refresh_at`：主机计划的下一次刷新时间；它不是供应商额度的重置时间。
- `stale`：当前快照是否因刷新失败而进入旧数据状态。
- `accounts[].provider`、`name`、`plan`：分别为归一化 provider、脱敏账号名/操作者别名和可选套餐；`plan` 未知时省略。
- `accounts[].status`：可能为 `ok`、`low`、`exhausted`、`unknown`、`disabled` 或 `error`。
- `accounts[].stale`：该账号当前展示的是被动 Header 观测或保留的上一份成功数据。顶层 `stale` 只在没有任何启用账号拥有新鲜额度时为 `true`；混合场景会保持顶层可用，同时在对应账号和画面行上标出 `stale`。
- `windows[].id`、`label`：分别为供客户端识别的稳定窗口 ID 和显示标签；客户端不应把标签当作协议键。
- `windows[].source`：`oauth_internal_endpoint` 表示主机主动查询了供应商内部接口；`cliproxy_headers` 表示使用 CLIProxyAPI 最近一次上游响应的被动 Header 观测；`demo` 只用于演示模式。
- `accounts[].last_fresh_at`：该账号最近一次主动额度查询成功的时间；降级到被动或保留数据时不会前移。
- `windows[].observed_at`：该窗口值实际被主动查询或由 CLIProxyAPI Header 观察到的时间，不是看板调度时间。服务会用它避免让更老的被动 Header 覆盖刚取得的主动结果。
- 百分比和 `resets_at` 都是可选字段。上游没有给出可信值时字段会省略，客户端不得把缺失值当作 `0`。
- `warning`、`error` 与顶层 `errors` 都是可选字段。

### ETag 与 304

成功响应包含：

```http
ETag: "<content-hash>"
Cache-Control: private, max-age=0, must-revalidate
```

客户端可在下次请求中发送：

```http
If-None-Match: "<previous-etag>"
```

内容未变化时返回 `304 Not Modified`，响应体为空。JSON ETag 针对完整 JSON 快照计算；调度时间字段改变时 ETag 也可能改变。

## `GET /api/v1/frame.png`

返回主机渲染后的 `image/png`。当前构建的渲染画布为 `800x480`；这只是当前软件输出契约，不在本文推断最终硬件规格。硬件板型与显示接入以项目 [README](../README.md) 的最终说明为准。

```bash
curl -sS \
  -H "Authorization: Bearer ${FRAME_ACCESS_TOKEN}" \
  -H "X-Display-Width: 800" \
  -H "X-Display-Height: 480" \
  -o frame.png \
  http://192.168.1.10:8787/api/v1/frame.png
```

`X-Display-Width` 与 `X-Display-Height` 可省略；一旦提供，就必须是十进制的 `800` 与 `480`，否则返回 `400 Bad Request`。

PNG 接口同样支持 `ETag`、`If-None-Match` 与 `304`。ETag 根据最终 PNG 字节计算，因此仅 `refreshed_at` 或 `next_refresh_at` 改变、而画面内容未改变时，PNG ETag 保持稳定。相框应在 `304` 时跳过刷新，以减少网络传输和无意义的屏幕更新。

## 错误响应

错误响应使用简洁 JSON：

```json
{
  "error": "unauthorized"
}
```

可能状态码：

- `400`：显示尺寸与当前构建不匹配。
- `401`：Bearer 缺失或错误。
- `503`：首次额度快照尚未准备好。
- `500`：主机无法编码快照或渲染 PNG。

相框应保留上一次成功画面，在下一周期重试；不得因为一次失败而清空显示。
