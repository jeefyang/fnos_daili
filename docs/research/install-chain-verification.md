# 安装链上机验证（V1 #10）

状态：进行中（2026-11-16）。只做验证，不改产品代码。

目标：在目标飞牛上确认 `appcenter-cli` 与本地安装/卸载/升级链路的真实行为，给「生成、安装、升级、卸载与状态存储」提供可直接引用的命令与结论。

## 环境记录

| 项 | 值 |
| --- | --- |
| 飞牛版本 | fnOS v1.2.1011，底层 Debian 12 (bookworm)；`/etc/version` 不存在，版本在 `/etc/issue` |
| 内核 / 架构 | Linux 6.18.18.c1186-trim，x86_64 |
| 执行身份 | root 下 uid=0(root)；普通用户 uid=1000(jeef)，附加组 1000(Administrators)，非 root 不可用 |
| appcenter-cli 路径 | `/usr/local/bin/appcenter-cli`；`/var/apps/appcenter/target/bin/appcenter-cli` 与 `/usr/bin/appcenter-cli` 不存在 |
| appcenter-cli 权限/属主 | `-rwxr-xr-x root:root`，8,005,856 字节，ELF 64-bit x86-64，动态链接，stripped |
| CLI 哈希 | `3d7cca24523d5d03c1ad9028ca332b4410319e837a2d508d80bccb7661482626` |
| fnpack | `/usr/local/bin/fnpack` 存在 |
| 默认卷 | 1；可见卷 /vol1、/vol2、/vol3（另有 /vol00、/vol02） |
| rpcbroker | `/run/trim_app_cgi` 777 root:root；`rpcbroker` socket 660 root:root |
| CLI 版本 | 1.0.2（`--help` 输出；`--version` 不是合法 flag） |

## 第一段观察（非 root）

`appcenter-cli` 存在，但以 uid 1000 执行任何子命令都在进入参数解析前 panic，退出码 2：

```
panic: ApplyPermission for '[com.trim.sysinfo com.trim.main]' error, dial unix /run/trim_app_cgi/rpcbroker: connect: permission denied
```

这不是“CLI 已被移除”，而是 CLI 初始化 trimrpc 时需要访问 `/run/trim_app_cgi/rpcbroker`。因此第一段只能证明：CLI 文件在、路径与哈希确定、非 root 不可用；子命令列表与参数必须用 root 重跑。

## 第一段观察（root）

root 下 CLI 正常，版本 1.0.2。实际子命令：

```
install        Install an application.
uninstall      Uninstall an application.
start          Start an application.
stop           Stop an application.
check          Check whether an application is installed or not.
status         Get status of an installed application.
list           List all installed application informations.
install-fpk    Install an application from fpk.
install-local  Install an application from local application directory.
manual-install Enable or disable manual installation feature in App Center.
default-volume Setting the default volume for new application installations from the CLI.
```

关键参数（help 原文）：

- `install-local`：`-a/--autostart`（默认 yes）、`-d/--dir`（应用源目录，默认当前目录）、`-e/--env`（环境变量文件，默认 ./.env）、`-v/--volume`（安装卷；升级时忽略）。
- `install-fpk`：`-e/--env`、`-v/--volume`（升级时忽略）；位置参数 `[fpk]`。
- `uninstall`、`start`、`stop`、`list`、`default-volume` 的 help 只显示命令名，没有列出 appname 位置参数；真实调用语法待安装测试确认。
- `default-volume` 无参数输出 `1`。
- `list` 输出带边框的表格：APP NAME / DISPLAY NAME / VERSION / STATUS / DEPENDENCY APPS；退出码 0。

社区“install-fpk/install-local 在 1.1.31xx 已移除”的说法在 fnOS v1.2.1011 上不成立。

## 第一段：只读侦察（先跑，不安装）

以 root 执行。这一段只读：不 install、不 uninstall、不 stop 任何应用。把整段原始输出贴回票里即可。

```sh
set -u

echo "=== date / host ==="
date
hostname

echo "=== fnOS version ==="
cat /etc/version 2>&1 || true
cat /etc/os-release 2>&1 || true

echo "=== kernel / arch ==="
uname -a
uname -m
getconf LONG_BIT 2>&1 || true

echo "=== identity ==="
id

echo "=== appcenter process ==="
ps -eo user,pid,args 2>/dev/null | grep -i '[a]ppcenter' || true

echo "=== candidate paths ==="
for p in /var/apps/appcenter/target/bin/appcenter-cli /usr/bin/appcenter-cli /usr/local/bin/appcenter-cli; do
  if [ -e "$p" ]; then
    echo "--- $p"
    ls -l "$p"
    file "$p" 2>&1 || true
    sha256sum "$p" 2>&1 || true
  else
    echo "MISSING $p"
  fi
done

echo "=== PATH lookup ==="
command -v appcenter-cli 2>&1 || true
command -v fnpack 2>&1 || true

CLI="$(command -v appcenter-cli 2>/dev/null || true)"
[ -n "$CLI" ] || CLI=/var/apps/appcenter/target/bin/appcenter-cli
echo "CLI=$CLI"

echo "=== CLI identity ==="
ls -l "$CLI" 2>&1 || true
sha256sum "$CLI" 2>&1 || true

echo "=== appcenter-cli --help ==="
"$CLI" --help 2>&1; echo "[exit=$?]"

echo "=== appcenter-cli --version ==="
"$CLI" --version 2>&1; echo "[exit=$?]"

echo "=== appcenter-cli list ==="
"$CLI" list 2>&1; echo "[exit=$?]"

echo "=== appcenter-cli default-volume ==="
"$CLI" default-volume 2>&1; echo "[exit=$?]"

echo "=== subcommand help ==="
for sub in install-local install-fpk uninstall start stop list default-volume; do
  echo "--- $sub --help"
  "$CLI" "$sub" --help 2>&1
  echo "[exit=$?]"
  echo "--- $sub help"
  "$CLI" help "$sub" 2>&1
  echo "[exit=$?]"
done

echo "=== volumes ==="
ls -ld /vol* 2>&1 || true
df -h /vol* 2>&1 || true
```

### 第一段要回答

1. `appcenter-cli` 是否还在、在哪、是否只有 root 能执行。
2. `--help` 实际列出哪些子命令；`install-fpk`、`install-local`、`uninstall` 是否都在。
3. 每个子命令的真实参数（尤其 `--volume`、工作目录、`--force`、`--yes`）。
4. `list` 与 `default-volume` 的输出格式，便于后续解析。
5. 固件版本与 CLI 哈希，便于判断社区“1.1.31xx 已移除”的说法是否适用于这台机器。

## 第二段：安装测试

测试包是 [fixtures/verify.probe](fixtures/verify.probe) 与 [fixtures/verify.probe.fpk](fixtures/verify.probe.fpk)：

- appname `verify.probe`，version `1.0.0`，入口 `verify.probe.Application`，端口 28999，不提供真实 HTTP 服务。
- `cmd/main` 的 start 只启动 `sleep 600`；用来验证 install/start/stop/status/uninstall。
- `config/privilege` 为 package；`config/resource` 为空。
- fpk 由 fnpack 1.2.3 生成，`cmd/*` 在包内为 0755；`checksum=5a537ff21c5a0eb9224c43e5845330f9`。

### 2.1 先补 help

```sh
for sub in check status install manual-install; do
  echo "--- $sub --help"
  appcenter-cli "$sub" --help 2>&1
  echo "[exit=$?]"
done
```

### 2.2 install-fpk

```sh
F=/tmp/verify.probe.fpk
curl -fL -o "$F" https://raw.githubusercontent.com/jeefyang/fnos_daili/main/docs/research/fixtures/verify.probe.fpk

start=$(date +%s%N)
appcenter-cli install-fpk -v 1 "$F" 2>&1
rc=$?
end=$(date +%s%N)
echo "[exit=$rc] [elapsed_ms=$(( (end - start) / 1000000 ))]"

appcenter-cli list 2>&1 | grep verify.probe || true
appcenter-cli check verify.probe 2>&1; echo "[check=$?]"
appcenter-cli status verify.probe 2>&1; echo "[status=$?]"
```

### 2.3 启动/停止

```sh
appcenter-cli start verify.probe 2>&1; echo "[start=$?]"
appcenter-cli status verify.probe 2>&1; echo "[status=$?]"
appcenter-cli stop verify.probe 2>&1; echo "[stop=$?]"
appcenter-cli status verify.probe 2>&1; echo "[status=$?]"
```

### 2.4 卸载

先试 appname 位置参数；若报错，原样贴回，不要继续猜：

```sh
appcenter-cli stop verify.probe 2>&1; echo "[stop=$?]"
appcenter-cli uninstall verify.probe 2>&1; echo "[uninstall=$?]"
appcenter-cli list 2>&1 | grep verify.probe || echo "verify.probe gone"
```

### 2.5 install-local

```sh
S=/tmp/verify.probe-src.tar.gz
curl -fL -o "$S" https://raw.githubusercontent.com/jeefyang/fnos_daili/main/docs/research/fixtures/verify.probe-src.tar.gz
rm -rf /tmp/verify.probe-src && mkdir -p /tmp/verify.probe-src
tar -xzf "$S" -C /tmp/verify.probe-src
chmod +x /tmp/verify.probe-src/verify.probe/cmd/*

start=$(date +%s%N)
appcenter-cli install-local -d /tmp/verify.probe-src/verify.probe -v 1 2>&1
rc=$?
end=$(date +%s%N)
echo "[exit=$rc] [elapsed_ms=$(( (end - start) / 1000000 ))]"

appcenter-cli list 2>&1 | grep verify.probe || true
```

### 2.6 失败注入（单独一轮，确认安装/卸载语法后再做）

- 改 fpk 一个字节（不碰 manifest 的 checksum 行），再装。
- 同 appname 已安装时再装一次。
- 端口 28999 被占时再装。
- 把 manifest 的 `desktop_applaunchname` 改成不存在的入口再装。
- 删掉 `config/resource` 或 `ICON_256.PNG` 再装。
每种都记录：CLI 输出、退出码、`list` 状态、应用中心是否留下半装状态。

## 第二段观察（2026-11-16 11:43）

- 下载的 fpk SHA256：`4786c2d649b33b4309ac1fe308fc90b401679d02ea3e84d08f73548c791de094`。
- `install-fpk -v 1`：退出码 0，耗时 10,263 ms；输出 `Verifying files.....`、`installing......`、`[Info]Installation complete...`。
- 安装后 `list`：`verify.probe / Verify Probe / 1.0.0 / starting`。
- `check verify.probe`：输出 `Installed`，退出码 0（help 未列 appname，但实际接受位置参数）。
- `status verify.probe`：输出 `starting`，退出码 0（同样实际接受 appname）。
- `start verify.probe`：输出 `[Error]Something wrong with appcenter: code 10500`、`[Error]Failed to launch app. error code 10500..`，但**退出码 0**。
- `stop verify.probe`：同样报 10500，**退出码 0**；状态仍是 `starting`。
- 11:47:27 复查：`list`/`status` 已变成 `running`；进程为 `verify.+ 421125 1 S sleep 600`；`/vol1/@appdata/verify.probe/info.log` 只有 `2026-11-16 11:43:55 - Starting process ...`；28999 没有监听。
- 因此 10500 是“应用还在 starting 状态”时 start/stop 的拒绝，不是安装失败；安装后到 running 之间有明显延迟（本例约 3.5 分钟）。T2 必须轮询 `status`，不能 install 完立刻 start/stop，也不能只看 exit code。
- 部署目录 `/vol1/@appcenter/verify.probe` 只有 config/server/ui/www，没有 cmd/main；cmd 脚本由 appcenter 安装时消费，不落在部署目录。目录与 appdata 都归 `verify.probe:verify.probe`。
- 结论（暂定）：本地安装链路可用；start/stop 的退出码不可靠，状态迁移要按 status 轮询。待 running 状态下复测 stop/start。

## 结果与结论

待填。
