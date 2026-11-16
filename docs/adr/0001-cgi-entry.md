# ADR 0001：所有应用入口走 CGI，不使用独立端口或统一网关

- 状态：superseded（代理应用部分被 ADR 0004 取代；托管台仍按本 ADR 走 CGI）
- 日期：2026-11-16
- 相关：T1 #7、T3 #9、#11、ADR 0004、docs/research/watchcow-cgi-entry.md、docs/research/fnos-fpk-and-embedding.md

> 2026-11-16 更正：代理应用不再走 CGI，恢复独立 TCP 端口（ADR 0004）。本文保留托管台 CGI 的决策与背景。

## 背景

项目早期（T1）决定每个代理应用监听一个自动分配的 TCP 端口，入口 `type=url`、`path=/`，理由是零改写、目标页相对路径天然成立。T3 又决定托管台走飞牛统一网关 `/app/<appname>/`，以便拿到同源与管理员头。

真机与源码复核推翻了这两条：

- watchcow 生成的应用和它自己的 dashboard 都走 `ui/index.cgi`：`internal/fpkgen/template.go:141-155` 把入口 url 写成 `/cgi/ThirdParty/<AppName>/index.cgi/redirect/...`；`internal/fpkgen/templates/cmd_install_callback.tmpl:3-9` 写 shim；`internal/cgi/handler.go:20-39` 用 `net/http/cgi.Serve` 把请求转给 Unix socket。
- #11 的 A 包证明 `service_port=0 + service_socket=app.sock` 可以安装并启动，但浏览器入口没有打通；继续押统一网关会把产品入口绑在未验证的网关上。
- CGI 入口同源、不占入口 TCP 端口、不需要端口分配器；代价是不支持 WebSocket、可能被网关缓冲 SSE、路径带 `/cgi/ThirdParty/<appname>/index.cgi` 前缀。

## 决策

托管台与所有代理应用都只使用 CGI 入口：

- `app/ui/config` 的 `url` 固定 `/cgi/ThirdParty/<appname>/index.cgi/`，`port` 空串。
- manifest 不写 `service_port`，`checkport=false`。
- `cmd/install_callback` 在 `TRIM_APPDEST/ui/index.cgi` 写入 shim，exec 包内二进制 `--mode cgi --socket <TRIM_PKGVAR>/<app>.sock`。
- 常驻进程只监听 Unix socket，不监听 TCP。
- 反代层负责把 Cookie `Path` 和同源 `Location` 加上 CGI 前缀。

## 后果

- 不再需要端口分配、端口 bind 预检、`TRIM_SERVICE_PORT`。
- 反代必须处理前缀：Cookie Path、Location、Origin/Referer；否则目标站点的跳转和会话会坏。
- WebSocket 在 CGI 下不可用：收到 `Upgrade` 返回 501；直连模式是兜底。
- SSE 是否被应用中心缓冲未实测；代理侧用 `FlushInterval=-1`，但不能保证上游网关不缓冲。
- 入口 URL 变长；所有文档、界面、测试都以 CGI 路径为准。

## 被否方案

- **独立端口**：要端口分配与回收，HTTPS 桌面跳 HTTP 端口是顶层导航但仍是额外攻击面；T1 的 20000–29999 只保留为目标拦截段。
- **统一网关**：同源、SSO、WebSocket 都更好，但本机未验证通，且需要 `service_socket`/`gatewayPrefix`/`gatewaySocket` 与管理员头；不作为 v1 产品入口。
