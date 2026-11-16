# V1 测试包：verify.probe

一次性测试包，只用于验证 fnOS 本地安装链路，不是产品包。

- appname：`verify.probe`
- version：`1.0.0`
- display_name：`Verify Probe`
- 入口：`verify.probe.Application`，type=url，port=28999，url=/
- 生命周期：`cmd/main` 的 start 只启动 `sleep 600`；没有真实 HTTP 服务，用来验证 install/start/stop/status/uninstall，不验证页面
- 权限：`config/privilege` 为 package；`config/resource` 为空
- 包：`verify.probe.fpk`，fnpack 1.2.3 生成，`cmd/*` 已改为 0755
- 源码：`verify.probe/`，可直接给 `install-local -d`

## 安装

```sh
# fpk 路径安装
appcenter-cli install-fpk -v 1 /path/to/verify.probe.fpk

# 源码目录安装（先在 Linux 上确保 cmd/* 可执行）
chmod +x /path/to/verify.probe/cmd/*
appcenter-cli install-local -d /path/to/verify.probe -v 1
```

## 清理

```sh
appcenter-cli stop verify.probe
appcenter-cli uninstall verify.probe
```

若 `uninstall` 的参数形式不是 appname，以 CLI 实际报错为准；这个包可以随时从应用中心手动卸载。
