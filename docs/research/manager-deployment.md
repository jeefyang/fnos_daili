# 托管台真机部署记录与排障

记录日期：2026-11-16/17。对象：`daili.manager` 在真实飞牛（fnOS）上的安装、启动、入口与踩过的坑。
只记事实与结论，机制细节见 `docs/specs/proxy-bundler.md`。

## 真机路径与形态

| 项 | 实际值 |
| --- | --- |
| 应用部署目录 `TRIM_APPDEST` | `/vol1/@appcenter/daili.manager`，只有 `bin/`、`ui/` |
| 运行数据目录 `TRIM_PKGVAR` | `/vol1/@appdata/daili.manager`（`manager.sock`、`manager.pid`、`manager.log`、`state.json`、`packages/`） |
| 守护进程 | `bin/manager --mode serve --socket /vol1/@appdata/daili.manager/manager.sock`，以 root 运行 |
| socket | `srw-rw----`（0660），`ss -xl` 可见 LISTEN，`ss -ltnp` 无该应用 |
| 入口 shim | `/vol1/@appcenter/daili.manager/ui/index.cgi`，安装时写入 |
| 应用中心 CLI | `/usr/local/bin/appcenter-cli`（v1.0.2，root:root 755），非 root 调用会因 `/run/trim_app_cgi/rpcbroker` 权限 panic |

`cmd/` 下的脚本在安装时被应用中心消费，**不会落到部署目录**；任何“安装后再执行 `$DESK/cmd/main`”的写法都会
`No such file or directory`。守护进程由 `ui/index.cgi` 的 shim 懒启动。

## 入口与访问地址

- 托管台入口：`/cgi/ThirdParty/daili.manager/index.cgi/`，`ui/config` 里**不能出现 `port` 字段**（空串也算），
  否则飞牛按“端口入口”解析，桌面窗口只剩标题、内容空白。
- 代理应用入口：独立 TCP 端口（20000–29999），manifest `service_port` + 入口 `port`，访问 `http://<NAS>:<port>/`。
- 公网（FN Connect）访问时，每个入口有独立应用子域：
  `https://<入口键点→连字符>.<当前域名>/`；本项目入口键固定 `<appname>.main`，故为
  `https://<appname 点→连字符>-main.<域名>/`。需要该应用已在 FN Connect 后台发布，否则子域返回 403。
  详见 `docs/research/fnos-fnconnect-entry-url.md`。

## NAS 侧 DNS 不可用（易误判为端口不通）

现象：目标填域名（如 `https://chrome.5520183.xyz`）时，托管台“测试连通”报 `resolve_failed` 或
`dns_timeout 10001ms`，安装后打开入口得到代理自己的 502；但同一域名在用户电脑上能打开。

根因：测试与反代都由 NAS 上的进程执行，用的是 NAS 的解析器。该机 `getent hosts <任意公网域名>` 无输出、
`/etc/resolv.conf` 指向的内网 DNS（`192.168.123.1`）不回答公网查询。

处理：在设置或目标表单里填一个可用的 DNS（如 `223.5.5.5`），生成时烤进 `app/config.json` 的 `dns`；
代理用该解析器解析、再逐个 IP 做目标拦截后拨号，Host/SNI 仍用域名。浏览器能打开只说明用户侧能解析，
不能作为 NAS 侧连通性的证据。

## 两个真实故障及根因

1. **桌面窗口只有标题、内容空白**：`app/ui/config` 的入口写了 `"port": ""`。飞牛把“存在 port 字段”
   当端口型入口，空串导致入口 URL 不成立。修法：入口结构体 `port` 加 `omitempty`，托管台入口不写该字段，
   `VerifyManager` 拒绝出现 `port` 的托管台入口。
2. **CGI 入口 HTTP 500，`manager.log` 只有启动行**：`cmd/install_callback` 用带引号 heredoc 写 shim，
   `TRIM_APPDEST`/`TRIM_PKGVAR` 留到运行期才展开；飞牛 CGI 环境不导出这两个变量，脚本带 `set -u` 立即退出。
   watchcow 的做法是**不带引号 heredoc**，安装时就把绝对路径写死进 `ui/index.cgi`。修法照做，shim 中不再出现
   `TRIM_`，并在 socket 缺失时 `nohup bin/manager --mode serve` 懒启动后 `exec --mode cgi`。

## 安装与状态机要点（实测）

- 只用 `appcenter-cli install-fpk -v <volume> <fpk>`；`install-local` 在该版本全部失败（10237 + panic）。
- 成功判据是输出含 `Installation complete` **且** `list` 中状态为 `running`；CLI 成功/失败经常都 exit 0。
- `starting` 可以持续数分钟；这期间 `uninstall` 报 `code 10500`，重试即可，不要当成永久失败。
- `install-fpk` 不校验 `manifest.checksum`；缺 `config/resource` 报 `10111`；
  `desktop_applaunchname` 与 `app.tgz/ui/config` 的键不一致也能装上，所以要自己保证一致。
- 升级 = 卸载 + 重装；已安装时 `install-fpk` 直接拒绝（`Application [...] is installed`）。

## 排障顺序

1. `sudo appcenter-cli list`：应用是否安装、状态是否 `running`。
2. `ls -l /vol1/@appdata/daili.manager`：`manager.sock`、`manager.pid`、`manager.log` 是否都在。
3. `cat /vol1/@appdata/daili.manager/manager.log`：应有 `started app=daili.manager`；CGI 打不开日志时会回退 stderr。
4. 直接请求 `https://<host>/cgi/ThirdParty/daili.manager/index.cgi/`：500 说明 CGI 层没执行或 shim 找不到路径。
5. `ps -ef | grep bin/manager`：进程在不在、是不是 root、socket 路径对不对。

## 已验证的流程（2026-11-18 真机）

- **安装形态**：`run-as root` + manifest 不写 `install_type` 的托管台包能正常安装、启动、长期运行；之前记为“未验证风险”的这一条可以撤销。
- **基础闭环**：安装托管台 → 打开 `/cgi/ThirdParty/daili.manager/index.cgi/` → 四步建反代目标 → 代理应用 running → 入口照常打开 → 卸载并清记录。
- **备份还原整圈**：列表「导出全部」→ 卸载并重装 `daili.manager`（state 丢失）→ 导入那份 JSON、勾选「同时还原全局设置」→ 已装同名应用按“接管”走一遍卸载重装，端口沿用备份里的值，图标与 bcrypt 口令哈希保持不变。
- **列表交互**：两个页签（已托管 / 系统里已有）、两处应用图标（已托管用 target 图标或默认图，未纳管读安装目录里的真实图标）、删除即时反馈、批量删除（每删完一条立刻刷新，不再残留“运行中”）、小屏卡片布局，均已真机确认。

仍未真机验收：真实站点整站反代（绝对路径资源）、直连 302、Basic Auth 401、重启后对账、日志脱敏抽查。
