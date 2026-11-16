# WatchCow 的 fnOS fpk 生成机制调研

**本次调研回答什么**：WatchCow（tf4fun/watchcow）如何把带 `watchcow.enable=true` 的 Docker 容器转换成一个可被 `appcenter-cli install-local` 安装的 fnOS 原生应用包（fpk）——包括包的目录与 manifest 字段、应用/入口的数据模型与 label 解析规则、"URL 重定向"入口的真实实现方式、安装与生命周期的调用链和权限要求，以及实际使用中的限制与坑。

> 版本锚点：commit `7dbe85ad4616d156cab5315a85eef696826cd19c`（本地浅克隆 `.tmp/watchcow`）。文中 `path:line` 均为该 commit 下的行号，permalink 前缀为
> `https://github.com/tf4fun/watchcow/blob/7dbe85ad4616d156cab5315a85eef696826cd19c/`。
> 结论只来自源码与仓库内 README/CI 配置；凡属推断处均显式标注"（推断）"。

---

## 1. 生成的 fpk 里到底有什么

### 1.1 目录布局（生成期）

生成器先把临时目录骨架建出来（`internal/fpkgen/generator.go:267-282`：`createDirectoryStructure` 创建 `<tmp>/`、`app/ui/images`、`cmd`、`config`），再按固定映射表渲染模板（`internal/fpkgen/generator.go:128-176`，`generateFromTemplates`）：

```text
<temp-dir>/                     ← os.MkdirTemp("", "watchcow-"+AppName+"-")  (generator.go:49-89)
├── manifest                    ← templates/manifest.tmpl        0644
├── ICON.PNG                    ← 64x64   PNG（默认入口图标）    (icons.go:25-113)
├── ICON_256.PNG                ← 256x256 PNG（默认入口图标）
├── LICENSE                     ← templates/LICENSE.tmpl (MIT)   0644
├── app/
│   └── ui/
│       ├── config              ← GenerateUIConfigJSON()          0644
│       └── images/
│           ├── icon_64.png     ← 默认入口
│           ├── icon_256.png
│           ├── icon_<entry>_64.png   ← 命名入口（每入口一对）
│           └── icon_<entry>_256.png
├── cmd/
│   ├── main                    ← templates/cmd_main.tmpl        0755
│   ├── install_callback        ← templates/cmd_install_callback.tmpl 0755
│   ├── install_init            ┐
│   ├── uninstall_init          │
│   ├── uninstall_callback      │ templates/cmd_empty.tmpl（仅 #!/bin/bash + exit 0）
│   ├── upgrade_init            │ 0755
│   ├── upgrade_callback        │
│   ├── config_init             │
│   └── config_callback         ┘
└── config/
    ├── privilege               ← {"defaults":{"run-as":"root"}}  0644
    └── resource                ← {}                              0644
```

要点：

* **没有 `callback/` 目录，也没有 `etc/` 目录，也没有 `wizard/`。** 仓库里不存在这三个名字的目录；生成器只写上面这些文件。install/uninstall/upgrade/config 的四个钩子全部落在 `cmd/` 下，且除 `install_callback` 外都是空脚本（`cmd_empty.tmpl`，见 `generator.go:152-175`）。
* `app/ui/images/` 里的图标文件名带 `{0}` 占位概念：模板里写的是 `images/icon_{0}.png`（`internal/fpkgen/template.go:280-284`），实际落盘是 `_64` / `_256` 两档，由 fnOS 侧按 DPI 代入（对照 watchcow 自身包的 `fnos-app/app/ui/config`：`icon: images/icon-{0}.png`）。
* 包根只放**默认入口**的 `ICON.PNG`（64）与 `ICON_256.PNG`（256）；若配置里没有默认入口，则取第一个入口的图标（`internal/fpkgen/icons.go:25-113`，`handleIcons`）。
* 生成目录是临时目录，安装完即删除；`GenerateFromContainer` 返回 `(config, appDir)`，由调用方 `RemoveAll`（`generator.go:49-89`、`internal/docker/monitor.go:972-1003`）。

### 1.2 manifest 格式与全部字段

模板原文（[manifest.tmpl](https://github.com/tf4fun/watchcow/blob/7dbe85ad4616d156cab5315a85eef696826cd19c/internal/fpkgen/templates/manifest.tmpl)，19 行，逐字）：

```text
appname={{.AppName}}
version={{.Version}}
display_name={{.DisplayName}}
desc={{.Description}}
arch=x86_64
platform=all
source=thirdparty
maintainer={{.Maintainer}}
distributor={{.Maintainer}}
os_min_version=0.9.0
install_type=root
{{- if .DefaultLaunchEntry}}
desktop_uidir=ui
desktop_applaunchname={{.DefaultLaunchEntry}}
{{- if .Port}}
service_port={{.Port}}
checkport=false
{{- end}}
{{- end}}
```

逐字段含义与来源：

| 字段 | 值 / 来源 | 说明 |
|---|---|---|
| `appname` | `AppConfig.AppName`（label `watchcow.appname` 或默认 `watchcow.<容器名>`） | fnOS 应用唯一标识，3–32 ASCII（见 §2.3） |
| `version` | label `watchcow.version`，默认 `1.0.0` | `generator.go:190-247` |
| `display_name` | label `watchcow.display_name`，默认容器名美化（`PrettifyName`，`generator.go:432-446`） | |
| `desc` | label `watchcow.desc`，默认 `Docker container: <image>` | 只把换行替换成空格（`escapeForTemplate`，`template.go:374-378`），HTML 会原样进入 manifest |
| `arch` | 硬编码 `x86_64` | arm 构建时 CI 删除此行（`.github/workflows/build.yaml:42-48`） |
| `platform` | 硬编码 `all` | 同上，arm 时改写 |
| `source` | `thirdparty` | 第三方应用 |
| `maintainer` / `distributor` | 同一个 label `watchcow.maintainer`，默认 `WatchCow` | distributor 直接复制 maintainer |
| `os_min_version` | `0.9.0` | |
| `install_type` | `root` | **安装与运行需要 root**（§4.3） |
| `desktop_uidir` | `ui`，仅当有 `DefaultLaunchEntry` | |
| `desktop_applaunchname` | 默认启动入口的 full name（`appname` 或 `appname.entry`） | 桌面双击启动哪个入口 |
| `service_port` / `checkport=false` | 仅当该入口 `Port` 非空 | **redirect 入口的 port 为空 → 不写这两行**（`template.go:335-362`） |

与 watchcow 自身的包相比，生成包**没有** `maintainer_url`、`distributor_url`、`ctl_stop`、`privileged` 这些字段（对照 `fnos-app/manifest`）。CI 在 tag 构建时用 `sed` 改写 `fnos-app/manifest` 的 version（`.github/workflows/build.yaml:35-40`）——这只影响 watchcow 自己的包，不影响生成包。

### 1.3 app/ui/config 的 JSON 结构

`internal/fpkgen/template.go:118-173`。结构是 `{ ".url": { <fullName>: {...} } }`：

```go
type UIConfig struct {
	URL map[string]*UIConfigEntry `json:".url"`
}
```

每个入口写入的字段（`template.go:158-169`）：`title`、`icon`、`type`、`protocol`、`port`（omitempty）、`url`、`allUsers`、`fileTypes`（omitempty）、`noDisplay`（omitempty）、`control`（omitempty，内含 `accessPerm`/`portPerm`/`pathPerm`）。用 `json.MarshalIndent(config, "", "    ")`（4 空格）输出（`template.go:172`）。

普通入口：`url=Path`（默认 `/`）、`port=ServicePort`。
重定向入口：`port` 被清空省略，`url` 改成 CGI 路由（见 §3.1）。

### 1.4 fnpack build 的输入/输出约定

* **输入 = 当前工作目录**：`cd fnos-app && fnpack build`，即 fnpack 读取 cwd 下的 `manifest`、`app/`、`cmd/`、`config/`、`ICON*.PNG`、`wizard/`（README.md:294-303；CI `.github/workflows/build.yaml:61-65`）。
* **输出**：当前目录下 `<appname>.fpk`，CI 把 `watchcow.fpk` 重命名为 `watchcow-x86.fpk` / `watchcow-arm.fpk` 并上传 Release（`build.yaml:61-65, 73-91`）。
* fnpack 二进制由 CI 从官方地址下载：`https://static2.fnnas.com/fnpack/fnpack-1.2.0-linux-amd64`（`build.yaml:56-59`）。仓库不 vendor fnpack，也没有 fnpack 的源码/文档副本。
* **生成包给 fnpack 的路径并不是走 fnpack**：`installer.go` 直接把生成目录交给 `appcenter-cli install-local`（§4.1），仓库里没有任何对生成目录调用 `fnpack` 的代码。即"生成目录 → fpk"这一步发生在 fnOS 的 appcenter-cli 内部（**推断**，仓库内无证据）。

---

## 2. 应用 / 入口的数据模型

### 2.1 核心 struct（原样摘录）

`internal/app/app.go:91-183`：

```go
// EntryControl represents permission settings for an entry
type EntryControl struct {
	AccessPerm string // "editable", "readonly", "hidden" - who can access setting
	PortPerm   string // "editable", "readonly", "hidden" - port setting permission
	PathPerm   string // "editable", "readonly", "hidden" - path setting permission
}

// RedirectConfig holds redirect configuration for an entry
type RedirectConfig struct {
	Host          string // External redirect host (e.g., "https://example.com")
	Port          string // Container port to use when on local network
	ForceExternal bool   // Skip local network detection
}

// Entry represents a UI entry point for an app
type Entry struct {
	Name          string        // Entry identifier (empty for default entry)
	Title         string        // Display title
	Protocol      string        // http or https
	Port          string        // Service port
	Path          string        // URL path
	UIType        string        // "url" (new tab) or "iframe" (desktop window)
	AllUsers      bool          // Access permission (true = all users)
	Icon          string        // Icon source: URL (file:// or http://) from labels, or base64 data from dashboard
	FileTypes     []string      // Supported file types for right-click menu
	NoDisplay     bool          // Hide from desktop (only show in right-click menu)
	Control       *EntryControl // Permission control settings
	Redirect      string        // External redirect host for CGI mode
	ForceExternal bool          // Skip local network detection, always redirect to external URL
}
```

`VolumeMapping`（`app.go:134-140`）：`Source`、`Destination`、`ReadOnly bool`、`Type string`（`bind`/`volume`）。
`App`（`app.go:145-183`）：身份五项 `AppName/Version/DisplayName/Description/Maintainer`；容器信息 `ContainerID/ContainerName/Image`；legacy 单入口 `Protocol/Port/Path/UIType/AllUsers`；`Entries []Entry`；`Volumes []VolumeMapping`；`Environment []string`；`Icon string`；`RestartPolicy string`；`Labels map[string]string`；运行时 `Status Status`。

**关键点**：这些 struct **没有任何 json tag**，也**没有 label tag**——它们是内部模型，`internal/fpkgen/types.go` 只是一组类型别名（`App=app.App`、`AppConfig=app.App`、`Entry`、`EntryControl`、`VolumeMapping`）。label 名字到字段的映射是**手写 switch**，不是反射（见 §2.2）。同一模型既服务 fpk 生成，也服务运行时 registry（`app.go:142-144` 注释）。

Dashboard 侧另有一套可 JSON 持久化的镜像结构（`internal/server/types.go:85-116`，字段与类型同源码，行尾中文注释为说明）：

```go
type StoredEntry struct {
	Name          string   // 空 = 默认入口
	Title         string
	Protocol      string
	Port          string
	Path          string
	UIType        string   // "url" 新标签页 / "iframe" 桌面窗口
	AllUsers      bool
	FileTypes     []string
	NoDisplay     bool
	Redirect      string   // External redirect host
	ForceExternal bool     // Skip local network detection, always redirect to external URL
	IconBase64    string
}
```

`StoredConfig`（`types.go:101-116`）另加 `Key ContainerKey`、`AppName`、`Revision`、`Pending`、`Deleting`、`LastError`、`CreatedAt/UpdatedAt`。`ContainerKey` 是 Dashboard 配置的主键，格式 `image|@containerName;containerPort/protocol:hostPort,...`（`types.go:12-38`），端口按容器端口排序序列化（`types.go:40-49`），所以**端口映射一变 key 就变**，旧配置靠 `LegacyConfigKeys` 迁移。

### 2.2 label 解析：`watchcow.<entry>.<field>`

字段白名单在 `internal/fpkgen/generator.go:451-466`（`entryFields`）：

```text
service_port, protocol, path, ui_type, all_users, icon, title,
file_types, no_display,
control.access_perm, control.port_perm, control.path_perm,
redirect, redirect_force_external
```

应用级 label 与保留入口名（`generator.go:468-478`，`reservedEntryNames`）：`enable`、`install`、`install_volume`、`appname`、`display_name`、`desc`、`version`、`maintainer`、`_`。`_` 保留给 CGI 表示"默认入口"，不能作入口名（README.md:148-169 同样声明）。

解析流程：

1. `hasDefaultEntry`（`generator.go:510-517`）：任一 `watchcow.<field>`（不带入口名的扁平 label）非空即为真。
2. `ParseEntries`（`generator.go:591-635`）扫全部 `watchcow.*`，`SplitN(suffix, ".", 2)`；若 `parts[1]` 是 entryField 且 `parts[0]` 通过 `isValidEntryName`（排除保留名与字段根名，只允许 `[a-zA-Z0-9_-]`，`generator.go:486-507`），则视为命名入口。
3. 入口顺序：**默认入口在前**（`hasDefaultEntry` 为真，或完全没有命名入口时），命名入口 `sort.Strings` 后依次追加——排序是为了让 `desktop_applaunchname`（取第一个非 `noDisplay` 入口）在不同 label 迭代顺序下保持确定（`generator.go:591-635`）。
4. 每个入口 `Port` 为空时回填 `defaultPort`（来自 `watchcow.service_port` 或容器首个 host 端口）。

单入口解析 `parseEntry`（`generator.go:523-588`）的默认值：

| 字段 | 默认 | 说明 |
|---|---|---|
| `title` | 默认入口 = `displayName`；命名入口 = `displayName + " - " + name` | |
| `protocol` | `http` | redirect 模式下用于拼内网地址 |
| `path` | `/` | 无前导 `/` 会自动补（`template.go:297-302`） |
| `ui_type` | `url`（新标签页）；`iframe`（桌面窗口） | |
| `all_users` | `true` | 只有字面量 `true` 才是 true；false = 仅管理员 |
| `icon` | 默认入口用整包默认图标；命名入口用 `buildIconURL(name)` | 用户配置 > 本地图标库 > Dashboard Icons CDN（README.md:214-280） |
| `file_types` | 空 | 逗号分隔、去空格 |
| `no_display` | `false` | |
| `control.*` | 三个 perm 任一非空才构造 `EntryControl`（否则 nil） | 各 perm 默认 `readonly`（README.md:144-146） |
| `redirect` | 空 | 非空即开启 CGI 重定向模式 |
| `redirect_force_external` | `false` | 只有 `true` 才为真 |

label 读取统一走 `getLabel`（`generator.go:295-300`）：**label 存在且非空**才覆盖默认值（空字符串等同未设置）。

### 2.3 appname 规则

`internal/app/app.go:21-89`：

* 长度边界 `MinAppNameLength=3`、`MaxAppNameLength=32`（21-22）。
* `SanitizeAppNamePart`（26-42）：转小写、去 `/` 前缀、`_` 换成 `-`、只保留 `[a-z0-9-]`，空则 `app`。
* `DefaultAppName(name)`（45-47）= `watchcow.<sanitized>`；`GeneratedAppName(container, suffix)`（51-57）再拼可选后缀。
* `BoundGeneratedAppName`（61-70）：超过 32 字符时截断并追加 `-` + `sha256[:8]`，保证稳定且 ≤32。
* `DashboardAppName(containerName, hostPort)`（76-89）：如果 sanitize **改变**了容器名（如 `foo_bar` 与 `foo-bar` 会撞成同一个），追加 hostPort 与 8 位 sha256 判别符。
* 写入 manifest 前还有一道 `ValidateAppName`（`generator.go:249-264`）：正则 `^[A-Za-z0-9][A-Za-z0-9._-]*$` 且长度 3..32，错误文案 `invalid appname %q: must be at least %d ASCII characters` / `...at most...` / `...use ASCII letters, numbers, '.', '_' or '-', starting with a letter or number`。**注意大小写**：`SanitizeAppNamePart` 只产小写，但校验正则允许大写——手工写 `watchcow.appname=MyApp` 会通过校验并原样写进 manifest。

### 2.4 多入口如何展开成 manifest 入口定义

`NewTemplateData`（`internal/fpkgen/template.go:240-371`）：

* full name：`fullName = AppName`（默认入口），`AppName + "." + entry.Name`（命名入口）（286-290）。UI config 的 key 就是 full name。
* 图标文件名：`icon_{0}.png` / `icon_<name>_{0}.png`（280-284），EntryData.Icon 写成 `images/+iconFilename`（327）。
* 每个入口的 `protocol`/`path`/`uiType` 再做一次默认（293-306）。
* **默认启动入口选择**（335-348）：优先"无名且非 `noDisplay`"的入口；否则取第一个非 `noDisplay` 的入口；`defaultLaunchPort` 取该入口 Port，**若该入口带 `redirect` 则置空**。
* `data.Port = defaultLaunchPort`，非空才生成 `Ports = [Port:Port]`（358-362）；因此 manifest 的 `service_port` 只描述默认启动入口。
* 任一入口有 redirect → `data.HasRedirect = true`（351-353），并读取 `TRIM_APPDEST` / `TRIM_PKGVAR` 环境变量塞进模板数据（364-368）——这两个变量只在 **watchcow 自己**作为包装载后的安装脚本里才存在，这是 §3 重定向实现的关键前提。

---

## 3. "URL 重定向"到底怎么实现

### 3.1 结论先行

**生成包里没有任何重定向程序，也没有静态 HTML 页。** 它是一个三段式委托：

```text
fnOS 应用中心 iframe/新标签页
   │  GET /cgi/ThirdParty/<appname>/index.cgi/redirect/<appname>/<entry>[/<path>]
   ▼
<appname>/ui/index.cgi          ← install_callback 在安装时生成的 bash 脚本
   │  exec <watchcow 的 TRIM_APPDEST>/watchcow --mode cgi --socket <TRIM_PKGVAR>/watchcow.sock
   ▼
watchcow 常驻进程（root，unix socket 0660 上的 HTTP 服务）
   │  chi 路由 /redirect → RedirectHandler
   ▼
内嵌的 web/templates/redirect.tmpl：200 + 一段 JS
   → 浏览器端探测内网端口，成功则 location 到 http://<当前主机名>:<port><path>，
     失败则 location 到 <redirect 域名的 HTTPS URL>
```

即：**包内只有一个几十字节的 shell CGI，真正的重定向逻辑在 WatchCow 主程序里**，而主程序通过 unix domain socket + 反向代理把 CGI 请求转成 HTTP（`internal/cgi/handler.go:15-63`）。

### 3.2 入口 URL 指向哪里

`GenerateUIConfigJSON`（`internal/fpkgen/template.go:141-156`，逐字）：

```go
// If redirect mode is enabled, modify port and URL
if entry.Redirect != "" {
	// Remove port (omitempty will exclude it from JSON)
	port = ""
	// Build CGI URL: /cgi/ThirdParty/<AppName>/index.cgi/redirect/<appname>/<entry>[<path>]
	// Use "_" for default entry (empty name)
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

注意三点：

1. `/cgi/ThirdParty/<pkg>/` 是 fnOS 的 CGI 前缀，`index.cgi` 之后的部分会作为 `PATH_INFO` 透传（`internal/cgi/handler.go` 的注释示例：`/cgi/ThirdParty/watchcow.dashboard/index.cgi/containers/xxx` → `/containers/xxx`）。
2. `port` 被清空且 `omitempty`，所以 UI config 里没有端口——fnOS 不会做端口转发/端口检查，manifest 也不写 `service_port`（§1.2）。
3. 默认入口在 URL 里是 `_`，处理端再还原成空串（`internal/server/redirect.go:115-118`）。

### 3.3 包内那个 CGI 是怎么来的

[cmd_install_callback.tmpl](https://github.com/tf4fun/watchcow/blob/7dbe85ad4616d156cab5315a85eef696826cd19c/internal/fpkgen/templates/cmd_install_callback.tmpl) 第 5-9 行（逐字）：

```bash
cat > "${TRIM_APPDEST}/ui/index.cgi" << 'EOFCGI'
#!/bin/bash
exec "{{.WatchCowAppDest}}/watchcow" --mode cgi --socket "{{.WatchCowPkgVar}}/watchcow.sock"
EOFCGI
chmod +x "${TRIM_APPDEST}/ui/index.cgi"
```

`{{.WatchCowAppDest}}` / `{{.WatchCowPkgVar}}` 在生成时被替换成**安装 watchcow 那一刻**的 `TRIM_APPDEST` / `TRIM_PKGVAR`（`template.go:364-368`）。含义：生成包硬依赖「WatchCow 本身已安装且路径不变」。若 WatchCow 被卸载/挪位置，所有已安装的生成包入口都会 503（**推断**，代码中无路径存活校验）。

对应地，watchcow 自身包的 `fnos-app/cmd/install_callback` 做的是同一件事，只是路径固定为自己（`${TRIM_APPDEST}/watchcow`）。

### 3.4 CGI 与守护进程的桥

* `cmd/watchcow/main.go:19-50`：`-mode server|cgi`、`-socket`（默认 `$TRIM_PKGVAR/watchcow.sock`，缺省 `/tmp/watchcow/watchcow.sock`）、`-debug`。
* `internal/cgi/handler.go:15-40`：`RunCGI(socketPath)` 用标准库 `net/http/cgi` + `httputil.NewSingleHostReverseProxy("http://localhost")`，`Transport.DialContext` 改成 `net.Dial("unix", socketPath)`（43-63）；socket 连不上时 `ErrorHandler` 返回 503 与"服务不可用 / WatchCow 服务未运行"页面。
* `internal/server/server.go:35-103`：server 模式监听 unix socket 并 `chmod 0660`（`socketFileMode` 常量 15），shutdown 时删 socket。
* `internal/server/router.go:16-33`：`chi` + `Recoverer`，`r.Mount("/redirect", redirectHandler)`，Dashboard 挂 `/`。

### 3.5 服务端做了什么（函数与行为）

`internal/server/redirect.go`：

* `RedirectHandler.ServeHTTP`（97-141）：路径 `/redirect/<appname>/<entry>[/<path...>]`（98-113），`_` 还原空串（115-118），从 registry 取 `App` 与 `GetEntry`；app 不存在 404 `App not found: %s`，入口不存在 404，入口无 `Redirect` 则 400 `Entry does not have redirect configured: %s`（120-138）。最后 `outputHTML(w, entry.Redirect, entry.Protocol, entry.Port, path, sanitizeQueryString(r.URL.RawQuery), entry.ForceExternal)`（140）。
* 返回 **HTTP 200 + HTML**，不是 302（`outputHTML`，151-197）。这是设计必然：跳转目标取决于浏览器所在网络，只能由浏览器决定。
* `outputHTML` 从内嵌资源读 `css/bulma.min.css` 与 `templates/redirect.tmpl`（159-170），注册 `js` = `template.JSEscapeString`（173-175），渲染 `redirectTemplateData{RedirectBase, RedirectPath, RedirectQuery, ContainerProtocol, ContainerPort, Path, QueryString, ForceExternal, BulmaCSS}`（182-192）。CSS 直接内联进 HTML。
* `parseRedirectHost`（46-74）：`redirect` label 可写 `https://example.com/base?x=1`，无 scheme 时临时补 `http://` 解析，拆出 Base/Path/Query。
* `sanitizeQueryString`（27-35）：正则 `^([a-zA-Z0-9_~.%-]+=[a-zA-Z0-9_~.%/-]*(&...)*)?$`，不匹配就丢弃整个 query，防 XSS/注入。
* 错误页 `outputError`（144-148）用 `template.HTMLEscapeString`。

### 3.6 浏览器端：探测逻辑（`web/templates/redirect.tmpl`，242 行）

模板里 JS 由服务端注入常量（38-48）：

* `REDIRECT_BASE`（外部域名）、`REDIRECT_PATH`、`REDIRECT_QUERY`、`CONTAINER_PROTOCOL`、`CONTAINER_PORT`、`PATH`、`QUERY_STRING`、`FORCE_EXTERNAL`。
* `buildLocalURL()`（88-96）：`protocol = CONTAINER_PROTOCOL==='https' ? 'https:' : 'http:'`，URL = `protocol + "//" + window.location.hostname + ":" + CONTAINER_PORT + PATH (+ "?" + QUERY_STRING)`。**主机名取的是浏览器当前访问 fnOS 用的主机名**，不是容器 IP。
* `buildExternalURL()`（99-115）：`REDIRECT_BASE` 无 scheme 时用 `window.location.protocol`；路径用 `mergePaths(REDIRECT_PATH, PATH)`、query 用 `mergeQueryStrings(REDIRECT_QUERY, QUERY_STRING)`——即 `watchcow.path` 会同时叠加到内网与外部目标上。
* `isPrivateIP`（118-132）：`10.`、`172.16-31`、`192.168.`、`127.0.0.1`/`localhost`。
* `isLocalHostname`（135-160）：IPv4 私网、`localhost`/`127.0.0.1`、`.local`、无点主机名（如 `nas`）都算内网。
* `isMixedContentProbeBlocked`（164-166，逐字语义）：`Boolean(CONTAINER_PORT) && window.location.protocol==='https:' && CONTAINER_PROTOCOL!=='https'`。注释写明：浏览器把"HTTPS 页面去 fetch 一个 HTTP 服务"当主动混合内容拦截，但**顶层导航是允许的**。
* `checkLocalAccess`（169-196）：`fetch(localURL, {method:'HEAD', mode:'no-cors', signal})` + 3 秒 `AbortController`；任何错误（CORS 不可见、超时、连接拒绝）都返回 false。
* `main`（199-230）决策树：

```text
FORCE_EXTERNAL === true                 → 外部（不探测）
isLocalHostname(hostname) === false      → 外部（来自公网）
isMixedContentProbeBlocked() === true    → 本地（不探测，直接试内网地址）
否则 checkLocalAccess(localURL)          → 成功：本地；失败：外部
异常兜底                                  → 2 秒后外部
```

所以在 **fnOS 的 HTTPS 页面 + 容器 HTTP** 这一最常见组合下，探测根本不执行：直接 `location = http://<fnOS主机名>:<port>`，靠浏览器允许"HTTPS 页面导航到 HTTP 顶层页面"这一例外工作（README.md:136 的描述与代码一致）。这也解释了为什么该模式需要依赖顶层导航，而 `fetch` 探测只在 fnOS 走 HTTP 时才真正有效。

### 3.7 ui_type 如何决定

`ui_type` 原样写进 UI config 的 `type` 字段（`template.go:161`），**重定向模式不改变它**（`GenerateUIConfigJSON` 里没有对 UIType 的条件分支）。含义：

* `ui_type=iframe`：fnOS 用内嵌窗口打开 `/cgi/ThirdParty/<app>/index.cgi/redirect/...`，该页面返回 200 HTML，再由 JS 在**同一个 iframe/window 内** `location.href` 换成内网或外网目标。若目标是外站且禁 frame，会受 `X-Frame-Options` 影响（**推断**，模板未做 frame 突破）。
* `ui_type=url`（默认）：新标签页打开该 CGI URL，再 JS 跳转。

### 3.8 探测端口 = 入口的 host 端口

`CONTAINER_PORT` 来自 `entry.Port`，而 `entry.Port` 默认取 `watchcow.service_port`，否则 `extractFirstPort`（`internal/fpkgen/generator.go:323-358`）：只看 `container.HostConfig.PortBindings` 里的 tcp 绑定，按容器端口数字升序、再按 host 端口数字升序取第一个非空 `HostPort`；**没有端口映射就返回空串**。因此：

* 桥接网络 + `-p 8080:80` → 入口 port=8080，探测 `http://<fnos-host>:8080`。
* `network_mode: host` → 没有 PortBindings，默认 port 为空，必须显式写 `watchcow.service_port`（README.md:133 原文："host 网络且无端口映射时需显式填写"；`generator_test.go` 的 `TestExtractConfigSupportsHostNetworkWithExplicitServicePort` 固化了这一行为：NetworkMode `host` + `watchcow.service_port=8080` → `config.Port` 与 `entry.Port` 都是 `8080`，manifest 写 `service_port=8080`）。

### 3.9 redirect 与 redirect_force_external 的运行时语义

* `redirect=""`：普通入口，UI config 写 `url=path`、`port=port`，fnOS 自己做端口转发/代理。
* `redirect=<url>`：入口变成 CGI 入口，服务端不 302，返回探测页；探测成功走内网，失败/外网访问走 `<url>`。
* `redirect_force_external=true`：JS 第一步就 `FORCE_EXTERNAL` 分支，跳过一切探测直接外部（README.md:137 举例 Vaultwarden 这类必须走域名 HTTPS 的应用）。
* 两者都只影响**入口点击**，不影响容器本身；manifest 的 `service_port` 若来自 redirect 入口则为空（§2.4）。

---

## 4. 安装链路

### 4.1 appcenter-cli install-local 的完整调用

[internal/fpkgen/installer.go:84-101](https://github.com/tf4fun/watchcow/blob/7dbe85ad4616d156cab5315a85eef696826cd19c/internal/fpkgen/installer.go#L84-L101)（`InstallLocal`）：

```go
args := []string{"install-local", "--volume", volume}
cmd := exec.Command(cliPath, args...)
cmd.Dir = appDir            // 关键：以生成目录为 cwd
cmd.Stdout = os.Stdout
cmd.Stderr = os.Stderr
```

* **只传 `--volume`，不传包路径、不传 fpk 文件、不传 `--force`、不传 `--yes`**（88-96）。install-local 从**当前目录**读包，即 §1.1 那个目录树。
* CLI 定位顺序（`installer.go:47-81`）：`/var/apps/appcenter/target/bin/appcenter-cli` → `/usr/bin/appcenter-cli` → `/usr/local/bin/appcenter-cli` → `exec.LookPath("appcenter-cli")` → `which appcenter-cli`；全失败报 `appcenter-cli not found in common locations or PATH`。
* 失败文案 `appcenter-cli install-local failed: %w`（100）。
* 失败**没有内置回滚**（不卸载半安装的包），只是把错误往上抛；临时目录由调用方 `defer os.RemoveAll(appDir)` 清掉（`internal/docker/monitor.go:972-1003`）。

### 4.2 安装卷的选择

`resolveInstallVolume`（`installer.go:103-116`）三级优先：

1. label `watchcow.install_volume`（`parseInstallVolume` 要求正整数，即 1、2、3…）；
2. `appcenter-cli default-volume` 的输出；
3. 兜底常量 `fallbackInstallVolume = "1"`（`installer.go:12-13`）。

README.md:113-146 的表格与此一致，并明确 `watchcow.install_volume` 优先于 `appcenter-cli default-volume`。

### 4.3 watchcow 自己与生成包以什么身份运行

* watchcow 自身包：`fnos-app/manifest` 有 `install_type=root`；`fnos-app/config/privilege` 是 `{"defaults":{"run-as":"root"}}`；因此 `cmd/main` 里的守护进程以 **root** 运行（`fnos-app/cmd/main`）。
* 生成包：模板固定写 `install_type=root`（`templates/manifest.tmpl`）与 `config/privilege = {"defaults":{"run-as":"root"}}`（`templates/config_privilege.json.tmpl`）。所以**安装本地应用需要管理员权限**，生成的应用也以 root 运行其 `cmd/main`。
* Docker 访问：代码只用 `client.FromEnv`（`internal/fpkgen/generator.go:29`、`internal/docker/monitor.go:218`），仓库里对 `docker.sock` / `DOCKER_HOST` / `/var/run` 的 grep 在 `*.go/*.md/*.yaml` 与两个 manifest 中**零命中**——没有显式挂载声明。合理解释是 fnOS 的 root 应用运行在宿主命名空间中，`/var/run/docker.sock` 天然可见（**推断**，源码未断言）。
* `fnos-app/config/resource` 声明了一个数据共享目录：`{"data-share":{"shares":[{"name":"icons","permission":{"rw":["watchcow"]}}]}}`——用于本地图标库（`TRIM_DATA_SHARE_PATHS/icons`，README.md:214-280）。

### 4.4 生命周期映射与串行化

README.md:52-59 的映射，对应 `internal/docker/monitor.go`：

| Docker 事件 | fnOS 动作 | 代码 |
|---|---|---|
| start（未安装） | 生成包 + `install-local` | `processContainerStartWithMode`（972-1003） |
| start（已安装） | `appcenter-cli start` | 910-918 |
| stop / die | `appcenter-cli stop` | `processStop`（1186-1205） |
| destroy | `appcenter-cli uninstall` | `processDestroy`（1208-1249） |

* 所有操作进**单条队列**串行执行：`opQueue chan *AppOperation` 容量 100（`monitor.go:131-132, 252`），`runOperationWorker`（783-816）按序处理 container_start / dashboard_install / dashboard_reinstall / stop / destroy / dashboard_uninstall / orphan_uninstall。
* 生成安装前 `time.Sleep(2 * time.Second)`（929）——等容器端口/网络就绪。
* `StartApp` 先查 `appcenter-cli status`，若为 `starting` 则跳过（注释：此状态再 start 会得到 appcenter code 10500）（`installer.go:194-211, 230-239`）。

### 4.5 卸载与回滚

`Installer.Uninstall`（`installer.go:152-191`）：

1. 先 `appcenter-cli stop`，记录 `stopErr` 但不中断；
2. 再 `appcenter-cli uninstall`（`CombinedOutput`）；
3. 若 uninstall 失败且 stop 原本成功 → `restoreStoppedApp` 用 `appcenter-cli start` 把应用拉回运行态；回滚也失败则返回 `*RestoreStoppedAppError{UninstallErr, RestoreErr}`（17-24，文案 `%v; restoring stopped app failed: %v`）；
4. uninstall 返回成功后还要 `checkAppInstalled` 复核：`appcenter-cli list` 输出里以竖线开头的行取第 2 列 trim 后与 appname 全等比较（`installer.go:242-249`），仍存在则视为失败，同样尝试 restore。

Dashboard 触发的卸载（`processDashboardUninstall`，`monitor.go:2085-2150`）额外有所有权守卫：

* `labelSourceOwnsApp` → 拒绝，文案"应用当前由 labels 配置接管，无法从 Dashboard 卸载"；
* `appHasActiveDesiredOwner` → 拒绝，"应用仍由另一个活动配置使用，无法卸载"；
* 通过后 `markPendingUninstall` → 若已安装则 `Uninstall`（失败写失败状态并 return）→ `Unregister` → `clearLabelRevision` → 清掉所有同 appname 容器的 Installed/AppName → `CompleteDelete(appName)`。
* 崩溃恢复：`label-revisions.json` 持久化 `label_revisions` / `label_owners` / `pending_uninstalls`（`monitor.go:142-148`，路径 `$TRIM_PKGVAR/label-revisions.json` → `$TRIM_PKGETC` → `/tmp/watchcow/label-revisions.json`，261-269），`processOrphanUninstall`（2152-2169）负责补卸载。

**升级不是 in-place**：`processDashboardReinstall`（`monitor.go:2193-2275`）先 `Uninstall(旧 appname)`，成功后再重新生成并 `install-local`；任一步 Uninstall 失败即中止并记录失败。因此"改端口/改图标/改版本"对 Dashboard 配置而言等于卸载重装，期间应用会短暂消失。

---

## 5. watchcow 自身的 fpk 模板（`fnos-app/`）

### 5.1 manifest

`fnos-app/manifest`（16 行）：`appname=watchcow`、`version=1.0.0`（CI 在 tag 时 sed 改写）、`display_name=WatchCow`、`desc` 含 HTML（`<b>`/`<br>`/`<a>`/`<code>`）、`arch=x86_64`、`platform=x86`、`source=thirdparty`、`maintainer=tf4fun`、`maintainer_url`、`distributor`、`distributor_url`、`desktop_uidir=ui`、`desktop_applaunchname=watchcow.dashboard`、`ctl_stop=true`、`checkport=false`、`install_type=root`。

与生成包模板的差异（对比 §1.2）：多了 `maintainer_url`、`distributor_url`、`ctl_stop=true`，且 `platform=x86` 而非 `all`，`checkport=false` 无条件写。

### 5.2 入口

`fnos-app/app/ui/config`（单行 JSON）：

```json
{".url":{"watchcow.dashboard":{"title":"watchcow","icon":"images/icon-{0}.png","type":"iframe","protocol":"http","url":"/cgi/ThirdParty/watchcow/index.cgi/","allUsers":false,"noDisplay":true}}}
```

* 入口 key = `watchcow.dashboard`（与 `desktop_applaunchname` 对齐）。
* `type=iframe`：Dashboard 在桌面窗口内打开。
* `allUsers=false`：仅管理员可见；`noDisplay=true`：不占桌面图标，只从右键/应用中心进（`desktop_applaunchname` 仍可启动）。
* `url` 指向自身 CGI：`/cgi/ThirdParty/watchcow/index.cgi/`。

### 5.3 服务与图标

* `fnos-app/cmd/main`（107 行）：`LOG_FILE=$TRIM_PKGVAR/info.log`、`PID_FILE=$TRIM_PKGVAR/app.pid`、`CMD=$TRIM_APPDEST/watchcow`；`start` 用 `bash -c "$CMD" >> log 2>&1 &` 并写 pid；`stop` 读 pid、`kill -TERM`、最多等 10s、再 `kill -KILL`；`status` 用 `kill -0`，运行 exit 0 / 未运行 exit 3；未知参数 exit 1。
* `fnos-app/cmd/install_callback`：安装时写 `$TRIM_APPDEST/ui/index.cgi`（`exec "$TRIM_APPDEST/watchcow" --mode cgi --socket "$TRIM_PKGVAR/watchcow.sock"`）并 `chmod +x`。**Dashboard 就是这样被访问的**：fnOS → CGI → unix socket → 常驻进程。
* 图标：`fnos-app/ICON.PNG`、`fnos-app/ICON_256.PNG`、`fnos-app/app/ui/images/icon-64.png`、`icon-256.png`（注意这里用 `-` 分隔，生成包用 `_`）。
* `fnos-app/wizard/{install,config}`：JSON 步骤数组，唯一一项是"图标 CDN 配置"，`text = WATCHCOW_ICON_CDN_TEMPLATE`，`initValue = https://cdn.jsdelivr.net/gh/homarr-labs/dashboard-icons/png/%s.png`，校验 `rules.pattern = ^https?://.*%s.*`（要求必须含 `%s` 占位）。
* `fnos-app/config/resource`：`data-share` 共享目录 `icons`，rw 给 `watchcow`。

### 5.4 UI 技术栈与后端通信

* 纯 Go 服务端渲染 + **HTMX**：`web/js/htmx.min.js`、`web/css/bulma.min.css`（+ 自定义 `dashboard.css`）、`web/templates/{dashboard,container_list,container_form,redirect}.tmpl`。没有前端构建步骤、没有 npm、没有 SPA 框架。
* 静态资源**内嵌进二进制**：`web/embed.go` 只有 `//go:embed templates css js`（6 行）；重定向页与 Dashboard 页都从 `web.Assets` 读（`internal/server/redirect.go:159-166`；Dashboard 的 BulmaCSS 同样内联）。
* 前后端通信：浏览器 → fnOS CGI → unix socket HTTP → chi 路由。Dashboard 的交互是 HTMX 片段请求：`GET /containers`、`GET /containers/{id}`、`POST /containers/{id}`（保存）、`DELETE /containers/{id}`（卸载）（`handler.go:94-101`）。表单是 `multipart/form-data`（图标上传），`ParseMultipartForm(10<<20)`（411-424）。
* 提交后写配置并触发安装：`storage.Replace` + `h.trigger.TriggerInstall(containerID, dockerConfig)`（`handler.go:529-542`），配置 revision 用 `dashboardConfigRevision`（对 AppName/DisplayName/Description/Version/Maintainer/Entries/IconBase64 做 json.Marshal 后 sha256 取前 16 字节，809-833）。
* Dashboard 只能编辑**不具名入口**（`parseEntryFromForm` 里 `Name` 恒为空，663-693；`mergeDashboardEntries` 保留命名入口，722-744）；有 `watchcow.enable=true` 的容器在 Dashboard 里直接 403"标签配置的容器无法修改"（397-400）。README.md:85-91 也是这样描述的。
* Dashboard 校验（`validateDashboardEntry`，695-720）：protocol 必须 http/https；ui_type 必须 url/iframe；redirect 非空必须是带 host 的 http(s) URL；**port 为空且 redirect 为空时报"普通入口必须填写端口"**；port 必须 1..65535。

---

## 6. 限制与坑

| # | 坑 | 事实与出处 |
|---|---|---|
| 1 | **appname 决定包身份** | `watchcow.appname` 缺失时是 `watchcow.<容器名>`（`monitor.go:1744-1750`、`app.go:45-47`）。Dashboard 生成后注释明确写"AppName 是已安装包身份，生成一次并在后续编辑/端口变化中保留"（`handler.go:471-477`）。改 appname = 换一个应用（旧包不会被自动清理，除非走 reinstall 流程）。 |
| 2 | **同一 appname 被两个容器使用** | 仓库没有跨容器唯一性检查：`getAppNameFromLabels` 只做单容器映射。两个容器给出同一 appname 会命中同一个 fnOS 包身份，第二次 `install-local` 的行为取决于 appcenter-cli（**仓库内无证据**），watchcow 侧表现为 registry 里同名 App 被覆盖（`app.Registry` 以 appname 为键，`app.go:218-283`）。 |
| 3 | **重复 install-local / 已安装时启动** | 启动路径会先看是否已安装：已安装走 `appcenter-cli start`，不再重装（`monitor.go:910-918`）。要强制重装只能走 reinstall 流程（先 uninstall）。`install-local` 不带 `--force`（§4.1）。 |
| 4 | **版本号只是文本** | `watchcow.version` 默认 `1.0.0`，直接写 manifest；没有版本比较/升级逻辑，升级=卸载+重装（§4.5）。 |
| 5 | **安装卷** | `watchcow.install_volume` → `appcenter-cli default-volume` → `1` 兜底（`installer.go:103-116`）。卷号写错由 appcenter-cli 报错，watchcow 不回退到别的卷。 |
| 6 | **必须管理员/root** | 两边的 `install_type=root` + `run-as: root`（§4.3）。普通用户既不能 install-local，也看不到 `allUsers=false` 的 Dashboard 入口。 |
| 7 | **host 网络 + 无端口映射** | 默认端口来自 `HostConfig.PortBindings`，host 网络下为空，必须显式 `watchcow.service_port`，否则 manifest 无 `service_port`、redirect 探测页 `CONTAINER_PORT` 为空（`generator.go:323-358`、`redirect.tmpl:164-166` 的 `Boolean(CONTAINER_PORT)` 守卫）。`HasAccessiblePorts` 对 host 网络放行（`types.go:147-150`），但那是 Dashboard 的"可配置"判断，不等于端口已知。 |
| 8 | **混合内容** | HTTPS 的 fnOS 页面无法 `fetch` HTTP 目标，代码直接跳过探测走内网地址（`redirect.tmpl:164-166, 199-230`）。若内网地址实际不可达，用户会看到一个浏览器错误页而不是自动回退到外部——回退只发生在**探测阶段**，跳转后失败不再重试（**推断**）。 |
| 9 | **iframe + 外部站点** | `ui_type=iframe` 时外部目标若禁 frame 会白屏；代码未处理 `X-Frame-Options`（**推断**）。 |
| 10 | **图标** | 支持 PNG/JPEG/WebP/BMP/ICO，按 magic bytes 判断（`format_detect.go:27-71`），不看扩展名；10MB 上限（`icon_source.go:16`）；统一转 PNG，方形透明补边 + CatmullRom 缩放到 64/256（`icons.go`）；浏览器会缓存旧图标（README.md:336-342）。`file://` 相对路径基于 `com.docker.compose.project.working_dir`（`icons.go:130-136`）。 |
| 11 | **label 改了不生效** | Docker 容器元数据不可变，必须删容器重建（README.md:336-342）。watchcow 靠 `label-revisions.json` 感知 label 变化并按 revision 重装（`monitor.go:142-148`），但**重建容器仍是前提**。 |
| 12 | **多入口只能靠 label** | Dashboard 只写单入口，命名入口只能在 compose/labels 里定义（`handler.go:663-693, 722-744`）。`_` 是保留入口名（`generator.go:468-478`）。 |
| 13 | **生成包硬依赖 watchcow 路径** | `index.cgi` 里写死了安装时的 `TRIM_APPDEST`/`TRIM_PKGVAR`（`cmd_install_callback.tmpl:5-9`）。移动/重装 watchcow 后，旧生成包的 redirect 入口会 503（`cgi/handler.go` 的 ErrorHandler）。 |
| 14 | **并发与延迟** | 所有 appcenter-cli 调用经单队列串行（`monitor.go:783-816`），且安装前固定 `Sleep(2s)`（929）——批量启动多个容器时会排队变慢。 |
| 15 | **无端口映射的应用** | 既没有 host 端口也没写 `service_port` 时，入口没有 port，只能配 `redirect` 才能可用（`validateDashboardEntry` 的"普通入口必须填写端口"就是这个约束，`handler.go:708-712`）。 |
| 16 | **32 字符上限** | 超长 appname 被截断加哈希（`app.go:61-70`），Dashboard 侧启动时还会迁移修复（`storage.go:412-471`）；手工写超长 `watchcow.appname` 会在生成时被 `ValidateAppName` 拒绝（`generator.go:253-264`）。注意 `SanitizeAppNamePart` 只产小写，但校验允许大写（§2.3）。 |

---

## 7. 对本仓库（fnos_daili）可复用的结论

以下均为**基于上述源码的推断**，未在本仓库验证：

1. 想生成一个"代理/入口类"应用，最小可用包 = `manifest` + `app/ui/config` + `cmd/main` + `cmd/install_callback` + `config/{privilege,resource}` + `ICON*.PNG`；`wizard/`、`etc/`、`callback/` 都不是必需的（WatchCow 两者都不生成）。
2. manifest 里 `install_type=root` + `config/privilege {"defaults":{"run-as":"root"}}` 是"需要访问宿主资源"的标准写法。
3. 若要做"HTTPS 页面打开 HTTP 内网服务"，WatchCow 的答案不是 302，而是 **CGI + 服务端渲染探测页 + 浏览器端 HEAD 探测（3s 超时）+ 混合内容时直接顶层导航**。这条路径可以照搬到任何 fnOS 应用。
4. `appcenter-cli install-local --volume <n>` 以 cwd 为包目录，不需要先 `fnpack`；本地调试时把包目录放好、在该目录执行即可。
5. 卸载的安全做法：先 `stop`，再 `uninstall`，用 `appcenter-cli list` 复核；失败时 `start` 回滚。
