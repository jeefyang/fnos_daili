# fnOS（飞牛 OS）第三方应用打包 与「应用入口嵌入任意 Web 目标」可行性调研

> 调研日期：2026-11-16。范围：fnOS 官方开发者文档、fnpack 1.2.3（实测）、社区已跑通实例（watchcow / conversun 应用集 / deepseek-harness-fnos）、官方帮助中心。只做调研，未改代码、未提交。

## 可信度分级

| 标记 | 含义 |
| --- | --- |
| **[官方]** | 飞牛官方文档/帮助中心原文，附页面 URL 与行号（行号取自官方文档夜间同步镜像 ckcoding/fnnas-docs @ `a8a70503b5b3413f0a00f4b6db6cd62066639508`，页面 URL 为 developer.fnnas.com 对应页） |
| **[实测]** | 本文作者用官方 fnpack 1.2.3 实际执行得到，可复现（§8） |
| **[样例]** | 社区公开 fpk 源码里的实际写法，附带 sha 的 permalink；样例≠规范 |
| **[社区经验]** | 社区作者的踩坑记录，非官方、非源码 |
| **[推断]** | 由官方字段定义/样例/Web 标准推出，无官方明文与直接实测 |
| **[未证实]** | 证据不足或互相冲突，需人拍板或上机验证 |

一手来源：开发者文档 <https://developer.fnnas.com/docs/guide>（镜像 <https://github.com/ckcoding/fnnas-docs>）、开放 API <https://developer.fnnas.com/api/overview>、帮助中心 <https://help.fnnas.com/>、fnpack <https://static2.fnnas.com/fnpack/fnpack-1.2.3-windows-amd64>；watchcow @ `7dbe85ad4616d156cab5315a85eef696826cd19c`；conversun/fnos-apps（186 个包）@ `cb13e3e1d42bace23de7baeb0f1ca9d8ab8578f3`；deepseek-harness-fnos @ `080d91ef13bdd364df2be8dab7f1171ce5ed633e`；fnos-app-dev-skill @ `92b2cb8d624e0cc660f3f453292c745733baf3bf`。

---

## 0. 结论速览

| 问题 | 结论 | 可信度 |
| --- | --- | --- |
| fpk 结构 | gzip tar 容器；必需 `manifest`、`config/privilege`、`config/resource`、`ICON.PNG`、`ICON_256.PNG`、`app/`、`cmd/`、`wizard/` | [官方]+[实测] |
| manifest 官方字段 | 约 20 个；**没有** `privileged` / `install_volume` / `ui_type` / `arch`（`arch` 已于 2025-12-31 废弃），架构字段是 `platform` | [官方] |
| 入口定义 | `app/ui/config` 里的 `".url"` 对象；`type` 只有 `iframe`（桌面窗口内）与 `url`（标签页/外部 Web 视图） | [官方] |
| 入口 URL 形态 | 官方定义 `url` 是"路径"，host 由当前访问域名拼；`protocol`+`port` 决定协议与端口 | [官方]+[推断] |
| 能否指向任意外部站点 | 官方模型不含"外部主机"；社区唯一套路是 CGI 页面**顶层跳转**（watchcow），不是嵌入 | [官方]+[样例]+[未证实] |
| 安装 | 应用中心"手动安装"= 本地测试入口；`appcenter-cli install-fpk`/`install-local` 是官方 CLI；需管理员 | [官方] |
| 被其它应用运行时调用 | 可行且已有先例（watchcow 调 `install-local --volume`；fnos-apps-store 提供装卸 UI），均以 root 运行 | [样例] |
| 端口入口是否带 SSO | **不带**。官方明说端口入口"和 NAS 用户登录态无关"；网关/CGI 才校验登录态 | [官方] |
| iframe 真实约束 | 端口入口 iframe 跨源（host 同、port 不同）：HTTPS 桌面嵌 HTTP 目标=混合内容被拦；跨源 cookie 受 SameSite 限制；目标站 XFO/CSP 照样生效 | [推断]+[社区经验] |
| 同源嵌任意后端 | 走统一网关（`gatewayPrefix`+`gatewaySocket`，Unix Socket）：同源、带 SSO 头、支持 WebSocket；代价是应用内自写反代+HTML 前缀重写 | [官方]+[样例] |
| 现成反代先例 | lucky / nginx-proxy-manager / nginx / nginx-ui / 1panel / dpanel 全是"自带端口+type=url"，**没有**"把外部站点包成 iframe 入口"的包 | [样例] |
| 一个包能否多入口 | 能（官方支持 + `desktop_applaunchname`）；但 186 个社区包中 0 个多入口样例 | [官方]+[实测] |

---

## 1. 包结构与 manifest

### 1.1 打包目录（官方）

```text
myapp/
├── app/
│   ├── ui/            # desktop_uidir，默认 ui
│   │   ├── config     # 入口定义（JSON）
│   │   └── images/    # 入口图标
│   └── docker/        # docker 模板才有
├── cmd/               # 生命周期脚本
├── config/
│   ├── privilege
│   └── resource
├── wizard/
├── manifest
├── ICON.PNG
└── ICON_256.PNG
```

来源：[官方 fnpack](https://developer.fnnas.com/docs/cli/fnpack/)，镜像 `docs/cli/fnpack.md:53-80`。打包强制检查项见 `:99-111`：

| 路径 | 类型 | 要求 |
| --- | --- | --- |
| `manifest` | 文件 | 存在，含必要字段 |
| `config/privilege` | 文件 | 存在，合法 JSON |
| `config/resource` | 文件 | 存在，合法 JSON |
| `ICON.PNG` | 文件 | 存在 |
| `ICON_256.PNG` | 文件 | 存在 |
| `app/` | 目录 | 存在 |
| `cmd/` | 目录 | 存在 |
| `wizard/` | 目录 | 存在 |
| `app/{desktop_uidir}/` | 目录 | 声明 desktop_uidir 时必须存在 |

[实测] fnpack 1.2.3 报错原文：`Packing failed. Required file config/resource is missing`、`Required file ICON_256.PNG is missing`。

### 1.2 容器内部格式 [实测]

`fpk` 是 **gzip tar**（magic `1f 8b`），成员：

```text
manifest        # 纯文本 key = value
config/privilege
config/resource
ICON.PNG
ICON_256.PNG
app.tgz         # 整个 app/ 打成的 tar.gz
cmd/*           # 保留可执行位
wizard/
```

[实测] build 时 fnpack **自动往 manifest 追加** `checksum=<md5(app.tgz)>`（本次测到 `2bf784591919f0bc220e67688395336d`，与 `md5sum app.tgz` 一致）。

[实测] fnpack **不校验未知字段**：`install_volume / privileged / arch / ui_type / service_port / checkport / ctl_stop` 塞进 manifest 全部原样进入 fpk。**"fnpack 接受"≠"官方字段"**。

### 1.3 manifest 字段（官方）

<https://developer.fnnas.com/docs/core-concepts/manifest/>，镜像 `docs/core-concepts/manifest.md`：

| 字段 | 说明 | 行号 |
| --- | --- | --- |
| `appname` | 应用名，入口 ID 前缀；只允许字母数字 `- _ .`，首尾须字母或数字 | `:11-15` |
| `version` / `display_name` / `desc`（可用 HTML）/ `source`（第三方填 `thirdparty`） | | `:11-15` |
| `platform` | `x86`/`arm`/`all`（`all` 仅当无架构二进制） | `:19-22` |
| `maintainer` / `maintainer_url` / `distributor` / `distributor_url` | | `:26-29` |
| `os_min_version` / `os_max_version` | | `:33-34` |
| `ctl_stop` | 应用中心是否显示启动/停止 | `:40-44` |
| `install_type` | 空=用户选盘；`root`=装系统分区 | `:48-53` |
| `install_dep_apps` | 运行时依赖，`:` 分隔，`>` 最低版本 | `:48-53` |
| `desktop_uidir` / `desktop_applaunchname` | UI 目录（默认 ui）/ 卡片默认入口 | `:57-58` |
| `service_port` / `checkport` | 服务端口 / 启动前是否查端口（默认 true，不监听固定端口可省略或 false） | `:62-65` |
| `disable_authorization_path` | 设置页是否显示授权目录 | `:69-71` |
| `changelog` | 更新说明 | `:77` |

官方示例 `manifest.md:85-103`。

坑与边界：

- `arch` 已废弃：更新日志 2025-12-31「manifest 中废弃原 arch 字段，改用 platform」——`docs/update-log.md:66`（<https://developer.fnnas.com/docs/update-log>）。[样例] 仍在用（watchcow `fnos-app/manifest` 写 `arch=x86_64`）或不写（conversun nginx）。
- [实测] `platform` 合法值比文档多：报错原文 `It must be one of x86, arm, loongarch, risc-v, or all.`（文档只写 x86/arm/all）。
- **`privileged` / `install_volume` / `ui_type` 都不是官方字段**（文档全文无此三词）。对应能力实际落在 `config/privilege` 的 `run-as`、安装卷参数、入口 `type`。
- [未证实] `micro_app=true`：只出现在**开放 API** 文档「如果应用需要调用 JS SDK，必须在 manifest 中声明：micro_app=true」（<https://developer.fnnas.com/api/calling>），`manifest.md` 未收录；社区样例写成入口级 `"microApp": true`（§3.5），写法不一致。
- [未证实] `service_socket`：官方无此字段，但 ofterm / fnos-apps-store 都写 `service_port=0` + `service_socket=app.sock`（<https://github.com/conversun/fnos-apps/blob/cb13e3e1d42bace23de7baeb0f1ca9d8ab8578f3/apps/ofterm/fnos/manifest>），推断是网关模型隐式声明。

### 1.4 入口：app/ui/config

<https://developer.fnnas.com/docs/core-concepts/app-entry/>，镜像 `docs/core-concepts/app-entry.md`。

```json
{ ".url": { "myapp.main": {
    "title": "My App",
    "icon": "images/icon_{0}.png",
    "type": "iframe",        // 或 "url"
    "protocol": "http",      // http | https | ""
    "port": "8080",          // 可用 ${wizard_port}
    "url": "/",              // 可用 ${wizard_path}；是"路径"
    "allUsers": true,
    "fileTypes": ["txt","md"],
    "noDisplay": false,
    "control": { "accessPerm": "editable" }
} } }
```

字段逐条（`app-entry.md:37-51`）：`type` 中 `iframe`=在 fnOS 桌面窗口内打开、`url`=浏览器标签页或外部 Web 视图（`:39-41`，建议要完整浏览器能力用 `url`：`:75`）；`protocol` http/https/空串自适应（`:42`）；`port`（`:43`）；`url`=**入口打开的路径**（`:44`）；`allUsers`=入口是否对所有用户可见（`:45`）；`fileTypes`=文件入口扩展名（`:46`）；`noDisplay`=桌面隐藏入口但保留文件操作入口（`:47`）；`control.accessPerm` editable/readonly/hidden（`:48-51`）。多入口时 `desktop_applaunchname` 指定卡片打开的入口（`:33`）。

运行时三模型：

| 模型 | 托管方 | URL 形态 | 登录态 | WebSocket | 来源 |
| --- | --- | --- | --- | --- | --- |
| 端口服务 | **应用自己**监听端口，桌面只负责打开 | `protocol://<当前访问域名>:<port><url>` | **与 NAS 登录态无关** | 取决于应用 | `app-entry.md:138`；拼装为[推断] |
| index.cgi | fnOS 调用应用提供的 CGI | `/cgi/ThirdParty/{appname}/index.cgi/`，沿用当前域名 | 调用前校验登录态 | **不支持** | `index-cgi.md:5,7,11,20,43` |
| 统一网关 | fnOS 网关 `/app/{appname}` → Unix Socket | `/app/{appname}[/customPath]` | 转发前校验会话，注入 `X-Trim-Userid/Isadmin/Username` | **支持** | `gateway-registration.md:26-31,68-81,91-103,132-160` |

注意 CGI 示例里虽写了 `protocol:http`+`port:8080`，但文档明说「对于 CGI 入口，protocol 和 port 会被忽略」（`index-cgi.md:7`，示例 `:33-36`）。网关示例（`gateway-registration.md:39-54`）用 `type=iframe`、`protocol=""`、`gatewayPrefix=/app/myapp`、`gatewaySocket=app.sock`、`url=/app/myapp`；`gatewayPrefix` 必须是 `/app/{appname}`、`gatewaySocket` 只填文件名且放 `target/`（`:74-81`）；Docker 应用挂载 ${TRIM_APPDEST} 建 socket 也可用（`:86`）。

### 1.5 图标

<https://developer.fnnas.com/docs/core-concepts/icon/>，镜像 `docs/core-concepts/icon.md:9-34,40-74`：包根 `ICON.PNG`(64x64) + `ICON_256.PNG`(256x256) 必填，PNG/JPG、sRGB、≤1024KB、圆角矩形；入口图标 `app/ui/images/icon_{0}.png`。[社区经验] 必须 RGBA，RGB 会被拒（fnos-app-dev-skill 的「应用包不符合系统要求」清单）。

---

## 2. 本地安装与卸载

### 2.1 应用中心"本地安装" vs appcenter-cli（官方）

- 手动安装：官方测试文档「打开应用中心，进入手动安装入口，并选择 HelloFnos.fpk」，并强调「**手动安装仅用于本地测试，不应作为公开分发方式**」——`docs/quick-started/test-application.md:11-19`（<https://developer.fnnas.com/docs/quick-started/test-application/>）。
- 官方 CLI 文档 `docs/cli/appcentercli.md:5-82`（<https://developer.fnnas.com/docs/cli/appcentercli/>）：`appcenter-cli install-fpk myapp.fpk`（向导 `--env config.env`，`:7-30`）；`cd /path/to/myapp && appcenter-cli install-local`——「该命令会完成本地打包和安装」（`:32-41`）；`default-volume`（`:43-55`）、`list/start/stop`（`:57-75`）。**没有 `uninstall`，也没有 `install-local --volume`**。
- 权限：官方前置条件「应用测试通常需要…管理员权限」，第一项即「安装和卸载本地应用包」——`docs/quick-started/prerequisites.md:44-54`。
- [社区经验] SSH 用户通常非 root，`appcenter-cli` 属 root 且 700，普通用户不可用（`references/fnos-package-rejection-troubleshooting.md:70-72`）。

### 2.2 安装时校验什么

- [实测] fnpack 侧：必需文件/目录、manifest 必填（appname）、`platform` 枚举、`desktop_applaunchname` 必须命中 `app/ui/config`（报错原文 `The value of desktop_applaunchname in manifest does not match any entry name defined in app/ui/config`）、自动写入 `checksum`。
- [未证实] 应用中心是否校验 `checksum`、有无签名：官方文档**全站无签名/证书校验说明**，`publish-application.md` 只讲发布与测试。
- [社区经验] 常见拒绝原因（非签名）：`desktop_applaunchname` 必须完全等于 `<appname>.Application`；`service_port` 需高端口（20127/20128/20365/8005 可用；1313 被拒、13133 通过）；`config/resource` 的 `[` 必须与 key 同行、4 空格缩进；图标必须 RGBA；manifest 不留空字段（空 `install_dep_apps` 解析失败）——见 <https://github.com/techysy/fnos-app-dev-skill/blob/92b2cb8d624e0cc660f3f453292c745733baf3bf/references/fnos-packaging-pitfalls.md> 与 `references/fnos-package-rejection-troubleshooting.md:14-24`。
- [未证实且冲突] 同 repo `references/fnos-dev-guide-outdated-flows.md:24` 称「install-fpk / install-local 在 fnOS 1.1.31xx 已被移除，只能 App Center Web UI 手动安装」。与现行官方文档、watchcow（2026-08）、fnos-apps-store（2026-11）冲突，未上机核实。

### 2.3 能否被另一个应用在运行时调用

能，两个公开先例：

1. **watchcow**（运行期装/卸"助手"App）：
   - 定位 CLI：`/var/apps/appcenter/target/bin/appcenter-cli`、`/usr/bin`、`/usr/local/bin`、PATH、`which` —— <https://github.com/tf4fun/watchcow/blob/7dbe85ad4616d156cab5315a85eef696826cd19c/internal/fpkgen/installer.go#L47-L81>
   - 安装：`exec.Command(cli, "install-local", "--volume", volume)`，`cmd.Dir=appDir` —— 同文件 `#L84-L101`（`--volume` 官方未文档化）
   - 默认卷：`appcenter-cli default-volume`（`#L126-L135`）；卸载：先 `stop` 再 `uninstall <appName>`（`#L152-L165`，`uninstall` 官方未文档化）
   - `config/privilege` 为 `run-as root`，README 写明"运行于 Root 权限"。
2. **fnos-apps-store**（第三方应用商店，一键装/卸 conversun 的 175+ 应用）：网关型包（`type=iframe` + `gatewayPrefix` + `gatewaySocket=app.sock`），`run-as root`，`service_port=0`、`service_socket=app.sock` —— <https://github.com/conversun/fnos-apps/blob/cb13e3e1d42bace23de7baeb0f1ca9d8ab8578f3/apps/fnos-apps-store/fnos/manifest>。二进制不在仓库，具体调用方式未见源码（[未证实]）。

结论：**运行时装/卸 fpk 是既成事实，前提是应用以 root（或具备 appcenter-cli 执行权限）运行**；官方只承认 CLI 供本地调试/CI，未承诺第三方应用可编程调用。

---

## 3. 入口与网络

### 3.1 谁托管入口

- 端口服务：应用自己监听端口，桌面只打开 URL。官方原话「端口服务：入口打开应用自己的服务端口。这种方式和 NAS 用户登录态无关」（`app-entry.md:138`）→ [推断] 端口入口不经应用中心网关、不带 SSO、不做鉴权，直连 `<host>:<port>`。
- index.cgi：fnOS 侧 CGI 调度器调用包内 `app/ui/index.cgi`，路径固定 `/cgi/ThirdParty/{appname}/index.cgi/`（`index-cgi.md:5,20`），与桌面**同源**。
- 统一网关：fnOS 网关监听 `/app/{appname}`，转发到 `/var/apps/{appname}/target/<socket>`（`gateway-registration.md:26-31,62-66`）。

### 3.2 入口 URL 能否是任意外部地址

- [官方] `url` = "入口打开的路径"（`app-entry.md:44`），`protocol`+`port` 另给，host 用当前访问域名（`index-cgi.md:43` 明说）→ 官方模型没有"外部主机"维度。
- [实测] conversun 186 包 / 173 入口，`url` **无一个是** `http(s)://` 绝对地址（全为 `/`、`/app/...`、`/cgi/...`），复现脚本见 §8。
- [样例] "跳外链"的唯一成熟做法是 **CGI 顶层跳转**（watchcow）：构建期若入口配 `redirect`，就把 `port` 清空、`url` 改成 `/cgi/ThirdParty/<AppName>/index.cgi/redirect/<appname>/<entry><path>`（<https://github.com/tf4fun/watchcow/blob/7dbe85ad4616d156cab5315a85eef696826cd19c/internal/fpkgen/template.go#L141-L151>），CGI 输出整页 `window.location.replace(外部URL)`（`internal/server/redirect.go#L97-L152`、`web/templates/redirect.tmpl#L65-L91`）。先做内网判定（`redirect.tmpl#L118-L155`，IP/`.local`/无点主机名），`force_external` 可跳过。**这是顶层导航，不是嵌入、也不是反代。**
- [未证实] 直接把绝对 URL 写进入口 `url`：无官方说明、无样例、未上机。

### 3.3 HTTPS 桌面 + HTTP 目标：混合内容

- 官方帮助：V0.8.22 起默认 HTTP 5666 / HTTPS 5667，可改端口、可开"强制 HTTPS 连接"（HTTP→HTTPS），可把 80/443 重定向到 5666/5667 —— <https://help.fnnas.com/articles/v1/settings/port-customization>。
- [推断/Web 标准] HTTPS 桌面里 iframe 加载 `http://nas:port` = **主动混合内容**，现代浏览器直接拦截（或自动升级 https 后失败），见 MDN <https://developer.mozilla.org/en-US/docs/Web/Security/Mixed_content>。同一包在 `http://nas:5666` 正常、在 `https://nas:5667` 白屏。`http://localhost` 虽属潜在可信来源，但端口入口用 NAS 主机名，救不了。
- [官方] 官方文档对混合内容/CSP/XFO **完全未提及**（全站检索无结果）。

### 3.4 iframe 加载第三方站点：XFO / CSP

- 标准事实：响应带 `X-Frame-Options: DENY/SAMEORIGIN` 或 `Content-Security-Policy: frame-ancestors ...` 时浏览器拒绝渲染（MDN <https://developer.mozilla.org/en-US/docs/Web/HTTP/Headers/X-Frame-Options>、<https://developer.mozilla.org/en-US/docs/Web/HTTP/Headers/Content-Security-Policy/frame-ancestors>）。
- [推断] fnOS 桌面 iframe 没有"剥头"能力，响应头由目标服务器决定 → **目标禁止被嵌时必然失败**；绕开只能服务端反代后同源输出。
- [社区经验] 真实症状：`type=iframe` 在 5666 里嵌应用端口"有跨域问题，WebUI 白屏"；入口指向 JSON API（无 CORS）→ `WebKitErrorDomain code=102` 白屏；**手机飞牛 App 对 url/iframe 都用 WebView iframe 打开**；`fygo-browser` 也可能拦 iframe —— <https://github.com/techysy/fnos-app-dev-skill/blob/92b2cb8d624e0cc660f3f453292c745733baf3bf/references/fnos-packaging-pitfalls.md>、同 repo `SKILL.md`（"Try type: url instead of iframe — iframe may be blocked by fygo-browser"）。
- [社区经验] 更硬的一条：**跨源 iframe 里 SameSite=lax 的会话 cookie 不持久**，套壳自带登录的应用会登录死循环（2026-08，9Router）；新标签页正常、手机 WebView 仍失败（`SKILL.md` "Login-based apps" 告警段）。→ 需要登录态的目标，跨源 iframe 不可靠；只能同源（统一网关）或新标签页。

### 3.5 内置反代的实例

- **deepseek-harness-fnos**：入口 `type=iframe + microApp:true + gatewayPrefix:/app/dsh + gatewaySocket:app.sock`（<https://github.com/techysy/deepseek-harness-fnos/blob/080d91ef13bdd364df2be8dab7f1171ce5ed633e/ui-config>），应用内 `proxy.py` 监听 Unix socket 反代到 `127.0.0.1:28001`：
  - `proxy.py:4` 注释：监听 ${TRIM_APPDEST}/target/app.sock，由统一网关转发；
  - `proxy.py:93-97`：必须给 HTML 里绝对资源路径（`/assets`、`/plugins`、`/manifest`、`/favicon`）加 `GATEWAY_PREFIX`，否则丢前缀 404 空白页；
  - `proxy.py:98-107`：重写 `Origin/Referer`（后端有 Host/Origin fence，否则 403）；
  - `proxy.py:159-213`：WebSocket 原始 socket 直通。
  → "把任意后端同源嵌进桌面"最完整的公开实例，代价是前缀重写 + Origin 改写 + WebSocket。
- **surface-battery**：`type=iframe` + `url=/cgi/ThirdParty/surface_battery_driver/index.cgi/`，`ui/index.cgi` 为 bash CGI 直接输出 HTML，与桌面同源（<https://github.com/conversun/fnos-apps/blob/cb13e3e1d42bace23de7baeb0f1ca9d8ab8578f3/apps/surface-battery/fnos/ui/config>）。CGI 能做同源小页面，但不支持 WebSocket、每请求起进程。

---

## 4. 应用自建服务：端口、暴露、鉴权

- [官方] 端口来自 `manifest.service_port`，运行期注入 `TRIM_SERVICE_PORT`（`environment-variables.md:48`）；`checkport` 控制"启动应用前是否检查端口"（`manifest.md:62-65`）。官方示例 `cmd/main` 用 `--port $TRIM_SERVICE_PORT` 起服务、`status` 用 `pgrep -f "myapp.*$TRIM_SERVICE_PORT"`（`environment-variables.md:96-136`）。
- [官方] **无端口分配器**：端口由开发者写死，fnOS 只在启动前检查（`checkport`）。冲突行为官方未写 → [未证实]。
- [社区经验] 需高端口（≥5000 量级）：1313 被拒、13133 通过（pitfalls 链接）。
- [样例] Docker 型包用 `config/resource` 的 `port-config`：nginx 为 `{"port-config":{"protocol-file":"nginxserver.sc"},"data-share":{...},"systemd-unit":{}}`，`nginxserver.sc` 是 docker-compose 风格 `src.ports/dst.ports` 声明（<https://github.com/conversun/fnos-apps/blob/cb13e3e1d42bace23de7baeb0f1ca9d8ab8578f3/apps/nginx/fnos/config/resource>）。官方 resource 文档只写 `data-share`/`usr-local-linker`/`docker-project`（`resource.md:7-125`），`port-config/systemd-unit/api-scope` 未文档化 → [样例]+[推断]。
- [官方] `allUsers` 只是可见性（`app-entry.md:45`），**不是鉴权**：网关文档明说"网关校验登录状态，不负责业务权限"，仅管理员应用建议同时 `allUsers=false` + `accessPerm=readonly`（`gateway-registration.md:166`、`app-entry.md:148`）。
- [官方] SSO 只在网关模型存在：`X-Trim-Userid/Isadmin/Username`（`gateway-registration.md:91-103`，WebSocket 同样带 `:160`）。CGI 是否注入用户身份未见文档 → [未证实]。端口服务**无任何 SSO**（`app-entry.md:138`）。

---

## 5. 反向代理先例

| 项目 | 包名 | 接入方式 | 备注 |
| --- | --- | --- | --- |
| Lucky | `lucky` | `type=url`、`port=16601`、`protocol=http`、`url="/"`、`allUsers=true` | DDNS/反代/HTTPS/端口转发，自带端口，非 fnOS 内置 |
| Nginx Proxy Manager | `nginx-proxy-manager` | `type=url`、`port=81`、`checkport=false`、`url="/"`，resource 用 `docker-project` | 官方 NPM 镜像封装 |
| nginx | `nginxserver` | `type=url`、`port=8888`，resource `port-config` | 静态/反代 |
| nginx-ui / nginx-web / 1panel / dpanel / frps / cloudflared | … | 同为"自带端口 + type=url" | 语料里无"外部站点包成 iframe 入口"的包 |

来源：<https://github.com/conversun/fnos-apps/tree/cb13e3e1d42bace23de7baeb0f1ca9d8ab8578f3/apps>（各包 `fnos/ui/config`、`fnos/manifest`、`fnos/config/resource`）。

- **fnOS 是否有官方内置反代**：[未证实，倾向没有]。帮助中心 106 篇文章（<https://help.fnnas.com/sitemap.xml>）无"反向代理"条目，开发者文档亦无；lucky/NPM 长期占位也旁证官方未提供。
- 入口接法：全部"应用自己监听端口 + `type=url` 打开新标签页"，不经 fnOS 网关。
- 唯一接近"内置反代"的是 §3.5 的 deepseek-harness-fnos（同源网关反代自家后端）。

---

## 6. 约束清单

| 约束 | 结论 | 可信度 |
| --- | --- | --- |
| 一个 fpk 只能有一个 appname？ | 是。`appname` 是 manifest 单值字段，且是安装目录/入口前缀/授权路径的根 | [官方] |
| 桌面图标数量 vs 入口数量 | 每个入口在桌面出现一个图标；`noDisplay=true` 把它从桌面隐藏、只留"打开方式"。官方无"每入口一图标"明文，但 noDisplay 语义（"在桌面隐藏入口"）反推默认显示 | [推断，高置信] |
| 多入口桌面呈现 | 各入口按自己的 `title/icon/type` 呈现；应用中心卡片只打开 `desktop_applaunchname` 指定的那个 | [官方]（卡片）+[推断]（桌面） |
| 多入口实例 | 语料 186 包 / 173 入口，**0 个多入口包**；官方也无多入口完整 JSON 示例 | [实测] |
| `noDisplay` 用途 | 文件关联：`fileTypes` + `noDisplay:true` → 不在桌面，只在"打开方式"出现；打开时 URL 追加 `?path=<绝对路径>`（官方示例 `http://localhost:8080/edit?path=/vol1/...`） | [官方] `app-entry.md:102-130` |
| 文件类型关联 | `fileTypes: ["txt","md"]`；不配 `noDisplay` 则桌面与打开方式同时出现 | [官方] |
| 无 UI 的包 | `fnpack create <appname> --template docker --without-ui true`（不生成桌面入口） | [官方] `fnpack.md:45-49` |
| 入口 ID 命名 | 以 `appname` 作前缀（如 `myapp.main`）；`desktop_applaunchname` 必须能在 config 命中 | [官方]+[实测] |
| 生命周期脚本 | `cmd/` 下 `main(start/stop/status)` + install/upgrade/uninstall/config 的 `_init/_callback`；`status` 退出码 0/1/3 | [官方] `framework.md:7-52`；[社区经验] 停止时 `status` 必须返回 1，否则 fnOS 不再调 start |
| 运行用户 | `config/privilege` 的 `defaults.run-as`：`package`（默认）或 `root` | [官方] `privilege.md:5-79` |

---

## 7. 方案矩阵（面向"入口嵌入任意 Web 目标"）

| 方案 | 能嵌外部站点？ | 同源/SSO | 混合内容 | 目标站 XFO/CSP | 代价 |
| --- | --- | --- | --- | --- | --- |
| A. `type=url` + 端口 | 否（只指向本机端口） | 跨源、无 SSO | 新标签页无此问题 | 不适用 | 最简单 |
| B. `type=iframe` + 端口 | 否 | 跨源、无 SSO、cookie 受 SameSite 限 | **HTTPS 桌面下被拦** | 直接嵌第三方会被拦 | 低 |
| C. `type=iframe` + index.cgi | 半（CGI 自行 fetch 再输出=自建反代） | 同源；校验登录态但无用户头 | 无 | 可绕（由 CGI 输出） | 中；无 WebSocket、每请求进程 |
| D. `type=iframe` + 统一网关 | 是（应用内反代） | **同源**+SSO 头+WebSocket | 无（走 `/app/*`） | 可绕（响应经应用改写） | 高：前缀重写、Origin 改写、socket 生命周期 |
| E. CGI 顶层跳转（watchcow） | 是，但**离开桌面** | — | 新页面无 | 不适用 | 低，但不是嵌入 |

建议：要"在飞牛桌面里以应用身份呈现任意 Web 目标"，只有 C/D 可行；D 是唯一同时满足同源 + SSO + WebSocket 的路径，直接参照 deepseek-harness-fnos 的 `proxy.py` 模式；只要"点图标跳过去"则 A/E 足够。

---

## 8. 复现与核对

```bash
# 1) 官方文档镜像
git clone https://github.com/ckcoding/fnnas-docs
grep -n "service_port" docs/core-concepts/manifest.md

# 2) fnpack 1.2.3 实测
curl -LO https://static2.fnnas.com/fnpack/fnpack-1.2.3-windows-amd64
./fnpack.exe create demo && cd demo && ../fnpack.exe build
tar tzf demo.fpk        # 看容器成员
md5sum app.tgz          # 与 manifest 的 checksum 比对

# 3) 社区语料统计（186 包）
git clone https://github.com/conversun/fnos-apps
python - <<'EOF'
import json,glob,re
for c in glob.glob("apps/*/fnos/ui/config"):
    t=re.sub(r'(?m)^\s*//.*$','',open(c).read())
    print(c, list(json.loads(t)[".url"]))
EOF
```

---

## 9. 未证实 / 必须由人拍板的点

1. **应用中心安装到底校验什么**：是否强制 `checksum`、有无签名链。官方零说明；若依赖"本地安装"分发，这是安全与合规的第一道未知。
2. **`install-fpk`/`install-local` 在 1.1.31xx 是否可用**：官方说可用、社区说被移除、watchcow/fnos-apps-store 说能用。三者冲突，必须实机验证；它决定"另一个应用运行时装包"是否长期可行。
3. **入口 `url` 能否写绝对外部 URL**：官方定义为路径、无样例、未实测；若可行，方案空间完全改写。
4. **多入口是否真产生多个桌面图标**：官方无明文、语料 0 样例；影响"一个 fpk 多个 Web 入口"的交互设计。
5. **手机 App 是否对所有入口一律 WebView iframe**：仅社区经验；若为真，移动端绕不开跨源 cookie 与 XFO。
6. **fnOS 桌面 iframe 是否设 CSP `frame-src`**：未见文档；若桌面自身 CSP 收紧，会额外限制可嵌域名。
7. **`micro_app=true` 的正确位置**（manifest 还是入口 `microApp`）：官方 API 文档与社区样例写法不一致。
8. **`service_socket` / `port-config` / `api-scope` 等未文档化字段的稳定性**：社区事实标准，随版本可能变。
9. **fnOS 是否有未公开的内置反代**：帮助中心与开发者文档均无，但不能排除系统设置内的隐藏能力。
