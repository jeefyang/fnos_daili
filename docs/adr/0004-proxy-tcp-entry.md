# ADR 0004：代理应用恢复独立 TCP 端口，CGI 只留给托管台

- 状态：accepted
- 日期：2026-11-16
- 取代：ADR 0001 中“代理应用也走 CGI”的部分
- 相关：T1 #7、T6 #8、docs/specs/proxy-bundler.md §3.2/§7.3/§11.4

## 背景

ADR 0001 把所有应用入口统一改成 `/cgi/ThirdParty/<appname>/index.cgi/`，理由是 watchcow 这么做、同源、不占端口。实现代理运行时后暴露出一个未重新论证的缺口：

- CGI 入口的浏览器 origin 是飞牛的 origin，路径带固定前缀。
- 目标页若用绝对路径（`/static/app.js`、`/api/...`），浏览器会请求 `https://<fnos-host>/static/app.js`，落到飞牛根路径，而不是代理前缀下。
- 规格明确不做 HTML/绝对 URL 改写（§13 第 6 条）。因此 CGI 入口只对“全部使用相对路径”的站点可用，与“代理任意 ip:port”的产品目标冲突。
- 独立端口下代理自己就是 origin，绝对路径天然成立，零改写；代价是端口分配与回收。

## 决策

- 代理应用使用独立 TCP 端口：生成时从 20000–29999 分配，写 manifest `service_port` 与 `ui/config.port`，入口 `url=/`、`type=url`。
- 代理进程以 `--mode serve --listen 0.0.0.0:<port>` 监听；入口 URL 为 `http://<fnos-host>:<port>/`。
- 托管台 `daili.manager` 继续走 CGI：它只需要一个受控的单页管理界面，不存在目标站绝对路径问题，且避免再占一个入口端口。
- 反代层在独立端口下不再加 CGI 前缀：Cookie `Path` 归一到 `/`，同源 `Location` 改成入口根路径下的路径。
- WebSocket 在代理端口上恢复支持；托管台 CGI 收到 Upgrade 仍返回 501。

## 后果

- 恢复端口分配、bind 预检、端口回收；删除目标时释放端口，对账把手工卸载的应用标 missing 并释放。
- 入口是 HTTP；HTTPS 桌面跳到 HTTP 端口是顶层导航，不受混合内容限制。
- 端口需从局域网可达；飞牛防火墙若启用需放行 20000–29999（未实测）。
- 代理不再依赖 CGI 网关的路径前缀，目标站点的绝对路径、重定向、Cookie 都按普通反代处理。
- 托管台与代理的入口实现分叉：托管台保留 CGI shim 与 501，代理走 TCP 与 Upgrade。

## 被否方案

- **继续 CGI + HTML/绝对 URL 重写**：要把 HTML、CSS、JS、JSON 里的绝对 URL 全部改写，任意站点极易碎，且与规格的 Out of scope 冲突。
- **继续 CGI + 要求目标站用相对路径**：只覆盖一部分站点，不满足“代理任意 ip:port”。
- **统一网关**：仍未在真机验证，不作为产品入口。
