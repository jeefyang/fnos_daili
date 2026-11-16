# GLOSSARY

本仓的领域术语。只收“是什么”，不收实现与决策。

## 托管台 (Manager)

装在飞牛上、提供表单界面并负责生成与安装代理应用的那个 fpk 应用。它同时是工具本体和管理界面；入口走 `/cgi/ThirdParty/<appname>/index.cgi/`，进程只监听 Unix socket、不直接对局域网监听 TCP，`all_users=false`；管理员认证方式见规格。

## 代理应用 (Proxy App)

托管台为一个目标生成并安装的独立 fnOS 应用。一个代理应用对应一个目标、一个 `appname`、一个入口，包内嵌同一份通用反代二进制与 `config.json`；入口走独立 TCP 端口，常驻进程直接监听该端口，不经过 CGI。默认仅管理员可见，可选 HTTP Basic Auth；可见性不是鉴权。

## 目标 (Target)

代理应用要转发的对端，形如 `scheme://host[:port]`；`host` 可为 IPv4、域名或 IPv6 字面量，不含 path / query / fragment。协议限定 `http` / `https`，它是任意地址，不必是飞牛本机的容器。反代模式下，目标在建立出站连接前要过目标拦截。

## 入口 (Entry)

fnOS 应用在桌面或应用中心暴露出来的打开方式，带标题、图标、`ui_type`、`all_users`、`url` 等字段。沿用 watchcow 的入口模型；fnOS 允许一个应用有多个入口，本项目每个代理应用只生成一个。代理应用的入口 `url` 是 `/`、`port` 是分配到的独立端口；托管台的入口 `url` 是 CGI 路径。两者都不指向目标地址。

## 应用子域 (fnDomain)

FN Connect 公网访问时，每个应用入口在外网暴露的独有子域：`https://<fnDomain>.<当前域名><path>`。`fnDomain` 由入口键（`desktop_applaunchname`）小写、点换成连字符得到，默认入口名 `Application` 省略；本项目入口键固定 `<appname>.main`，故外网地址为 `https://<appname 点→连字符>-main.<当前域名>/`。它由飞牛服务端下发，需要应用在 FN Connect 里发布；登录态由 FN Connect 校验，应用自己仍按入口可见性与认证决定是否放行。

## 可见性 (all_users)

入口字段，决定哪些飞牛用户能在桌面看到图标；`false` 仅管理员可见，`true` 所有登录用户可见。它不控制入口端口或 CGI 路径的可达性，也不是登录或鉴权。

## CGI 入口 (CGI entry)

fnOS 入口的一种运行方式：`app/ui/config` 的 `url` 写成 `/cgi/ThirdParty/<appname>/index.cgi/`，`port` 留空；安装时 `cmd/install_callback` 在 `TRIM_APPDEST/ui/index.cgi` 写一个可执行 shim，把请求 exec 给包内二进制的 `--mode cgi --socket <TRIM_PKGVAR>/...`。二进制从路径里截掉 `index.cgi` 及之前的部分，把剩余路径经 Unix socket 交给常驻进程。它同源、不占入口 TCP 端口，但自身不带 SSO；本项目只有托管台用它。

## 网关入口 (Gateway entry)

飞牛入口的一种运行方式：桌面请求经 `/app/<appname>/` 统一网关转发到应用自己的 Unix socket，网关先校验飞牛登录态并注入用户/管理员头。本项目不采用它（托管台用 CGI 入口，代理应用用独立端口），保留此条仅作对照。

## 反代模式 (Reverse mode)

浏览器打开代理应用自己的端口，常驻进程在服务端把整站请求转发给目标；浏览器只与 `http://<fnos-host>:<port>` 这个 origin 交互。默认模式，受目标拦截约束。

## 直连模式 (Direct mode)

代理应用收到入口请求后返回 302，把浏览器顶层导航到目标地址，后续流量由浏览器与目标直连。用于浏览器能直达目标、且必须由目标自己处理登录或回调的场景；目标不可达时表现为浏览器自身的错误页。它不经过 NAS 出站，回环地址指的是浏览器所在机器。

## 代理认证 (Proxy auth)

代理应用可选开启的 HTTP Basic Auth；只在反代模式生效，未认证请求返回 401。Basic 口令的传输安全性取决于飞牛入口本身是 HTTP 还是 HTTPS。

## 目标拦截 (Target guard)

反代模式建立出站连接前的检查：默认拒绝回环、链路本地、未指定与多播地址，以及飞牛管理端口和预留端口段；每个应用可用“允许本机目标”开关放行回环，但保留端口拒绝。

## 端口 (Port)

代理应用的入口端口：生成时从 `20000–29999` 自动分配，写进 manifest 的 `service_port` 与入口 `port`，运行时以 `TRIM_SERVICE_PORT` 为准；一个应用一个端口，删除应用时释放。该段仍同时是目标拦截要拒绝的预留段，防止代理链回自己。

## 应用标识 (appname)

代理应用在 fnOS 里的唯一包名，3–32 位，只含字母、数字与 `.` `_` `-`，首字符须为字母或数字；由用户填写，首次安装后不可更改，是安装目录与入口 ID 的根。

## 生成 (Build)

把一份目标与入口配置变成可安装的 fpk 的过程。

## 安装 (Install)

把生成的 fpk 交给飞牛应用中心并装上的过程，走 `appcenter-cli install-fpk -v <volume> <fpk>`。已安装时 `install-fpk` 拒绝而不是升级；升级 = 先 `uninstall <appname>` 再安装。

## 升级 (Upgrade)

重新生成代理 fpk，先卸载旧应用再安装新包；失败时用上次可用包回滚。因为配置烤在包内，编辑配置与升级是同一个动作。

## 托管台状态 (Manager state)

托管台为每个目标保存的期望状态（appname、版本、安装卷、包路径、最近结果等）。实际状态以飞牛应用中心的已安装列表为准；两者不一致时以实际为准对账，不静默接管。

## 上次可用包 (Last-known-good)

最近一次安装后达到 `running` 的代理 fpk。升级或重装失败时用它回滚；没有它就没有自动回滚的落点。

## 未纳管应用 (Unmanaged app)

飞牛应用中心里存在、但托管台状态没有记录的代理应用。列表只读展示，不自动接管、不自动卸载。

## 丢失 (Missing)

托管台有记录、但应用中心里已不存在对应应用的代理应用。保留记录，提供重装或删除，不把记录当成已安装。

## 入口前缀 (Entry prefix)

CGI 入口的固定路径前缀 `/cgi/ThirdParty/<appname>/index.cgi`。托管台的 CGI 层用它截路径；代理应用走独立端口，入口前缀为空，不参与 Cookie/Location 改写。

## 操作 (Operation)

托管台发起的一次异步变更，取值为 `install`、`upgrade`、`reinstall`、`uninstall`、`rollback`、`start` 之一。记录在托管台状态的 `last_operation` 里，界面按 operation id 轮询进度；同一目标同时只允许一个变更操作。
