# 代理打包器实施规格

- 状态：可开工（implementation-ready）
- 日期：2026-11-16
- 地图：[#1 代理打包器](https://github.com/jeefyang/fnos_daili/issues/1)
- 决议来源：T1 #7、T2 #4、T3 #9、T4 #6、T5 #3、V1 #10
- 事实来源：docs/research/watchcow-fpk-generation.md、docs/research/fnos-fpk-and-embedding.md、docs/research/install-chain-verification.md、docs/research/watchcow-cgi-entry.md、docs/research/gateway-verification.md

本规格把已经拍板的决议固化成实现契约。实现会话按本文件开工，不需要再回头问“为什么”。

## 0. 一句话架构

一个装在飞牛上的 **托管台**（appname 固定为 `daili.manager`，run-as root，只监听 Unix socket，入口走 `index.cgi`），提供四步向导；用户填写目标 `scheme://host[:port]`、显示名、appname 等信息后，托管台在本地生成一个**独立的代理 fpk**（run-as package，内嵌通用反代二进制和烤入的 config.json），通过 `appcenter-cli install-fpk -v <volume> <fpk>` 安装；代理应用监听一个独立 TCP 端口（20000–29999），入口直接指向 `http://<fnos-host>:<port>/`，不经过 CGI。

## 1. 范围与非目标

### 1.1 v1 必须做到

- 任意 HTTP(S) 的 `scheme://host[:port]` 目标；host 可以是 IPv4、域名、IPv6 字面量。
- 新建、编辑、删除、重装、升级、回滚一个代理应用。
- 默认反代模式；可选直连模式（302）。
- 托管台界面完成四步：目标 → 应用信息 → 入口与高级 → 生成并安装。
- 生成物可下载；安装失败可重试；升级失败可用上次可用包回滚。
- 托管台状态持久化，重启后与 `appcenter-cli list` 对账。
- 代理默认仅管理员可见；可选 HTTP Basic Auth；反代目标默认拦截回环/链路本地等地址。
- 托管台入口走 CGI；代理应用入口是独立 TCP 端口（20000–29999），整站位于该端口根路径。

### 1.2 v1 明确不做

- Docker/Compose 集成、watchcow label 兼容、容器生命周期同步。
- 非 HTTP(S) 协议（SSH/RDP/裸 TCP）。
- 统一网关 `/app/<appname>` 入口（未验证可用，不再是产品入口）。
- HTML 响应体重写、绝对 URL 改写、CSP 改写。
- 自定义图标上传/抓 favicon；v1 只用内置默认 64/256 图标。
- 批量导入导出、模板、多目标一次生成。
- 实时健康检查、访问统计、自动失败重启。
- 托管台自身的 WebSocket（CGI 不支持 Upgrade）；代理应用在独立端口上支持 WebSocket。
- 公网发布、DDNS、证书申请、企业 SSO/OIDC/多租户。

## 2. 术语与不变量

术语以 GLOSSARY.md 为准；下面是实现必须守住的不变量。

1. 一个代理应用 = 一个目标 + 一个 appname + 一个入口。反代/直连在创建时二选一。
2. appname 是包身份；首次安装后不可更改。改名 = 删除后新建。
3. 包内 `config.json` 是运行时唯一配置源；托管台状态是编辑源。任何字段变化 = 重新生成 + 重装。
4. 代理入口使用独立 TCP 端口，范围 `20000–29999`；托管台入口不使用 TCP 端口。
5. 代理入口 URL 是 `http://<fnos-host>:<port>/`；不是目标地址，也不带 CGI 前缀。
6. 托管台进程只监听 Unix socket；不监听 TCP；不信任任何客户端提供的 `X-Trim-*` 管理员头。
7. 安装/卸载/启动/停止/状态查询全部通过 `appcenter-cli`；`install-fpk` 退出码 0 不等于成功，必须看输出标记和 `list/status`。
8. 代理应用 run-as package；托管台 run-as root（只有它需要调 CLI）。
9. fpk 的 `install_type` 留空，由 `-v <volume>` 选择安装卷；不再写 `install_type=root`（V1 实测空值 + `-v` 可用；T1 的该行作废）。
10. 不在日志、错误页、API 响应里出现目标上游细节、认证口令、Cookie、Authorization。

## 3. 运行时拓扑

### 3.1 托管台

- appname：`daili.manager`（保留名，用户不能给代理应用起这个名）。
- 入口：`daili.manager.main`，`url=/cgi/ThirdParty/daili.manager/index.cgi/`，`type=iframe`，`all_users=false`，`no_display=false`。
- 进程：`${TRIM_APPDEST}/bin/manager`。
- 模式：
  - `--mode serve`（默认）：常驻，监听 `${TRIM_PKGVAR}/manager.sock`，权限 0600。
  - `--mode cgi`：由 `${TRIM_APPDEST}/ui/index.cgi` 调用，把 CGI 请求反向代理到 manager.sock。
- 数据目录：`${TRIM_PKGVAR}`，权限 0700；state、packages、build、日志、CSRF token 都在这里。
- 不监听任何 TCP 端口。

### 3.2 代理应用

- appname：用户填写，3–32 位，正则 `^[A-Za-z0-9][A-Za-z0-9._-]*$`。
- 入口：`<appname>.main`，`url=/`，`port=<port>`，`type=url`（默认）或 `iframe`，`all_users=false`（默认，可改 true），`no_display=false`（默认，可改 true）。
- 端口：托管台生成时从 `20000–29999` 分配，写 manifest `service_port` 与 `ui/config.port`；首次分配后不可改，改端口 = 删除后新建。
- 进程：`${TRIM_APPDEST}/bin/proxy`，run-as package。
- 模式：`--mode serve --listen 0.0.0.0:<port>`：常驻，监听独立 TCP 端口；HTTP/1.1 与 WebSocket Upgrade 都支持。
- 配置：安装时把 `${TRIM_APPDEST}/config.json` 复制到 `${TRIM_PKGVAR}/config.json`（0600）；守护进程读后者，缺失时回退前者。
- 端口不进 `config.json`；运行时以启动参数的 `--listen` 为准。

### 3.3 请求路径

```
浏览器
  -> http://<fnos-host>:<port>/<target-path>?<query>
  -> proxy --mode serve 反代到目标（reverse）或返回 302（direct）
```

飞牛桌面是 HTTPS，点击代理入口是顶层导航到 HTTP 端口，不属于混合内容，也不会被拦截。代理端口是独立 origin，目标页面的绝对路径 `/static/app.js` 仍落在该端口根下，因此无需 HTML/绝对 URL 改写。

## 4. 数据模型

### 4.1 TargetSpec（托管台）

```go
type TargetSpec struct {
    ID             string     `json:"id"`
    AppName        string     `json:"appname"`
    DisplayName    string     `json:"display_name"`
    Description    string     `json:"description"`
    Version        string     `json:"version"`        // 默认 1.0.0
    TargetURL      string     `json:"target_url"`     // 规范化 origin
    Mode           string     `json:"mode"`           // reverse | direct
    EntryTitle     string     `json:"entry_title"`
    UIType         string     `json:"ui_type"`        // url | iframe
    Port           int        `json:"port"`           // 20000-29999，托管台分配
    AllUsers       bool       `json:"all_users"`      // 默认 false
    NoDisplay      bool       `json:"no_display"`     // 默认 false
    FileTypes      []string   `json:"file_types"`     // 默认空
    InstallVolume  string     `json:"install_volume"` // 默认 default-volume
    SkipTLSVerify  bool       `json:"skip_tls_verify"`// 仅 reverse + https
    AllowLoopback  bool       `json:"allow_loopback"` // 仅 reverse
    BasicAuth      *BasicAuth `json:"basic_auth,omitempty"`
}

type BasicAuth struct {
    Username     string `json:"username"`
    PasswordHash string `json:"password_hash"` // bcrypt cost 12；API 不返回
}
```

字段规则：

| 字段 | 规则 |
|---|---|
| `appname` | 3–32 字节；`^[A-Za-z0-9][A-Za-z0-9._-]*$`；不能是 `daili.manager`；首次安装后锁定 |
| `display_name` | 1–32 个 Unicode 字符，不能含换行 |
| `description` | 0–120 个 Unicode 字符，不能含换行；写 manifest 时转义 |
| `version` | `^[0-9]+\.[0-9]+\.[0-9]+([-.][0-9A-Za-z.-]+)?$`，最长 32；默认 `1.0.0` |
| `entry_title` | 1–32 个 Unicode 字符；默认等于 display_name |
| `ui_type` | `url` 或 `iframe`；默认 `url` |
| `port` | `20000–29999`；托管台创建时分配，之后不可改；改端口 = 删除后新建 |
| `install_volume` | `^[A-Za-z0-9._-]+$`；默认取 `appcenter-cli default-volume`，失败回退 `1` |
| `target_url` | 见 §4.2；只接受 origin |
| `mode` | `reverse` 或 `direct`；默认 reverse |
| `basic_auth` | 仅 reverse；用户名 1–64 字节，密码 8–72 字节；direct 下出现即校验失败 |
| `skip_tls_verify` | 仅 reverse + https 有意义；http 下忽略 |
| `allow_loopback` | 仅 reverse；打开只放行回环，不放行链路本地/多播/端口拒绝 |

### 4.2 目标规范化

输入先 trim。没有 scheme 时补 `http://`。解析后必须满足：

- scheme 只允许 `http` / `https`；
- 必须有 host；不能有 userinfo；
- 不能有 path（允许空或 `/`）、query、fragment；
- 端口 1–65535；省略时 http=80、https=443；
- IPv4 用点分十进制；IPv6 必须用方括号字面量；拒绝 zone（`%`）；
- 域名只接受 ASCII 标签（含 `xn--`），不接受下划线；末尾点可去掉；
- 规范化：scheme/host 小写；去掉默认端口；输出 `scheme://host[:port]`。

示例：

| 输入 | 规范化 |
|---|---|
| `192.168.1.10:8123` | `http://192.168.1.10:8123` |
| `https://Example.COM` | `https://example.com` |
| `http://[fd00::1]:8080/` | `http://[fd00::1]:8080` |
| `http://host/path` | 拒绝（有 path） |

### 4.3 代理 config.json

代理包内 `app/config.json`，安装时复制到 PKGVAR：

```json
{
  "schema": 1,
  "appname": "proxy.home.9f3a",
  "mode": "reverse",
  "target": "http://192.168.1.10:8123",
  "tls": { "skip_verify": false },
  "guard": { "allow_loopback": false },
  "basic_auth": {
    "username": "home",
    "password_hash": "$2a$12$....................................................."
  },
  "timeouts": {
    "dial": "10s",
    "tls_handshake": "10s",
    "response_header": "30s",
    "idle": "90s"
  }
}
```

- `schema`、`appname`、`mode`、`target` 必填；`appname` 只作身份标识，不再拼入口前缀；`mode` 只允许 `reverse`/`direct`。
- 端口不写进 `config.json`；由 `cmd/main` 的 `--listen` 传入，值来自生成时的分配。
- `basic_auth` 只在 reverse 出现；direct 出现即启动失败。
- `timeouts` 缺省用上面的值；解析失败即启动失败。
- 未知键忽略（向前兼容）。
- direct 模式忽略 `tls`/`guard`。
- 文件权限 0600。

### 4.4 托管台 state.json

路径 `${TRIM_PKGVAR}/state.json`，原子写：同目录临时文件 → `fsync` → `rename` → `chmod 0600`。

```json
{
  "schema": 1,
  "targets": [
    {
      "spec": {
        "id": "01J...",
        "appname": "proxy.home.9f3a",
        "display_name": "家里的 Home Assistant",
        "description": "通过代理访问 http://192.168.1.10:8123",
        "version": "1.0.0",
        "target_url": "http://192.168.1.10:8123",
        "mode": "reverse",
        "entry_title": "家里的 Home Assistant",
        "ui_type": "url",
        "all_users": false,
        "no_display": false,
        "file_types": [],
        "install_volume": "1",
        "skip_tls_verify": false,
        "allow_loopback": false,
        "basic_auth": { "username": "home", "password_hash": "$2a$12$..." }
      },
      "runtime": {
        "state": "running",
        "installed_version": "1.0.0",
        "package_path": "/vol1/@appdata/daili.manager/packages/proxy.home.9f3a/1.0.0-1760000000.fpk",
        "last_known_good_path": "/vol1/@appdata/daili.manager/packages/proxy.home.9f3a/1.0.0-1760000000.fpk",
        "package_sha256": "...",
        "last_operation": {
          "id": "01J...",
          "kind": "install",
          "status": "succeeded",
          "step": "verify",
          "started_at": "2026-11-16T10:00:00Z",
          "finished_at": "2026-11-16T10:00:12Z",
          "error": ""
        },
        "last_error": "",
        "created_at": "2026-11-16T09:59:00Z",
        "updated_at": "2026-11-16T10:00:12Z"
      }
    }
  ]
}
```

`last_operation.kind`：`install`、`upgrade`、`reinstall`、`uninstall`、`rollback`、`start`。
`last_operation.status`：`running`、`succeeded`、`failed`、`interrupted`。
`runtime.state`：

| 状态 | 含义 |
|---|---|
| `draft` | 已保存，未安装 |
| `building` | 正在生成包 |
| `installing` | 已开始 install-fpk |
| `starting` | 应用中心显示 starting |
| `running` | list/status 确认 running |
| `installed_not_running` | 已安装但启动超时或 stopped |
| `uninstalling` | 正在卸载 |
| `failed` | 最近操作失败，包保留 |
| `broken` | 升级/回滚都失败，需人工处理 |
| `missing` | state 有记录，list 中不存在 |
| `uninstall_failed` | 删除时卸载失败，记录保留 |
| `unmanaged` | list 中存在、state 无记录，但 packages 目录有同名包；只读展示 |

状态机：

```
draft -> building -> installing -> starting -> running
                                  \-> installed_not_running
building/installing/starting -> failed
running -> uninstalling -> (删除记录) | uninstall_failed
running -> building (upgrade) -> ... -> running | failed -> rollback -> running | broken
running/installed_not_running -> missing (对账)
```

## 5. fpk 包结构

### 5.1 通用外壳

每个 fpk 是 gzip 压缩的 tar，包含：

```
manifest
ICON.PNG                 # 64x64 RGBA
ICON_256.PNG             # 256x256 RGBA
config/privilege         # JSON
config/resource          # JSON
app.tgz                  # 整个 app/ 目录的 gzip tar，根下直接是 ui/、bin/ 等
cmd/main                 # 0755
cmd/install_init         # 0755
cmd/install_callback     # 0755
cmd/uninstall_init       # 0755
cmd/uninstall_callback   # 0755
cmd/upgrade_init         # 0755
cmd/upgrade_callback     # 0755
cmd/config_init          # 0755
cmd/config_callback      # 0755
wizard/                  # 空目录，fnpack 要求
```

- 不写 `checksum` 以外的未知 manifest 字段。
- `checksum` = `md5(app.tgz)` 小写十六进制；由生成器写入。
- `app.tgz` 的根是 `app/` 的内容，**不是** `app/` 本身。即 `ui/config`、`bin/proxy`、`config.json`。
- `app.tgz` 内文件权限：二进制 0755，config.json 0600，ui 文件 0644，目录 0755。
- 外层 fpk 权限 0600；`manifest` 0644；`cmd/*` 0755。
- 所有路径用正斜杠；禁止 `..`、绝对路径、符号链接。

### 5.2 托管台包

```
app/
  ui/config
  ui/images/icon_64.png
  ui/images/icon_256.png
  bin/manager
```

- `bin/manager` 内嵌 Web 静态资源、代理二进制（`proxy-linux-amd64` 或 `proxy-linux-arm64`）、默认图标。
- 托管台不需要 `app/config.json`。
- `ui/index.cgi` 不在包里，由 `cmd/install_callback` 写入。
- `config/privilege`：`{"defaults":{"run-as":"root"}}`。
- `config/resource`：`{}`。
- manifest：

```ini
appname=daili.manager
version=1.0.0
display_name=代理打包器
desc=为任意 HTTP(S) 目标生成并安装代理应用
platform=x86
source=thirdparty
maintainer=fnos_daili
distributor=fnos_daili
os_min_version=0.9.0
desktop_uidir=ui
desktop_applaunchname=daili.manager.main
ctl_stop=true
checkport=false
checksum=<md5(app.tgz)>
```

- `ctl_stop=true`：托管台是没有 `service_port` 的守护型应用，应用中心据此提供启停并调用 `cmd/main`。
- `cmd/install_callback` 只写 `ui/index.cgi`；shim 在每次请求时检查 `manager.sock`，不存在就 `nohup manager --mode serve` 拉起守护进程，再 exec `--mode cgi`。这样即使 appcenter 没有自动 start，第一次打开界面也能用；`manager serve` 发现 socket 已有活进程时直接退出 0，不产生第二实例。
- 不写 `service_port`、`install_type`、`arch`。
- `platform` 由构建目标决定：amd64→`x86`，arm64→`arm`。v1 只发 x86 包；ARM 构建同流程另编。

### 5.3 代理包

```
app/
  ui/config
  ui/images/icon_64.png
  ui/images/icon_256.png
  bin/proxy
  config.json
```

- `config/privilege`：内容为 `run-as: package`，但必须按 fnOS 模板写成四空格缩进的多行 JSON，不能写成 minified：
  ```json
  {
      "defaults":
      {
          "run-as": "package"
      }
  }
  ```
  生成器与该文件的逐字节校验都以这个形状为准；社区有 minified 被应用中心拒绝的记录。
- `config/resource`：`{}`（单行，无尾随换行）。
- manifest 样例（`appname=proxy.home.9f3a`，`display_name=家里的 Home Assistant`，目标 `http://192.168.1.10:8123`）：

```ini
appname=proxy.home.9f3a
version=1.0.0
display_name=家里的 Home Assistant
desc=通过代理访问 http://192.168.1.10:8123
platform=x86
source=thirdparty
maintainer=fnos_daili
distributor=fnos_daili
os_min_version=0.9.0
desktop_uidir=ui
desktop_applaunchname=proxy.home.9f3a.main
service_port=20001
checkport=false
checksum=<md5(app.tgz)>
```

- 写 `service_port=<port>`；`port` 必须是 20000–29999。
- 不写 `install_type`、`arch`。
- `desc` 不能含换行；如果用户描述有换行，替换为空格。
- 入口 `url` 固定 `/`，`port` 写十进制端口字符串。
- 图标：v1 使用内置默认 64/256；`ui/config` 的 `icon` 固定 `images/icon_{0}.png`，`{0}` 原样写，由飞牛按 DPI 代入 64/256。

### 5.4 app/ui/config

托管台：

```json
{
  ".url": {
    "daili.manager.main": {
      "title": "代理打包器",
      "icon": "images/icon_{0}.png",
      "type": "iframe",
      "protocol": "http",
      "port": "",
      "url": "/cgi/ThirdParty/daili.manager/index.cgi/",
      "allUsers": false,
      "noDisplay": false
    }
  }
}
```

代理：

```json
{
  ".url": {
    "proxy.home.9f3a.main": {
      "title": "家里的 Home Assistant",
      "icon": "images/icon_{0}.png",
      "type": "url",
      "protocol": "http",
      "port": "20001",
      "url": "/",
      "allUsers": false,
      "noDisplay": false
    }
  }
}
```

- 入口 key 必须等于 `desktop_applaunchname`，否则 fnpack/应用中心可能拒绝；V1 证明应用中心不校验跨文件一致，所以生成器必须自己校验。
- 代理入口 `port` 必须等于 manifest 的 `service_port`；`url` 为 `/`。托管台入口 `port` 为空串、`url` 为 CGI 路径。
- `protocol` 写 `http`：代理端口不启 TLS；飞牛桌面 HTTPS 跳 HTTP 是顶层导航。
- `allUsers` 默认 false；`noDisplay` 默认 false。
- 只有 `file_types` 非空时才写 `fileTypes` 数组；默认不写。
- 不写 `control`（v1 不暴露应用控制权限）。

### 5.5 cmd 脚本

所有脚本用 `#!/bin/bash`，`set -u`，不依赖外部工具（除 `kill`/`sleep`/`mkdir`/`chmod`/`rm`/`seq`/`cat`/`cp`/`nohup`）。失败退出码非 0；stdout/stderr 写应用中心日志。

`cmd/main`（代理版；manager 版换成 `daili.manager`/`manager`/Unix socket，不监听 TCP）：

```bash
#!/bin/bash
set -u
APP="proxy.home.9f3a"
PORT="20001"
DESK="${TRIM_APPDEST:-/tmp/$APP}"
VAR="${TRIM_PKGVAR:-/tmp/$APP-var}"
PID="$VAR/$APP.pid"
LOG="$VAR/$APP.log"

start() {
  mkdir -p "$VAR" || exit 1
  chmod 700 "$VAR"
  if [ -f "$PID" ] && kill -0 "$(cat "$PID")" 2>/dev/null; then
    exit 0
  fi
  rm -f "$PID"
  if [ -f "$DESK/config.json" ]; then
    cp -f "$DESK/config.json" "$VAR/config.json"
    chmod 600 "$VAR/config.json"
  fi
  nohup "$DESK/bin/proxy" --mode serve --listen "0.0.0.0:$PORT" --config "$VAR/config.json" \
    >>"$LOG" 2>&1 &
  echo $! >"$PID"
  chmod 600 "$PID"
  for _ in $(seq 1 50); do
    if ! kill -0 "$(cat "$PID")" 2>/dev/null; then break; fi
    if (echo >"/dev/tcp/127.0.0.1/$PORT") 2>/dev/null; then exit 0; fi
    sleep 0.1
  done
  exit 1
}

stop() {
  if [ -f "$PID" ]; then
    kill "$(cat "$PID")" 2>/dev/null || true
    for _ in $(seq 1 50); do
      kill -0 "$(cat "$PID")" 2>/dev/null || break
      sleep 0.1
    done
    kill -9 "$(cat "$PID")" 2>/dev/null || true
    rm -f "$PID"
  fi
  exit 0
}

status() {
  if [ -f "$PID" ] && kill -0 "$(cat "$PID")" 2>/dev/null; then
    exit 0
  fi
  exit 3
}

case "${1:-}" in
  start) start ;;
  stop) stop ;;
  status) status ;;
  *) exit 1 ;;
esac
```

- 端口在生成时写死进 `cmd/main`；运行时以 manifest `service_port` 为准由应用中心分配，二者必须一致。
- `start` 用 pid 判活，再以 TCP 连接探测端口就绪；`status` 只按 pid，返回 0/3。
- 日志追加写；单文件超过 5MB 由 manager/proxy 自己轮转（保留 3 份）。

`cmd/install_callback`：

- 代理包：`#!/bin/bash` + `set -u` + `exit 0`；不再写 `ui/index.cgi`。
- 托管台包：写 `ui/index.cgi`，exec `${TRIM_APPDEST}/bin/manager --mode cgi --socket ${TRIM_PKGVAR}/manager.sock`，并 `chmod 755`。

`install_init`、`uninstall_init`、`uninstall_callback`、`upgrade_init`、`upgrade_callback`、`config_init`、`config_callback` 一律 `exit 0`，不删除用户数据；卸载时 PKGVAR 由应用中心清理。

注意：托管台的 `ui/index.cgi` 不在 `app.tgz` 里，必须由 `install_callback` 写入；升级后 `install_callback` 会重写，保证路径与当前包一致。

## 6. CGI 契约（托管台）

代理应用不走 CGI；本节只描述托管台。`cmd/manager --mode cgi` 与通用 `proxy --mode cgi --socket <path>` 共用同一套 handler：

1. 用 `net/http/cgi.Serve` 包一个 `http.Handler`。
2. Handler 在 `r.URL.Path` 中找第一个 `/index.cgi`，要求其后为空或以 `/` 开头；截掉包括 `index.cgi` 在内的前缀；剩余为空时补 `/`。这样既接受 `/index.cgi/...`，也接受 fnOS 实际的 `/cgi/ThirdParty/<appname>/index.cgi/...`。
3. 把请求体原样流式转发到 Unix socket；不预读、不限 body 大小。
4. 连接 socket 超时 5s；失败返回 503 通用页，带 `X-Request-Id`。
5. 如果 `Upgrade` 头非空或 `Connection` 含 `upgrade`：返回 501，不尝试转发。
6. 响应头原样回传（反代层已处理 Location/Cookie/安全头）；CGI 层不额外加 `Content-Length`。
7. `REMOTE_ADDR` 由 CGI 提供；反代层用它填 `X-Forwarded-For` 的第一个值。
8. 不读取 `X-Trim-*` 作为身份；CGI 层只负责转发。

托管台的 socket 是 `manager.sock`；501 换成普通 502（托管台不需要 WS）。代理应用在独立 TCP 端口上处理 Upgrade，不走本节。

## 7. 代理运行时

### 7.1 启动校验

`proxy --mode serve` 启动时：

- 读 `--config`，缺省 `$TRIM_PKGVAR/config.json`，再缺省 `$TRIM_APPDEST/config.json`；权限必须 0600，否则拒绝启动。
- 校验 `schema=1`、`mode`、`target`；direct 模式下出现 `basic_auth` 拒绝启动。
- 解析 target；执行一次目标拦截检查（DNS 失败只记 warning，不阻止启动；混合结果拒绝）。
- 以 `--listen 0.0.0.0:<port>` 监听独立 TCP 端口；端口来自生成时的分配，启动失败即退出非 0。
- 写日志 `$TRIM_PKGVAR/proxy.log`（0600），包含启动、配置摘要（不含口令/哈希）、监听地址。

### 7.2 reverse 模式

使用 `httputil.ReverseProxy`，不用 `NewSingleHostReverseProxy` 的默认 Director；用 `Rewrite`：

1. 复制入站 URL；scheme/host 换成 target；path/query 原样。
2. `out.Host = target.Host`（Host 与 SNI 都是目标）。
3. 清空客户端传入的 `Forwarded`、`X-Forwarded-For`、`X-Forwarded-Host`、`X-Forwarded-Proto`。
4. 设 `X-Forwarded-For` = 客户端 IP（TCP 连接的 `RemoteAddr`）；`X-Forwarded-Host` = 入口 Host；`X-Forwarded-Proto` = 入口是 HTTPS 则 `https`，否则 `http`。
5. 删除 hop-by-hop 头：`Connection` 列出的每个头、`Proxy-Connection`、`Keep-Alive`、`Proxy-Authenticate`、`Proxy-Authorization`、`Te`、`Trailer`、`Transfer-Encoding`、`Upgrade`。
6. `Transport`：
   - `DialContext` 先做目标拦截（§7.5），再拨号；超时 10s。
   - `TLSHandshakeTimeout=10s`；`ResponseHeaderTimeout=30s`；`IdleConnTimeout=90s`；`ForceAttemptHTTP2=true`；`DisableCompression=true`。
   - TLS 默认校验系统根；`skip_verify=true` 时 `InsecureSkipVerify=true`，只对 https 目标生效。
7. `FlushInterval=-1`（每次写后 flush）；`ErrorHandler` 返回 502 通用页；`ErrorLog` 丢弃到自定义 logger。
8. 不设总超时；不限制请求/响应 body；不二次压缩；`WriteTimeout=0`，`ReadHeaderTimeout=10s`，`IdleTimeout=90s`。
9. `ModifyResponse`：
   - `Location`：解析；相对路径原样；绝对/协议相对且 origin 等于 target 时，改写为 `entryPrefix + path + query + fragment`；其他 origin 原样。
   - `Set-Cookie`：删除 `Domain`；路径按 §7.3 加前缀；保留 `Secure`/`HttpOnly`/`SameSite`。
   - 删除 `Strict-Transport-Security`、`Alt-Svc`、`Public-Key-Pins`、`Public-Key-Pins-Report-Only`。
   - 请求头 `Origin` 等于入口 origin 时改为 target origin；`Referer` 等于入口 origin 或以其开头时，把前缀换成 target origin。
10. 不重写 HTML/CSS/JS 响应体；不处理 `Content-Security-Policy`。

### 7.3 入口根路径与 Cookie Path

独立端口下 `entryPrefix = ""`，入口 origin 就是 `http://<fnos-host>:<port>`。

- Cookie `Path` 为空或 `/`：写 `/`。
- Cookie `Path` 以 `/` 开头：原样保留。
- 不合法的相对 Path：写 `/`。
- 永不加 CGI 前缀；托管台的 CGI 层仍按其自己的 `/cgi/ThirdParty/daili.manager/index.cgi` 处理（见 §8.1），二者不共用前缀。

### 7.4 direct 模式

- 收到请求后返回 `302 Found`。
- `Location = target + cleanPath(r.URL.Path) + "?" + r.URL.RawQuery`；query 为空不加 `?`。
- `cleanPath` 用 `path.Clean("/"+p)`，禁止 `//` 开头和 `..` 逃逸；不做 HTML 输出。
- 响应体为空；加 `Cache-Control: no-store`。
- 不套用目标拦截（浏览器直连，NAS 不出站）；不启用 Basic Auth（配置校验已拒绝）。
- 目标不可达时就是浏览器错误页，代理不探测。

### 7.5 目标拦截

只作用于 reverse 模式的出站拨号。默认拒绝：

| 范围 | CIDR |
|---|---|
| IPv4 未指定 | `0.0.0.0/8` |
| IPv4 回环 | `127.0.0.0/8` |
| IPv4 链路本地 | `169.254.0.0/16` |
| IPv4 多播 | `224.0.0.0/4` |
| IPv4 保留/广播 | `240.0.0.0/4`（含 255.255.255.255） |
| IPv6 未指定 | `::/128` |
| IPv6 回环 | `::1/128` |
| IPv6 链路本地 | `fe80::/10` |
| IPv6 多播 | `ff00::/8` |

放行：RFC1918（10/8、172.16/12、192.168/16）、IPv6 ULA（fc00::/7）、公网。

规则：

- IPv4-mapped IPv6 先归一化为 IPv4 再判断。
- 域名在创建时做一次 best-effort 解析（失败只警告）；每次拨号都重新解析；任一结果为拒绝地址即拒绝整个拨号（混合结果拒绝）。
- 端口拒绝：`20000–29999`、`5666`、`5667`；托管台当前不监听 TCP，未来若监听，其端口也加入拒绝表。
- `allow_loopback=true` 只额外放行 `127.0.0.0/8` 与 `::1/128`；链路本地/多播/未指定仍拒绝；端口拒绝仍生效。
- 拦截失败返回 502 通用页；日志只记 appname、request id、被拒类别（loopback/linklocal/port 等），不记解析出的 IP 列表到错误页。

### 7.6 Basic Auth

- 仅 reverse。配置里有 `basic_auth` 才启用。
- 在反代前校验：解析 `Authorization: Basic`；用户名常量时间比较；密码用 bcrypt 比较（cost 12）。
- 失败：401，`WWW-Authenticate: Basic realm="<appname>"`，空 body；日志只记 request id，不记用户名/口令/哈希。
- 成功：删除 `Authorization` 与 `Proxy-Authorization` 再转发。
- WebSocket 握手同样先校验 Basic Auth；失败返回 401，成功再交给 ReverseProxy 升级。
- 口令只以 bcrypt 哈希存 config.json 与 state.json；API 永不返回哈希。

### 7.7 错误页与日志

- 错误页固定 HTML，含状态码、一句通用说明、`X-Request-Id`；不含 target、上游地址、头、堆栈。
- 访问日志：时间、appname、request id、method、path（不含 query）、status、耗时、bytes；不记 body、Authorization、Cookie、Set-Cookie。
- 上游错误详情只写 `proxy.log`（0600）；日志轮转 5MB × 3。
- 请求 id：16 位十六进制，响应头 `X-Request-Id` 必带。

## 8. 托管台 API

### 8.1 认证与 CSRF

- 托管台只通过 CGI 访问；依赖飞牛登录态，`allUsers=false` 只隐藏图标，不是鉴权。
- 不信任 `X-Trim-Userid`/`X-Trim-Isadmin`/`X-Trim-Username`。
- 启动时生成/读取 `$TRIM_PKGVAR/csrf.token`（32 字节随机，hex，0600）。
- `GET /` 设置 cookie `daili_csrf`：`HttpOnly; SameSite=Strict; Path=/cgi/ThirdParty/daili.manager/index.cgi/`，HTTPS 时加 `Secure`；HTML 里 `<meta name="csrf-token" content="...">`。
- 所有非 GET 请求必须：`X-CSRF-Token` 等于 token；`Origin` 或 `Referer` 的 host 等于请求 Host；否则 403。
- API 不接受跨源；不加 CORS 头。
- 已知限制：知道入口 URL 的普通登录用户可访问托管台；v1 不额外做口令/角色。需要更强隔离时另开票。

### 8.2 端点

统一前缀 `/api`，JSON；错误：

```json
{"error":{"code":"validation_failed","message":"target_url: 只接受 origin"}}
```

| 方法 | 路径 | 说明 |
|---|---|---|
| GET | `/api/health` | `{ok, version, cli_path, cli_ok}` |
| GET | `/api/targets` | 列表，含 spec 摘要 + runtime |
| GET | `/api/targets/{id}` | 单个；不含 password_hash |
| POST | `/api/targets` | 创建 draft；返回 id |
| PUT | `/api/targets/{id}` | 更新；appname 不可改；已安装则标记“需重新应用” |
| POST | `/api/targets/{id}/apply` | 生成并安装/升级；body `{"kind":"install"|"upgrade"|"reinstall"}`，默认按状态推断 |
| POST | `/api/targets/{id}/delete` | 卸载并删记录；异步 |
| POST | `/api/targets/{id}/rollback` | 用 last-known-good 重装 |
| GET | `/api/targets/{id}/package` | 下载当前 fpk（0600，Content-Disposition） |
| GET | `/api/targets/{id}/log?lines=200` | 返回脱敏后的最近日志 |
| POST | `/api/targets/{id}/test-target` | 测试连通性；不阻断保存 |
| GET | `/api/appnames/check?appname=` | `{ok, reason}`；查 state + list |
| GET | `/api/volumes` | `appcenter-cli default-volume` + 常见卷 |
| GET | `/api/operations/{id}` | 异步操作进度 |

- `apply`/`delete`/`rollback` 立即返回 `202 {operation_id}`；进度写 state 的 `last_operation`，前端轮询。
- `test-target` 用同一目标拦截；返回 `{reachable, status, elapsed_ms, error}`；错误只给类别。
- `appnames/check` 规则：正则、保留名、state 冲突、`appcenter-cli list` 冲突。
- 下载包只允许 state 里记录的路径，防目录穿越。
- 日志接口按行读，最多 1000 行；脱敏 Authorization/Cookie/Set-Cookie/口令。

### 8.3 启动对账

1. 读 state；损坏则备份 `state.json.bad.<ts>` 并以空 state 启动（记日志，不自动删应用）。
2. 调 `appcenter-cli list`；失败只警告，列表标“状态未知”。
3. state 有、list 无：`missing`（若最近操作是 install 且未完成，则 `failed`）。
4. list 有、state 无：`unmanaged`，只读展示，可“纳管”或“卸载”。
5. `last_operation.status=running`：标 `interrupted`；按 list 的实际状态修正 runtime。
6. 不自动启动/卸载任何应用；只有用户操作才改。

## 9. 构建、安装与生命周期

### 9.1 生成器

- 纯 Go 实现 tar/gzip，不依赖 `fnpack`；生成后仍可用 `fnpack` 校验（可选）。已实现为 `internal/fpk.BuildProxy`，开发期用 `cmd/build-fpk` 调同一构建器；托管台后续直接复用，不另写打包逻辑。
- 流程：校验 spec → 建 `build/<uuid>/app/` → 写 config.json（0600）→ 复制图标/二进制 → 打 `app.tgz` → 算 md5 → 写 manifest → 写 cmd/config/wizard → 打外层 fpk（0600）→ 预检 → 移到 `packages/<appname>/<version>-<unix>.fpk`。
- 构建目录在 `$TRIM_PKGVAR/build/<uuid>/`，成功或失败后删除；不落 `/tmp`。
- 包保留当前 + 上一版；删除更旧；`last-known-good` 单独指针。
- 生成后做 §9.2 预检；预检失败不写 state 为 installing。

### 9.2 预检

必须逐项通过：

1. appname 正则/长度/保留名；与 state、list 无冲突。
2. display_name/desc/entry_title/version 长度与字符。
3. target 规范化；reverse 下目标拦截；direct 下无 basic_auth。
4. manifest 字段无换行/非法字符；`desktop_applaunchname == app/ui/config` 的唯一 key。
5. `app.tgz` 根下存在 `ui/config`、`ui/images/icon_64.png`、`ui/images/icon_256.png`、`bin/proxy`、`config.json`；无 `app/` 前缀；无符号链接/绝对路径/`..`。
6. `config/privilege`、`config/resource` 存在且 JSON 合法。
7. `cmd/` 九个文件存在且 0755。
8. `checksum` 等于 `md5(app.tgz)`。
9. 图标 64/256 PNG，RGBA，尺寸精确。
10. 包内 `config.json` 能被代理同一套解析器解析。
11. `service_port` 在 20000–29999；与 state 中其他 target 的端口不重复；生成前 bind 测试可用（分配时已 bind 过一次，预检再确认一次）；`ui/config.port` 与 manifest `service_port` 相等。

T2 的“端口 bind 预检”恢复：代理入口使用独立端口，分配与预检都要做。回收在删除 target 时完成；手工卸载的应用若仍留着 state，由对账标记 missing 并释放端口。

### 9.3 安装

1. 定位 CLI：`/usr/local/bin/appcenter-cli` → `/var/apps/appcenter/target/bin/appcenter-cli` → `/usr/bin/appcenter-cli` → `PATH`；必须 root（`os.Geteuid()==0`），否则 `cli_not_root`。
2. 已存在同名应用：
   - 属于本台且 `running`：按 upgrade/reinstall 先卸载；
   - `starting`：等待到 running 或 300s 超时；
   - `stopped`：可直接卸载；
   - 不属于本台：`appname_conflict`。
3. `appcenter-cli install-fpk -v <volume> <package>`，超时 60s；`cmd.Dir` 设为包目录；不传 `--force`/`--yes`。
4. 成功判定：exit 0 + stdout 含 `Installation complete`；否则解析 `[Error]...` 转可读错误；exit 0 但无标记也视为失败。
5. 安装后每 2s 调 `appcenter-cli list`，最多 300s：
   - running → 成功；
   - starting → 继续等；
   - stopped → `start <appname>` 一次，再等 60s；
   - 超时 → `installed_not_running`，保留包，允许重试/卸载。
6. `starting` 期间绝不执行 start/stop/uninstall；遇到 `10500` 视为“还没起来”，等待后重试一次。
7. 成功后更新 state：`running`、installed_version、package_path、sha256、last_known_good（首次或成功升级）。

### 9.4 卸载与删除

- `uninstall <appname>`，超时 60s；成功标记 `uninstall success`；然后 `list` 轮询最多 60s 确认消失。
- `starting` 时先等 running；`10500` 等 5s 重试一次；仍失败 → `uninstall_failed`。
- 删除目标两段式：先卸载；成功且 list 消失才删 state；失败保留记录与包，状态 `uninstall_failed`。
- 删除成功后 best-effort 删包目录与日志；不删用户其他数据。

### 9.5 升级与回滚

- 升级 = 生成新包 → 预检 → 等当前 running → 卸载 → 安装新包 → 轮询。
- 安装失败：用 `last-known-good` 包回滚（同样卸载→安装→轮询）；回滚成功 → `failed` + 旧版本运行；回滚失败 → `broken`。
- 新包成功后才把 `last-known-good` 指向新包；旧包保留一版。
- 版本号默认 1.0.0，用户可改；不自动 bump；相同版本允许重装（reinstall）。

### 9.6 锁与并发

- 每 target 一把进程内互斥锁；所有 CLI 变更操作共享一个全局信号量（容量 1）。
- 操作开始写 `last_operation {id, kind, status:running, started_at}`；结束写 succeeded/failed。
- 启动时若发现 running 且超过 10 分钟，标 interrupted 并按 list 对账；不超过 10 分钟也标 interrupted（进程已重启，不可能还在跑），但保留 started_at 供诊断。
- 读操作（list/status/log）不拿全局锁，可并行。

## 10. 界面

### 10.1 四步向导

1. **目标**：target_url 输入；test-target 按钮（非阻断）；格式错误才禁用下一步；反代模式显示“目标拦截”提示。
2. **应用信息**：display_name、appname（预填 `proxy.<slug>.<4hex>`，可改，异步 check）、description、version（高级折叠，默认 1.0.0）。
3. **入口与高级**：mode（reverse/direct）、ui_type（url/iframe）、all_users（默认 false）、no_display、entry_title、install_volume、skip_tls_verify（仅 https+reverse）、allow_loopback（仅 reverse）、Basic Auth（开关 + 用户名/密码，仅 reverse）；端口只读展示（新建时“生成时自动分配”，编辑时显示已分配端口）。
4. **确认与生成**：摘要含入口端口与最终 URL；生成按钮；进度 5 步（校验 → 生成包 → 安装 → 启动 → 完成）；失败停在对应步骤，提供下载 fpk、查看日志、重试；成功后给入口链接。

### 10.2 列表

列：应用（display_name + appname）、目标、模式、入口（`http://<host>:<port>/`，复制 URL）、状态、最近操作（时间 + 结果 + 短原因）、操作（打开/编辑/重装/回滚/删除/下载/日志）。

- 编辑：appname 锁定；保存后若已安装，提示“将重新生成并重装”；可“另存为新应用”（新 appname）。
- 删除：二次确认，输入 appname 才可提交；卸载失败展示原因与重试。
- 不做实时健康检查；提供刷新。
- 空状态给“新建代理应用”按钮。
- 所有错误用统一 toast + 字段级错误；不显示上游/堆栈。

## 11. 测试与验收

### 11.1 单元测试

- target 规范化表（IPv4/域名/IPv6/默认端口/path 拒绝/userinfo 拒绝）。
- appname/version/display/desc 校验。
- 目标拦截 CIDR 表（含 IPv4-mapped、混合解析、allow_loopback、端口拒绝）。
- Cookie Path 根路径分支（空/`/`/绝对路径/非法相对）；Location 同 origin/跨 origin/相对。
- 端口分配：区间、跳过已用、bind 失败跳过、耗尽报错；`Port=0` 只在测试/构建层允许。
- hop-by-hop 头、Forwarded 清理、Origin/Referer 改写。
- config/state JSON 解析、缺省、未知键、原子写。
- manifest 生成与 checksum；app.tgz 成员/权限/无符号链接。
- 状态机迁移表。

### 11.2 黄金包测试

对固定 spec 生成 fpk，断言：

- 成员集合与顺序无关；
- manifest 含 appname/version/display_name/desc/platform/source/maintainer/distributor/os_min_version/desktop_uidir/desktop_applaunchname/service_port/checkport/checksum；
- 不含 install_type/arch；
- `app.tgz` 根下直接 `ui/`、`bin/`、`config.json`；
- `ui/config` key == desktop_applaunchname，`port` == service_port，`url` == `/`；
- checksum == md5(app.tgz)；
- 权限/大小/图标尺寸；
- `config/privilege` 与 fnOS 模板逐字节一致；
- config.json 不含明文口令。

### 11.3 集成测试

- 假 CLI（shell 脚本）模拟：成功、exit 0 无标记、10111、10500、starting 超时、stopped→start、卸载失败；验证轮询、错误映射、回滚、锁。
- 假 target HTTP server（代理以 TCP 端口启动）：reverse 正常、重定向、Set-Cookie、SSE、大文件、WebSocket 升级、TLS 自签（skip_verify 开关）、502。
- 托管台 CGI handler：路径截断、Upgrade 501、socket 缺失 503。
- API：CSRF 缺失/错误、Origin 不符、appname 冲突、删除两段式、日志脱敏。

### 11.4 真机验收

1. 构建并安装 `daili.manager.fpk`（`sudo appcenter-cli install-fpk -v 1 ...`），确认 running。
2. 打开 `/cgi/ThirdParty/daili.manager/index.cgi/`，四步创建一个反代目标。
3. `sudo appcenter-cli list` 看到代理 running；浏览器打开 `http://<fnos-host>:<port>/`，目标首页与**绝对路径资源**（如 `/static/app.js`）都正常。
4. 直连应用：入口 302 到目标，后续浏览器地址是目标。
5. Basic Auth：401 → 正确口令 200；错误口令不泄露。
6. 目标为 `127.0.0.1` 时默认 502；开 allow_loopback 后可访问（若目标确实在本机）。
7. 修改 display_name 后重新应用；升级失败注入（坏包）后回滚到上一版。
8. 删除应用：先卸载，list 消失，state 清空；卸载失败保留 uninstall_failed。
9. 重启托管台后 state 对账：missing/unmanaged 正确。
10. 日志不含口令/Cookie/query；错误页不含上游。

## 12. 实现切分（建议顺序）

1. **基础库**：target 规范化、校验、状态/配置 JSON、原子写、日志脱敏。
2. **fpk 生成器**：tar/gzip、manifest、checksum、图标、预检；黄金包测试。
3. **代理二进制**：serve/cgi、reverse/direct、guard、auth、headers、错误页。
4. **CLI 客户端**：定位、执行、输出解析、list/status 轮询、错误映射。
5. **托管台服务**：state、API、CSRF、构建/安装/卸载/升级/回滚、锁、对账。
6. **UI**：四步向导、列表、操作与轮询、错误展示。
7. **真机验收**：按 §11.4。

## 13. 已知限制

1. 托管台管理员认证只依赖飞牛登录态 + `allUsers=false`；知道 URL 的普通登录用户可能访问。需要更强隔离时另开票。
2. 托管台 CGI 入口不支持 WebSocket（Upgrade 返回 501）；代理应用走独立 TCP 端口，支持 WebSocket 与 SSE。
3. 代理端口需要从局域网可达；飞牛防火墙若启用需放行 20000–29999，规则未实测。端口被其他进程占用时安装后启动失败，需换端口重建（删除后新建）。
4. iframe 模式受目标 `X-Frame-Options`/`CSP frame-ancestors` 限制；默认 `url` 新标签可绕开。
5. Basic Auth 在 HTTP 入口上明文传输；不负责加密；需要加密时用 HTTPS 入口或直连。
6. 不做 HTML/绝对 URL 改写；独立端口让目标页的绝对路径（`/static/app.js`）自然落到代理 origin，因此不依赖站点使用相对路径。若目标页硬编码完整外部 URL，仍会指回外部地址。
7. 应用中心不校验 checksum/desktop_applaunchname 一致性（V1 实测）；正确性由生成器预检保证。
8. `install-fpk` 不支持原地升级；升级必然卸载重装，期间入口短暂不可用。
9. 非 root 无法调 CLI；托管台必须 run-as root。
10. 每个应用一个入口；多入口/多目标 v1 不做。
11. ARM 包需在 ARM 构建；v1 先发 x86。
12. 目标 DNS 在创建时失败仍允许保存；运行时每次拨号重新解析并拦截。

## 14. 决议映射

| 决议 | 来源 | 本规格位置 |
|---|---|---|
| 每包内嵌通用二进制 + config.json | T1 #7 | §3.2、§5.3 |
| 独立端口 + 新标签 | T1 #7；2026-11-16 恢复（CGI 前缀破坏绝对路径） | §3.2、§3.3、§5.4 |
| 端口 20000–29999 | T1 #7（2026-11-16 恢复：分配 + 拦截） | §4.3、§7.5、§9.2 |
| 目标只收 origin | T1 #7 | §4.2 |
| 直连 302 | T1 #7 | §7.4 |
| run-as package / manager root | T1 #7、T2 #4 | §3、§5.2、§5.3 |
| 一应用一入口 | T1 #7 | §2、§5.4 |
| install-fpk、轮询、回滚、锁 | T2 #4、V1 #10 | §9 |
| state.json、对账、missing/unmanaged | T2 #4 | §4.4、§8.3 |
| 包保留当前+上一版 | T2 #4 | §9.1 |
| 默认卷 + 改卷重装 | T2 #4 | §9.3 |
| 托管台 CGI、allUsers=false、CSRF | T3 #9（网关部分作废） | §8.1 |
| 代理默认 allUsers=false | T3 #9 | §5.4 |
| Basic Auth | T3 #9 | §7.6 |
| 目标拦截 | T3 #9 | §7.5 |
| 错误页/日志脱敏 | T3 #9 | §7.7 |
| 四步向导、字段分层、列表 | T4 #6 | §10 |
| 内置图标 | T4 #6 | §5.4 |
| TLS 校验/开关 | T5 #3 | §7.2 |
| Host/SNI=target、X-Forwarded-* | T5 #3 | §7.2 |
| Location/Cookie/安全头 | T5 #3（Cookie Path 按入口根路径修正） | §7.2、§7.3 |
| WS/SSE、超时、流式 | T5 #3（代理端口恢复支持；托管台 CGI 501） | §6、§7.2、§13 |
| 代理入口恢复独立 TCP 端口 | 2026-11-16 用户决策 | §3.2、§3.3、§7.3、§11.4 |
| CGI 入口 | 2026-11-16 源码复核；仅托管台 | §3、§5、§6 |

## 15. 未在真机验证的风险

- `run-as root` + 空 `install_type` 的组合未在真机验证（V1 只验证了 package + 空 install_type）。首个托管台包安装时先做冒烟；若应用中心拒绝，回退写 `install_type=root` 并记录，不影响代理包。
