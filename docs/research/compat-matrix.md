# 真实站点兼容性矩阵（#14）

- 日期：2026-11-17
- 被测实现：main @ 之前的 `2c4be6c` + 本轮 WebSocket 修复
- 被测入口形态：代理应用走独立 TCP 端口，`proxy.Options{Root:true}`，入口前缀为空，即整站同源反代
- 规格对照：`docs/specs/proxy-bundler.md` §7（连接语义）、§11.4（真机验收）、§13（已知限制）

## 1. 方法

本地起一个 `httptest` 服务器直接挂 `internal/proxy` 的 `Handler`（与包内 `cmd/proxy --mode serve` 同一条代码路径），用带 cookie jar 的 HTTP 客户端从"浏览器视角"逐项访问：

| 检查项 | 做法 |
|---|---|
| 首页 | `GET /`，记录状态码、Content-Type、字节数；不跟随 3xx |
| 静态资源 | 从首页 HTML 里取最多 3 个以 `/` 开头的 `.js/.css/.png/.svg/.ico/.woff2` 引用，逐个经代理取，记录状态码/类型/字节 |
| 指向自身 origin 的绝对 URL | 统计首页 HTML 里出现的目标 host 绝对 URL 数量（这些不会被改写，点击即离开代理） |
| Cookie | 记录响应里的 Set-Cookie，检查是否仍带 `Domain=`、`Secure`/`HttpOnly`/`SameSite` 是否保留 |
| 缺失路径 | `GET /__daili_compat_missing__`，看上游 404 是否原样透出、是否混入代理自己的错误页 |
| 直连模式 | 同一目标用 `mode=direct` 再跑一次，断言 302 且 `Location = 目标 origin + 原始 path/query` |
| 错误页 | 把目标指向 `127.0.0.1:1`，确认 502 页面只有状态码 + `X-Request-Id`，没有目标/上游/堆栈 |

行为替身（local stand-in）：Home Assistant / qBittorrent / Jellyfin 这类只能在你局域网跑的 app，用一个复刻其关键行为的小服务器代替（`127.0.0.1` + `allow_loopback=true`），验证同样的连接语义。它们是**行为复刻，不是真实站点**，真机结果仍待 NAS 上补。

## 2. 真实公网站点

测试主机出网必须经过本机 `127.0.0.1:10808` 代理（Windows WinINET 设置），Go 直连部分站点会超时；`duckduckgo.com` 即因此 502，属测试环境问题，不计入结论。NAS 侧直连正常，不受这条影响。

| 站点 | 类型 | 首页 | 静态资源 | 自身 origin 绝对 URL | Cookie | 缺失路径 | 结论 |
|---|---|---|---|---|---|---|---|
| github.com | 大型 SPA/多子域资源 | 200，525,005 B | 首页资源在 `github.githubassets.com`，本站仅 `/` 资源，未取到 | 94 处 | 3 个：`_gh_sess`/`_octo`/`logged_in`，无 Domain，Secure/HttpOnly/SameSite 保留 | 上游 404 原样透出 | 反代可用；但页面里 94 处自指绝对 URL 会把浏览器送回 github.com |
| www.python.org | 传统服务端渲染 | 200，51,956 B | `/static/js/libs/modernizr.js` 200、`/static/stylesheets/style.*.css` 200、`mq.*.css` 200 | 8 处 | 无 | 上游 404 原样透出 | **完全可用**，绝对路径资源全通过 |
| grafana.com | Next.js 静态站 | 200，714,178 B | 首页 1 MB 内未见 `/...` 形式资源，未取到 | 62 处 | 1 个：`NEXT_LOCALE`，无 Domain | 上游 404 原样透出 | 反代可用；自指绝对 URL 较多 |
| www.home-assistant.io | 文档站（HA 官网） | 200，90,608 B | `/images/favicon.ico` 200、`favicon.svg` 200、`favicon-192x192.png` 200 | 4 处 | 无 | 上游 404 原样透出 | **完全可用** |
| jellyfin.org | Docusaurus | 200，586,048 B | `/images/favicon.ico` 200、`/assets/css/styles.*.css` 200、`runtime~main.*.js` 200 | 11 处 | 无 | 上游 404 原样透出 | **完全可用** |
| www.cloudflare.com | Astro 静态站 | 200（读取截断在 1 MiB） | `/icons.svg` 200（624,815 B）、`/favicon.ico` 200、`/_astro/*.js` 200 | 22 处 | 5 个：`_ga`/`cfz_*`/`kndctr_*`/`__cf_bm`，均无 Domain，Secure/HttpOnly 保留 | 上游 301 原样透出 | 反代可用；大文件与大 Cookie 集正常 |
| gitlab.com | GitLab | **301 → `https://about.gitlab.com/`** | — | — | 1 个：`_cfuvid`，无 Domain | `/missing` → 302 | 入口自己把用户送到**另一个 origin**；跨 origin 的 Location 不改写，浏览器直接离开代理 |
| duckduckgo.com | SPA | 502（测试机出网被拦） | — | — | — | 502 | 环境问题，不计入结论 |
| 任意目标 → `127.0.0.1:1` | 错误页 | 502 | — | — | — | — | 错误页只有 `502` + `Upstream request failed` + `X-Request-Id`，不含目标、上游、堆栈 |

直连模式（`mode=direct`）对以上每个目标都是 `302`，`Location` 等于「目标 origin + 原始 path/query」，例如 `/ui/index.html?a=1` → `https://github.com/ui/index.html?a=1`。

## 3. 行为替身（复刻 LAN 应用的关键行为）

| 替身 | 复刻的行为 | 结果 |
|---|---|---|
| Home Assistant | 首页嵌 `/static/app.js`（绝对路径）、`/api/websocket` 同源 WS、响应带 HSTS 与 Alt-Svc | 首页 200；`/static/app.js` 200；**WS 握手 101**；HSTS/Alt-Svc 已被剥掉（响应头为空） |
| qBittorrent | 未登录 `/` → 302 `/login`（相对 Location）；登录 POST 下发 `SID`（Path=/、HttpOnly） | 302 的 Location 原样是 `/login`；`/login` 200；POST 后 302 `/`，`SID=ok; Path=/; HttpOnly`（无 Domain）；带 cookie 再取 `/` 得到 dashboard 200 → **登录闭环成立** |
| Jellyfin | `/videos/movie.mp4` 支持 Range | `Range: bytes=100-199` → **206**，`Content-Range: bytes 100-199/262144`，长度 100 |
| 强制 HTTPS 的站点 | `/` → 301 `https://secure.example.test/` | Location 不改写（跨 scheme+host），浏览器直接去 https 站点 → 用户**离开代理** |
| SSE | `text/event-stream`，每 80ms 一个事件，共 5 个 | 首字节 0.00s 到达（流式未缓冲），总 45 B |
| 大文件 | 8 MiB `application/octet-stream` | 8,388,608 B 全量读完，无截断 |
| 同 origin 绝对 Location | `/docs` → 301 `http://<target>/docs/` | 改写成 `Location: /docs/`，浏览器仍留在代理 origin |
| 代理自身错误页 | 目标不可达 | 502，仅状态码 + `X-Request-Id` |

## 4. 本轮发现与修复

### 4.1 WebSocket 在 TCP 入口不可用（已修）

替身第一次跑出 `ws=400 Bad Request`，上游收到的是 `expected upgrade`——即 `Upgrade`/`Connection` 头没到目标。

根因：`httputil.ReverseProxy` 在调用 `Rewrite` **之前**已经剥掉 hop-by-hop 头，并按客户端请求把 `Connection: Upgrade`/`Upgrade: websocket` 加回去（`reverseproxy.go:469-485`）；而 `internal/proxy/handler.go` 的 `rewrite` 又调了一次 `removeHopHeaders`，把刚加回来的两个头删掉。

修法：删掉 `rewrite` 里多余的 `removeHopHeaders` 调用（连带删掉这个函数）。回归测试 `TestReverseProxyUpgradesWebSocket`（原始 TCP 握手断言 101），`TestReverseRewriteAndResponse` 的 `Te` 断言改为允许 `trailers`（ReverseProxy 有意转发）。

这条推翻了 `docs/specs/proxy-bundler.md` 之前"WS 支持但真机未验"的状态，现在至少有代码级证据。

### 4.2 自指绝对 URL 会把用户送出代理

首页 HTML 里指向目标 host 的绝对 URL（github 94 处、grafana 62 处、cloudflare 22 处）不改写——这是 T5/规格 §13-6 的既定取舍（不做 HTML 改写）。后果：用户点这类链接后 URL 变成 `https://github.com/...`，登录态分裂成两份（代理 origin 一份、目标 origin 一份）。对 HA/qBittorrent 这类前端大量相对路径的 app 无影响；对多子域大站影响明显。

### 4.3 跨 origin 重定向会让用户离开入口

gitlab.com 直接 301 到 about.gitlab.com；强制 HTTPS 的站点 301 到别的 scheme/host。规格只改同 origin 的绝对 Location，所以这些情况浏览器会跳走。属于预期行为，但要在矩阵里写清楚，避免误判为 bug。

## 5. 与规格 §13 的对应

| 矩阵现象 | 规格 §13 条目 |
|---|---|
| 自指绝对 URL 不改写、点击离开代理 | §13-6（已改写为"页面内指向自身 origin 的绝对 URL 不改写"）+ §13-13 |
| 跨 origin 3xx 不改写、用户离开入口 | §13-14（新增） |
| CGI 入口 Upgrade 501、TCP 入口 WS 可用 | §13-2（已按本轮结果补"TCP 入口实测 101"） |
| 强制 HTTPS 站点的 HSTS/Alt-Svc 被剥 | §7.3 / §13-5（混合内容与 HTTPS 入口说明） |
| 端口需放行、占用需重建 | §13-3 |

## 6. 仍待 NAS 真机补的项

以下只能在你的飞牛上跑（本文档的"真实站点"是公网站点，不是你的 LAN 应用）：

1. **Home Assistant 真机**：`http://192.168.123.200:8121` 走反代，验证登录、`/api/websocket` 实时状态、Lovelace 卡片资源。
2. **qBittorrent 真机**：登录 302 → `/login`、SID cookie、种子列表里的 WebSocket 推送。
3. **Jellyfin 真机**：播放长视频（Range + 拖动进度）、封面图、字幕。
4. **Basic Auth**：401 挑战、错误口令、WS 握手是否同样 401。
5. **手机飞牛 App 打开入口**：WebView 下的表现（社区经验说 url/iframe 都是 WebView，未实测）。

补齐后把结果追加到第 3 节，失败项按 §5 的规则回填规格 §13 或开票。
