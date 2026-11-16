# 安装链上机验证（V1 #10）

状态：进行中（2026-11-16）。只做验证，不改产品代码。

目标：在目标飞牛上确认 `appcenter-cli` 与本地安装/卸载/升级链路的真实行为，给「生成、安装、升级、卸载与状态存储」提供可直接引用的命令与结论。

## 环境记录

| 项 | 值 |
| --- | --- |
| 飞牛版本 | Debian 12 (bookworm)；`/etc/version` 不存在，fnOS 版本待 root 确认 |
| 内核 / 架构 | Linux 6.18.18.c1186-trim，x86_64 |
| 执行身份 | uid=1000(jeef)，gid=1001(Users)，附加组 1000(Administrators)；非 root |
| appcenter-cli 路径 | `/usr/local/bin/appcenter-cli`；`/var/apps/appcenter/target/bin/appcenter-cli` 与 `/usr/bin/appcenter-cli` 不存在 |
| appcenter-cli 权限/属主 | `-rwxr-xr-x root:root`，8,005,856 字节，ELF 64-bit x86-64，动态链接，stripped |
| CLI 哈希 | `3d7cca24523d5d03c1ad9028ca332b4410319e837a2d508d80bccb7661482626` |
| fnpack | `/usr/local/bin/fnpack` 存在 |
| 默认卷 | 待 root 确认；可见卷 /vol1、/vol2、/vol3（另有 /vol00、/vol02） |

## 第一段观察（非 root）

`appcenter-cli` 存在，但以 uid 1000 执行任何子命令都在进入参数解析前 panic，退出码 2：

```
panic: ApplyPermission for '[com.trim.sysinfo com.trim.main]' error, dial unix /run/trim_app_cgi/rpcbroker: connect: permission denied
```

这不是“CLI 已被移除”，而是 CLI 初始化 trimrpc 时需要访问 `/run/trim_app_cgi/rpcbroker`。因此第一段只能证明：CLI 文件在、路径与哈希确定、非 root 不可用；子命令列表与参数必须用 root 重跑。

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

## 第二段：安装测试（待第一段结果后补）

不在第一段执行。等确认 CLI 子命令与参数后，再给测试 fpk、安装命令、失败注入步骤与记录表。

计划覆盖：

- 最小测试包 `verify.probe`：`install-local --volume <n>` 与 `install-fpk <fpk>` 各装一次，记录工作目录、stdout/stderr、退出码、耗时。
- 安装后 `list` 是否出现、`start`/`stop` 是否可用、桌面入口是否出现。
- 卸载：`uninstall` 是否需要先 `stop`，卸载后 `list` 是否清理。
- 升级：同 appname 更高 version 覆盖安装 vs 卸载重装；失败时停在什么状态。
- 失败注入：坏 checksum、appname 冲突、端口被占、`desktop_applaunchname` 不匹配、包结构缺文件。
- 结论：可用 / 不可用 / 需替代路径；给 T2 可直接复制的命令清单。

## 结果与结论

待填。
