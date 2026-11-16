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
docs/specs/         实施规格
docs/research/      调研笔记与真机验证记录
```

## 验证

```bash
go test ./...
go vet ./...
```

真机安装链的实测记录见 [docs/research/install-chain-verification.md](docs/research/install-chain-verification.md)。
