# fnos_daili

把任意 HTTP(S) 目标装成飞牛（fnOS）桌面应用的小工具：填一个 `ip:port`，生成一个自包含的代理 `.fpk`，装好后从飞牛桌面直接打开。

当前仓库包含**代理运行时**、**fpk 生成器**、**托管台（安装状态机 + 内嵌 Web 界面）**和默认图标；设计见 [docs/specs/proxy-bundler.md](docs/specs/proxy-bundler.md)。

## 它是怎么工作的

每个代理应用是一个独立 fpk：

- 包内自带一个静态 Go 反向代理（`internal/proxy`）和生成时烤入的 `config.json`；
- 入口是独立 TCP 端口（20000–29999，托管台生成时分配）：manifest `service_port` + 桌面入口 `port`，代理监听 `0.0.0.0:<port>`，整站位于该端口根路径 `/`；
- 反代模式下整站转发；直连模式下入口返回 302，浏览器随后直接访问目标；
- 默认拒绝回环/链路本地/组播目标，按需可开 `allow_loopback`；
- 可选 HTTP Basic Auth（仅反代），口令以 bcrypt cost 12 哈希写入包内。

## 构建一个代理包

需要 Go 1.27+。仓库根目录：

```bash
# 1. 交叉编译静态 linux/amd64 代理
CGO_ENABLED=0 GOOS=linux GOARCH=amd64 \
  go build -trimpath -ldflags "-s -w -buildid=" \
  -o dist/proxy-linux-amd64 ./cmd/proxy

# 2. 生成并自检 fpk
go run ./cmd/build-fpk \
  --appname daili.demo \
  --display-name "示例代理" \
  --target http://192.168.1.10:8123 \
  --port 20001 \
  --proxy-bin dist/proxy-linux-amd64 \
  --out dist/daili.demo-1.0.0.fpk
```

也可以直接 `make sample`（等价的两步），或省略 `--proxy-bin` 让工具自己交叉编译。

常用参数：

| 参数 | 说明 |
| --- | --- |
| `--appname` | 应用标识，3–32 字节，`^[A-Za-z0-9][A-Za-z0-9._-]*$`，安装后不可改 |
| `--display-name` | 飞牛桌面显示名，1–32 字符 |
| `--target` | 目标 origin，如 `http://192.168.1.10:8123`；只接受 scheme://host[:port] |
| `--port` | 入口 TCP 端口，20000–29999；托管台会自动分配，手动构建时必填 |
| `--mode` | `reverse`（默认）或 `direct` |
| `--platform` | `x86`（linux/amd64）或 `arm`（linux/arm64） |
| `--all-users` | 入口对所有用户可见（默认仅管理员） |
| `--basic-auth-user` / `--basic-auth-hash` | 可选 Basic Auth，哈希需 bcrypt cost 12 |
| `--skip-tls-verify` | 反代时不校验目标 TLS 证书 |
| `--allow-loopback` | 允许反代到本机回环地址 |
| `--out` | 输出路径，默认 `dist/<appname>-<version>.fpk` |

生成器会先构建到内存、跑 `internal/fpk.Verify` 做结构自检，再用原子替换写到 `--out`；同一个 spec 重复生成 SHA256 不变。

## 构建托管台

托管台是 `daili.manager`，入口走 CGI，进程只监听 `$TRIM_PKGVAR/manager.sock`；它内嵌 Web 界面、状态文件与代理二进制，负责生成/安装/升级/卸载代理应用。

```bash
make manager
# 产物：dist/daili.manager-1.0.0.fpk
```

等价地，也可以手动交叉编译 `./cmd/manager` 与 `./cmd/proxy`，再运行：

```bash
go run ./cmd/build-manager \
  --manager-bin dist/manager-linux-amd64 \
  --proxy-bin dist/proxy-linux-amd64 \
  --out dist/daili.manager-1.0.0.fpk
```

托管台的 `config/privilege` 是 `run-as root`，因为它要调用 `appcenter-cli`；代理应用仍是 `run-as package`。

## 安装到飞牛

先装托管台，再从它的界面建代理；也可以手工装任意代理包。把 fpk 传到 NAS，以 root 执行：

```bash
sudo appcenter-cli install-fpk -v 1 /path/to/daili.manager-1.0.0.fpk
sudo appcenter-cli list
```

安装成功的标志是输出含 `Installation complete`，且 `list` 中状态变为 `running`。`install-local` 在已验证的 fnOS 版本上不可用（10237 + panic），不要使用。

## 用托管台建代理

打开飞牛桌面上的“代理打包器”图标（CGI 入口，也可直接访问
`https://<NAS>/cgi/ThirdParty/daili.manager/index.cgi/`），点“新建代理”：

1. 填目标 origin（如 `http://192.168.123.200:8021`），可点“测试连通”先探一次 TCP；
2. 填显示名称，appname 会按设置里的全局前后缀自动建议，可改；可上传应用图标（PNG/JPEG，≥64×64，≤2MB），也可点“从站点获取”按需抓取目标的 favicon（ICO 亦可）；
3. 选模式（反向代理 / 直连 302）与可见性，端口由托管台从 20000–29999 分配；
4. 点“生成并安装”，托管台会打包、调用 `appcenter-cli install-fpk` 并等它 `running`，成功后自动回列表。

列表里的入口可直接点开：

- 局域网/自定义域名：`http://<NAS>:<端口>/`；
- 经 FN Connect 公网访问：`https://<appname 点→连字符>-main.<域名>/`
  （需先在 FN Connect 里发布该应用，规则见 [docs/research/fnos-fnconnect-entry-url.md](docs/research/fnos-fnconnect-entry-url.md)）。

编辑已有应用会从第 1 步开始，目标地址可改；保存即重新打包并重装，appname 与入口端口保持不变。

列表工具栏的“批量导入”可以一次装一批：选一个 JSON 文件或直接粘贴，先“预览”看哪些会新建、哪些被跳过（appname 冲突、字段不合法、文件内重复），确认后“开始导入”。导入会逐个探测目标、逐个安装，单条失败不影响其余条目；不可达的目标只建为草稿、不安装。JSON 字段与新建表单一致，示例见 [docs/examples/import-targets.json](docs/examples/import-targets.json)。

图标写在 `icon` 字段里，两种写法都行：

```json
{"appname": "proxy.ha", "icon": "/vol1/1000/icons/home-assistant.png"}
{"appname": "proxy.ha", "icon": "data:image/png;base64,iVBORw0KGgo..."}
```

路径必须是 NAS 上的绝对路径（文件超过 2MiB 会拒绝），base64 可以直接从别处复制 data URL。

## 备份与还原

导出的 JSON 就是这个托管台的备份：换了机器、或者重装了 `daili.manager`，把这份文件导回去即可。

工具栏“导出全部”下载一个包含所有目标的 JSON（连带全局设置：appname 前后缀、默认 DNS），每行的“导出”只下载这一条。两者默认把自定义图标内嵌成 base64。注意导出的 `basic_auth` 带 bcrypt 口令哈希（否则导回后口令就丢了），按敏感文件保管。

还原时有三件事会自动处理：

- **已装应用会被接管**：如果代理应用还在、托管台却认不出它（state 丢了），导入会显示“接管并重装”——先把那个应用卸掉，再用备份的配置重新装上，而不是跳过。预览里逐行标出，不会静默接管。
- **端口尽量沿用**：备份里的 `port` 是偏好值，空闲就沿用（入口地址不变），被占用才在 20000–29999 里另分配。
- **全局设置可选还原**：勾选“同时还原全局设置”才用文件里的前后缀与默认 DNS 覆盖当前设置。

真机部署路径、两个历史故障（空白窗口 / CGI 500）与排障顺序见
[docs/research/manager-deployment.md](docs/research/manager-deployment.md)。

## 仓库结构

```text
cmd/proxy/          代理可执行文件（serve / cgi 两种模式）
cmd/manager/        托管台可执行文件（serve / cgi 两种模式）
cmd/build-fpk/      单包生成 CLI
cmd/build-manager/  托管台包生成 CLI
internal/fpk/       fpk 构建与结构校验（代理包 + 托管台包）
internal/proxy/     反代、CGI 桥、Basic Auth、Location/Cookie 改写
internal/proxyconfig/ 包内 config.json 的解析与校验
internal/manager/   托管台服务、API、CSRF、安装状态机
internal/state/     state.json 原子读写
internal/cli/       appcenter-cli 封装与输出解析
internal/guard/     出站目标拦截
internal/spec/      ProxySpec 与校验
internal/target/    target origin 规范化
internal/assets/    内嵌默认图标
tools/gen-icons/    重新生成默认图标
docs/examples/      批量导入示例 JSON
docs/specs/         实施规格
docs/research/      调研笔记与真机验证记录
```

## 验证

```bash
go test ./...
go vet ./...
```

真机安装链的实测记录见 [docs/research/install-chain-verification.md](docs/research/install-chain-verification.md)。
