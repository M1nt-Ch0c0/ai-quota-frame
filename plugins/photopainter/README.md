# PhotoPainter CPA 插件

`plugins/photopainter/` 把网页插件打包成一个 CPA 插件：Go 写的 Linux 动态库（`.so`），由 CPA 加载后在 CPM 的插件菜单里出现“发送到相框”。当前版本 0.3.5，C ABI 1，插件 RPC schema 7。

这一页写给构建和发布插件的人，讲插件提供哪些路由、怎么构建和核对产物、怎么安装、升级和回滚。页面本身怎么工作见 [web/README.md](../../web/README.md)，页面的使用方法见[电脑端、控制台与网页插件](https://github.com/M1nt-Ch0c0/esp32s3/blob/main/docs/07-clients.md#cpm-网页插件)。

## 插件提供什么

插件只注册静态资源和一个管理接口，不注册模型、执行器、OAuth 或计费钩子，不监听新端口，也不代理蓝牙。采集、渲染、配对和推图都发生在访问页面的浏览器里。

### 静态资源

| 路径 | 内容 | Content-Type |
|---|---|---|
| `/v0/resource/plugins/photopainter/panel` | 页面 `panel.html`，菜单名“发送到相框” | `text/html` |
| `/v0/resource/plugins/photopainter/frame.wasm` | Go 引擎，带 `Content-Encoding: gzip` | `application/wasm` |
| `/v0/resource/plugins/photopainter/wasm_exec.js` | Go 的 WASM 运行时 | `text/javascript` |

资源前缀下的其他路径返回 404。所有响应都带 `Cache-Control: no-store`、`X-Content-Type-Options: nosniff`，以及：

- `Content-Security-Policy`：只允许同源脚本（含内联）、`wasm-unsafe-eval`、内联样式、`blob:`/`data:` 图片、同源连接和同源 iframe；`frame-ancestors 'self'`，`form-action 'none'`。
- `Permissions-Policy: bluetooth=(self)`。

### account-subject 接口

```text
GET /v0/management/plugins/photopainter/account-subject?name=<认证文件名>
```

这个接口挂在 CPA 的管理路由下，受 CPA 已有的管理鉴权保护；不带管理密钥访问返回 401。

它只服务一个用途：采集 Grok（xAI）额度需要账号的 subject，原生程序从认证文件里读，浏览器则调用这个接口。插件通过 CPA 的宿主回调列出认证记录，找到名称、`auth_index` 和服务商都对得上的唯一一条 xAI 记录，只返回：

```json
{"sub": "..."}
```

不返回 OAuth 文件、访问令牌、刷新令牌或文件路径。

出错时返回 `{"error": "<代码>"}`：

| 状态码 | 代码 | 原因 |
|---|---|---|
| 400 | `invalid_account_name` | 查询参数不是唯一一个 `name`，或 `name` 为空、超过 512 字节、含路径分隔符、控制字符或首尾空白 |
| 404 | `account_not_found` | 没有这个名称的认证记录 |
| 404 | `account_not_available` | 找到的记录不是启用中的 xAI 账号 |
| 404 | `account_subject_missing` | 认证记录里没有 subject |
| 409 | `ambiguous_account` | 名称对应的记录不是唯一一条 |
| 502 | `account_lookup_failed`、`account_read_failed`、`invalid_account`、`invalid_account_subject` | 从 CPA 读取认证记录失败或内容无效 |

## 构建

需要 Node 22.18 或更新版本、Go 1.26 或更新版本、Chrome 或 Chromium（网页检查用），以及目标平台的 C 编译器。

```sh
./plugins/photopainter/build.sh
```

`build.sh` 做三件事：

1. 在 `web/` 里 `npm ci --ignore-scripts && npm run build`。
2. 把 `web/dist/` 里的 `panel.html`、`frame.wasm.gz`、`wasm_exec.js` 复制到 `plugins/photopainter/`。
3. 运行 `build-native.sh`：执行插件的 Go 测试，然后用 `-buildmode=c-shared` 编译出 `dist/photopainter-v<版本>.so`。

版本号取自 `main.go` 里的 `const version`；网页的版本在 `web/package.json`，两者保持一致。复制进来的三个资源和 `dist/` 都不提交。

### 生产构建

生产环境是 Linux amd64。`.so` 必须和 CPA 服务器的平台一致，并且和服务器的 glibc 兼容。推荐做法：

1. 在本地完成网页构建和检查（见 [web/README.md](../../web/README.md#构建和测试)），把三个资源复制进本目录。
2. 在和服务器同一发行版的 Go 容器里断网运行 `./build-native.sh`。仓库的 CI 使用固定摘要的 Debian 12 Go 镜像，摘要写在 `.github/workflows/web-plugin.yml` 里：

   ```sh
   docker run --rm --network none \
     -v "$PWD:/workspace" -w /workspace/plugins/photopainter \
     golang@sha256:<工作流里的摘要> ./build-native.sh
   ```

插件的 Go 代码只依赖标准库，所以可以断网构建。

在 macOS 上也可以用 Zig 交叉编译 Linux 产物，但 Linux 版的测试只能在 Linux 上运行。先在本机跑测试和本机 C ABI 检查，再单独交叉编译：

```sh
cd plugins/photopainter
go test ./...
CGO_ENABLED=1 go build -buildvcs=false -trimpath -buildmode=c-shared -o /tmp/photopainter-native.so .
python3 verify-native.py /tmp/photopainter-native.so ../../web/dist/panel.html

GOOS=linux GOARCH=amd64 CGO_ENABLED=1 CC='zig cc -target x86_64-linux-gnu.2.34' \
  go build -buildvcs=false -trimpath -buildmode=c-shared -o dist/photopainter-v<版本>.so .
```

`-target` 里的 glibc 版本按服务器实际情况选择。

### 核对产物

```sh
python3 plugins/photopainter/verify-native.py \
  plugins/photopainter/dist/photopainter-v<版本>.so web/dist/panel.html
```

`verify-native.py` 在本机加载这个 `.so`（所以只能核对和本机同平台的产物），不需要服务器和真实凭据。它检查：

- C ABI 版本为 1，`plugin.register` 返回 schema 7，能力只有 `management_api`；
- 注册的资源正好是 `panel`、`frame.wasm`、`wasm_exec.js`，其中只有一个带菜单；
- 三个资源的字节和 `web/dist/` 里的文件完全一致，`frame.wasm` 带 gzip 编码且解压后是 WASM；
- 其他管理路径返回 404，未实现的方法返回失败。

通过后它输出一行 JSON，包含版本、库文件和各资源的字节数与 SHA-256，可以随发布记录保存。

## 安装和升级

1. 备份 CPA 的配置文件和插件目录。
2. 把新的 `.so` 放进 CPA 配置中 `plugins.dir` 指向目录下的 `linux/amd64/`，权限 0644，确保 CPA 的运行用户可读。每个版本使用新文件名，旧版本的文件留着，方便回滚。
3. 在 CPA 配置里启用 `plugins.configs.photopainter`，其他配置保持不变。CPA 检测到配置变化后，会扫描插件目录并热加载较新的版本，不需要重启。
4. 修改配置文件时必须**原地写入**，保留原文件的 inode。CPA 只监听原来那个文件；如果用“写临时文件再重命名”的方式覆盖，CPA 会失去对配置的监听，之后的修改都不生效，只能重启 CPA 才能恢复。
5. 发布后核对：
   - CPM 插件菜单出现“发送到相框”，页面显示的版本正确；
   - 三个资源的内容和 `verify-native.py` 输出的 SHA-256 一致；
   - 不带管理鉴权访问 `account-subject` 返回 401，带鉴权时能为 xAI 账号返回 subject；
   - 其他插件和服务不受影响。

## 停用和回滚

- **停用**：把 `plugins.configs.photopainter` 的 `enabled` 关掉，确认菜单和资源路由都消失了。
- **回滚**：先保存当前状态，然后撤下新版本的 `.so`，只恢复这个插件对应的配置改动，触发热加载。不要把整个 CPA 配置或数据库恢复成旧备份，以免覆盖别人之后做的修改。

浏览器里还没确认的图片记录保存在访问者的 IndexedDB 里，不会因为插件回滚而消失，仍按[发送结果未知](https://github.com/M1nt-Ch0c0/esp32s3/blob/main/docs/08-maintenance.md#发送结果未知)处理。
