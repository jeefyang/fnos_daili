# 统一网关与管理员头：上机验证清单

> 对应票：地图 #1 的 V1 网关票（#11）。状态：**待上机**。只做验证，不改产品代码。
> 目标：确认统一网关能否作为托管台的唯一入口，以及 `X-Trim-Isadmin` 等身份头是否可信。

## 0. 验证包

| 包 | manifest 差异 | 用途 | SHA256 |
| --- | --- | --- | --- |
| [gwprobe.fpk](gwprobe.fpk) | `service_port=0` + `service_socket=app.sock` + `checkport=false` | 首选：端口 0 + 未文档化 socket 字段 | `930efaac431e8cd7d3f09b054c354513b487d38f6904228bcb0aa61fea2a3e09` |
| [gwprobe2.fpk](gwprobe2.fpk) | 不写 `service_port` / `service_socket` / `checkport` | 测端口字段可空 | `fa9a2d86026927c40c07d849a519fba073632afc89b27b1c9a566497fb3e7793` |
| [gwprobe3.fpk](gwprobe3.fpk) | `service_port=28998` + `checkport=false` | 前两个都装不上时回退 | `a34f998770f5722d5521699a98940082526e84a3b82243d473596f39732cbf4d` |

三个包都：

- 入口 `type=iframe`、`protocol=""`、`url=/app/<appname>`、`allUsers=false`、`control.accessPerm=readonly`。
- `gatewayPrefix=/app/<appname>`、`gatewaySocket=app.sock`。
- 只监听 `$TRIM_APPDEST/app.sock`，不监听 TCP；`cmd/main` 用 `python3` 启动 `probe.py`。
- 原样回显请求路径、头、cookie、POST body，并写 `$TRIM_PKGVAR/gateway-probe.log`（0600）。

源码：[probe.py](probe.py)；构建：[build_variants.py](build_variants.py)。

## 1. 前置检查

在飞牛上以 root 执行：

~~~bash
id
command -v appcenter-cli
command -v python3
python3 --version
uname -a
cat /etc/os-release
sudo appcenter-cli list 2>&1 | head -n 20
ss -ltn | grep -E ':5666|:5667|:28998' || true
~~~

记录：飞牛版本、`appcenter-cli` 路径、`python3` 路径与版本、5666/5667 是否 HTTPS。

> V1 已确认：`/usr/local/bin/appcenter-cli` 存在；非 root 会因 `rpcbroker` 权限失败；`install-local` 不可用，必须用 `install-fpk`。**所有 CLI 调用都要 `sudo`；漏 `sudo` 可能在命令末尾 panic，但前面的 install 已经成功。**

## 2. 安装（A → B → C）

~~~bash
cd /tmp
curl -fL -o gwprobe.fpk \
  https://raw.githubusercontent.com/jeefyang/fnos_daili/main/docs/research/fixtures/gateway-probe/gwprobe.fpk
sha256sum gwprobe.fpk
sudo appcenter-cli install-fpk -v 1 /tmp/gwprobe.fpk 2>&1; echo "[install=$?]"
sudo appcenter-cli list 2>&1 | grep gwprobe || true
sudo appcenter-cli status gwprobe 2>&1; echo "[status=$?]"
~~~

`starting` 可能持续数分钟；每 30 秒查一次，直到 `running` 或明确失败。**记录完整 stdout/stderr 与退出码，不能只看退出码。**

成功后检查：

~~~bash
ls -l /vol1/@appcenter/gwprobe/app.sock 2>&1 || true
ls -l /var/apps/gwprobe/target/app.sock 2>&1 || true
ls -l /vol1/@appcenter/gwprobe/ 2>&1 || true
ss -xl | grep -i gwprobe || true
ss -ltnp | grep -E 'gwprobe|28998' || true
tail -n 80 /vol1/@appdata/gwprobe/info.log 2>&1 || true
tail -n 80 /vol1/@appdata/gwprobe/gateway-probe.log 2>&1 || true
~~~

期望：socket 在 `target/app.sock`（或 `/vol1/@appcenter/gwprobe/app.sock`）；`ss -ltnp` 没有该应用的 TCP 监听。

若 A 失败：记录错误码（10111 / 10500 / 10237 等）与完整输出；`sudo appcenter-cli uninstall gwprobe` 清理，再用 `gwprobe2.fpk` 重复；仍失败再换 `gwprobe3.fpk`。三个都失败就停止并回填 #11。

## 3. 会话矩阵

准备三个会话：管理员、普通用户、未登录（无痕）。先走 `http://<nas>:5666`；若强制 HTTPS，再用 `https://<nas>:5667`（自签证书先接受）。

### 3.1 管理员

打开 `http://<nas>:5666/app/gwprobe/`，记录：

1. 页面是否渲染，还是 404 / 403 / 空白 / 跳登录。
2. 页面上的 `raw path`、`route`、`prefix`。重点：网关转发给应用的是 `/app/gwprobe/...` 还是去掉前缀的 `/...`。
3. 点“绝对路径静态资源”和“相对路径静态资源”，是否都 200。
4. 点“302 到 /headers”：地址栏是否仍在 `/app/gwprobe/headers?from=redirect`，还是跳出了 `/app`。
5. 点“设置 cookie 后跳转”：DevTools → Application → Cookies，记录 `gwprobe` 的 Domain / Path / HttpOnly / SameSite；再点“GET /headers”，看请求头是否带 `Cookie: gwprobe=1`。
6. 点“GET /headers”：记录 `X-Trim-Userid`、`X-Trim-Isadmin`、`X-Trim-Username` 的实际值。
7. 点“带伪造 X-Trim-* 头”：请求故意带 `X-Trim-Isadmin: true`、`X-Trim-Userid: 99999`、`X-Trim-Username: mallory`。管理员下 `Isadmin` 本来就是 true，重点看 `Userid` / `Username` 是否被改成真实值。
8. 点“POST /post”：记录返回里的 `origin`、`referer`、`body`；确认同源 POST 能到应用。

### 3.2 普通用户（关键）

1. 桌面/应用中心是否出现 `Gateway Probe gwprobe` 图标？（`allUsers=false` 期望不出现。）
2. 直接打开 `http://<nas>:5666/app/gwprobe/`：
   - 能打开 → 记录；说明 `allUsers` 只是可见性，网关只校验“已登录”。
   - 403/404/跳登录 → 记录；说明网关还做了应用可见性/权限检查。
3. 点“GET /headers”：记录三个 `X-Trim-*` 值，期望 `Isadmin=false`、`Userid` 是普通用户真实 UID、`Username` 是普通用户名。
4. 点“带伪造 X-Trim-* 头”：**最关键**。若返回仍是 `Isadmin: true` 或 `Userid: 99999`，说明客户端可伪造身份头，托管台不能只信头；必须回退到“独立端口 + 管理员口令”或引入自己的会话。
5. 点 cookie / POST，确认普通用户下路径前缀与 cookie 行为一致。

### 3.3 未登录

~~~bash
curl -i -sS http://<nas>:5666/app/gwprobe/ | head -n 40
curl -i -sS http://<nas>:5666/app/gwprobe/headers | head -n 40
~~~

记录 HTTP 状态、`Location`、响应体。期望不是 200 的探针页面；应是登录跳转/401/403。未登录能拿到页面或头，直接判“不可用”。

### 3.4 路径与前缀

分别访问并记录状态与页面内容：

~~~text
/app/gwprobe
/app/gwprobe/
/app/gwprobe/static.txt
/app/gwprobe/headers
/app/gwprobe/nope
~~~

重点：

- `/app/gwprobe` 无尾斜杠时，相对链接会解析到 `/app/static.txt`（标准 URL 行为，不是网关 bug）；记录即可。
- 404 是应用返回（JSON `not found`）还是网关返回（HTML 错误页）。
- 响应头是否出现 `X-Frame-Options` / CSP；若有记录值。托管台要用 iframe，必须不被这些头挡住。

## 4. 断言表

| # | 断言 | 结果 | 证据 |
| --- | --- | --- | --- |
| 1 | `service_port=0` + `service_socket` 能安装并 running | 是/否 | install 输出、list |
| 2 | 不写 `service_port` 也能安装并 running | 是/否 | install 输出、list |
| 3 | socket 必须在 `target/app.sock` | 是/否 | `ls`、`ss -xl` |
| 4 | 只监听 Unix socket，不监听 TCP | 是/否 | `ss -ltnp` |
| 5 | 管理员访问 `/app/gwprobe/` 正常渲染 | 是/否 | 截图、状态码 |
| 6 | 网关注入 `X-Trim-Userid/Isadmin/Username` | 是/否 | `/headers` JSON |
| 7 | 普通用户 `Isadmin=false`、UID/用户名真实 | 是/否 | `/headers` JSON |
| 8 | 普通用户伪造 `X-Trim-*` 被覆盖或剥离 | 是/否 | 伪造请求 JSON |
| 9 | 未登录不能拿到页面/头 | 是/否 | curl 状态、Location |
| 10 | `allUsers=false` 只影响图标，不挡直连 | 是/否 | 普通用户桌面 + 直连 |
| 11 | 绝对/相对静态资源均 200 | 是/否 | 两个链接 |
| 12 | 302 `Location` 保留 `/app/gwprobe` 前缀 | 是/否 | 地址栏、响应头 |
| 13 | cookie `Path=/app/gwprobe` 生效，SameSite 可设 | 是/否 | DevTools |
| 14 | 同源 POST 的 Origin/Referer 可用 | 是/否 | `/post` JSON |
| 15 | iframe 未被 XFO/CSP 挡住 | 是/否 | 响应头、桌面窗口 |

## 5. 结果记录模板

回填到 #11 评论（或本文件末尾），附截图/输出：

~~~text
飞牛版本：
appcenter-cli：
python3：
使用包：gwprobe / gwprobe2 / gwprobe3
安装输出：
list/status：
socket 路径：
ss -xl：
ss -ltnp：
管理员 /headers：
普通用户 /headers：
普通用户伪造头：
未登录 curl：
302 Location：
cookie：
POST Origin/Referer：
XFO/CSP：
结论：可用 / 不可用 / 需替代路径
不可用原因：
~~~

## 6. 清理

~~~bash
for a in gwprobe gwprobe2 gwprobe3; do
  sudo appcenter-cli uninstall "$a" 2>&1; echo "[$a uninstall=$?]"
done
sudo appcenter-cli list 2>&1 | grep -E 'gwprobe' || echo "all gateway probes gone"
~~~

卸载前若 `starting`，先等 `running` 或先 `stop`；10500 表示“还没起来”，不是永久失败。

## 7. 判定规则

- **可用**：管理员正常；普通用户 `Isadmin=false` 且伪造头被覆盖/剥离；未登录被挡；socket 只在 Unix；静态/重定向/cookie/POST 都正常。
- **需替代路径**：网关能转发，但身份头可伪造、未登录可访问、或 `allUsers=false` 不提供任何服务端限制。此时托管台必须回到“独立端口 + 管理员口令”，T3 另开票，不静默降级。
- **不可用**：三个包都无法安装/启动，或网关根本不转发。记录错误码与固件版本，T6 不能按网关方案写。
