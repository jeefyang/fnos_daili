# watchcow 的 CGI 入口模型

- 日期：2026-11-16
- 源码：`tf4fun/watchcow`，commit `7dbe85ad4616d156cab5315a85eef696826cd19c`
- 本地：`.tmp-research/watchcow/`
- 结论：watchcow 生成的应用和它自己的托管台都不使用 fnOS 统一网关；入口是 `/cgi/ThirdParty/<appname>/index.cgi/`。

## 1. 生成应用的入口如何改写

`internal/fpkgen/template.go:141-155`：

```go
if entry.Redirect != "" {
    port = ""
    entryName := entry.Name
    if entryName == "" {
        entryName = "_"
    }
    cgiPath := "/cgi/ThirdParty/" + data.AppName + "/index.cgi/redirect/" + data.AppName + "/" + entryName
    if entry.Path != "" && entry.Path != "/" {
        cgiPath += entry.Path
    }
    urlPath = cgiPath
}
```

生成的 `app/ui/config` 中，该入口不再有 `port`，`url` 是上面的 CGI 路径；`type` 仍由 `ui_type` 决定（`url` 或 `iframe`）。

## 2. CGI 脚本从哪来

`internal/fpkgen/templates/cmd_install_callback.tmpl:3-9`：

```bash
cat > "${TRIM_APPDEST}/ui/index.cgi" << EOFCGI
#!/bin/bash
exec "${TRIM_APPDEST}/watchcow" --mode cgi --socket "${TRIM_PKGVAR}/watchcow.sock"
EOFCGI
chmod +x "${TRIM_APPDEST}/ui/index.cgi"
```

即：安装后由 `cmd/install_callback` 写入 CGI shim；`ui/index.cgi` 不是二进制，是一行 bash，把请求交给同一个二进制的 CGI 模式。

## 3. CGI 模式如何转发

`internal/cgi/handler.go:20-39`：

- 用标准库 `net/http/cgi.Serve`。
- 从 `r.URL.Path` 中找到 `index.cgi`，截掉它及之前的部分，剩余路径（如 `/containers/xxx`）作为后端路径；空则补 `/`。
- 改写 `r.URL.Path` / `r.RequestURI` 后交给 `httputil.ReverseProxy`。

`internal/cgi/handler.go:43-63`：

- 目标 URL 只是占位 `http://localhost`。
- `http.Transport.DialContext` 固定 `net.Dial("unix", socketPath)`。
- 守护进程未启动时返回 503 HTML。

## 4. manifest 因此没有 service_port

`internal/fpkgen/template.go:335-362` 选择默认启动入口：`entry.Redirect != ""` 时把 `defaultLaunchPort` 置空。`templates/manifest.tmpl:12-18` 只有 `DefaultLaunchEntry` 非空才写 `desktop_uidir`/`desktop_applaunchname`，只有 `Port` 非空才写 `service_port` + `checkport=false`。所以 CGI 应用没有 TCP 端口，也没有 `service_port`。

## 5. watchcow 自己的 dashboard 也走 CGI

`fnos-app/app/ui/config`：

```json
{
  ".url": {
    "watchcow.dashboard": {
      "title": "watchcow",
      "icon": "images/icon-{0}.png",
      "type": "iframe",
      "protocol": "http",
      "url": "/cgi/ThirdParty/watchcow/index.cgi/",
      "allUsers": false,
      "noDisplay": true
    }
  }
}
```

`fnos-app/manifest` 没有 `service_port`，有 `desktop_uidir=ui`、`desktop_applaunchname=watchcow.dashboard`、`ctl_stop=true`、`checkport=false`、`install_type=root`；`config/privilege` 是 `run-as: root`（因为它要调 appcenter-cli）。`fnos-app/cmd/install_callback` 同样生成 `ui/index.cgi`；`cmd/main` 启停 watchcow 守护进程并写 pid。

## 6. 对本项目的含义

1. 托管台和生成的代理应用都可以用同一种入口：`/cgi/ThirdParty/<appname>/index.cgi/`；不需要 `/app/<appname>`、`gatewayPrefix`、`gatewaySocket`，也不需要为入口分配 TCP 端口。
2. 代理应用的结构可以简化为：
   - `cmd/main`：启动 `app/bin/proxy --mode server --socket ${TRIM_PKGVAR}/proxy.sock`。
   - `cmd/install_callback`：写 `app/ui/index.cgi`，内容 `exec "${TRIM_APPDEST}/bin/proxy" --mode cgi --socket "${TRIM_PKGVAR}/proxy.sock"`。
   - `app/ui/config`：一个入口，`type=url` 或 `iframe`，`url=/cgi/ThirdParty/<appname>/index.cgi/`，无 `port`。
   - `manifest`：无 `service_port`，保留 `desktop_uidir=ui` 与 `desktop_applaunchname=<appname>.main`。
3. 代理的 CGI 模式与 watchcow 一样，把 `index.cgi` 之后的路径透传给 Unix socket 上的常驻进程；区别是我们不探测端口，反代模式直接转发到 `config.json` 里的 target，直连模式返回 302。
4. 托管台可以照 watchcow 的 dashboard：`index.cgi` + `allUsers=false` + `noDisplay=true`；但因为 `allUsers` 只是可见性、不是鉴权，管理员认证仍需在 T6 明确（口令会话或头校验）。
5. 不照抄 watchcow 的 `install-local`：本机 V1 已证明它 10237 不可用，安装仍走 `appcenter-cli install-fpk -v <volume> <fpk>`（见 `docs/research/install-chain-verification.md`）。

## 7. 与旧决议的冲突

- T1 原决议“独立端口 20000–29999 + 入口 path=/ + 新标签页”被本模型取代；保留的是“每包内嵌通用二进制 + config.json、一应用一入口、直连 302、目标只收 origin、运行身份 package”。
- T3 原决议“托管台只走统一网关 + X-Trim-Isadmin”被本模型取代；保留的是“只监听 Unix socket、不监听 TCP、allUsers=false、CSRF、代理侧 Basic Auth/目标拦截”。
- #11“验证统一网关入口与管理员头”不再对应产品入口，按 out of scope 关闭；已完成的 A 包事实（`service_port=0` + `service_socket=app.sock` 可安装/启动）保留在 `docs/research/gateway-verification.md`，但不再作为路线。
