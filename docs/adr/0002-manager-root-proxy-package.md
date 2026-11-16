# ADR 0002：托管台 run-as root，代理应用 run-as package

- 状态：accepted
- 日期：2026-11-16
- 相关：T1 #7、T2 #4、T3 #9、V1 #10、docs/research/install-chain-verification.md

## 背景

托管台要调用 `appcenter-cli` 完成安装、卸载、启动、停止和状态查询。V1 在真机上确认：

- CLI 实际路径是 `/usr/local/bin/appcenter-cli`（v1.0.2，root:root 755）。
- 非 root 调用会因 `/run/trim_app_cgi/rpcbroker` 权限失败（panic）。
- `install-fpk` 一次约 10.3 秒；`starting` 到 `running` 可能数分钟；exit 0 不代表成功。

代理应用只做两件事：读包内配置、连目标；它不调 CLI，也不需要写系统目录。

## 决策

- 托管台 fpk 的 `config/privilege` 为 `{"defaults":{"run-as":"root"}}`。
- 代理应用 fpk 的 `config/privilege` 为 `{"defaults":{"run-as":"package"}}`。
- 托管台仍然只监听 Unix socket，不监听 TCP；root 权限只用于 CLI 和写自己的 PKGVAR。
- 代理应用只写自己的 PKGVAR，不 chown、不安装、不调用外部命令。

## 后果

- 托管台是特权进程，代码面要小：不做任意命令执行、不接受用户传路径、不把 CLI 参数拼成 shell。
- 托管台被攻破的后果比代理大；因此它不暴露 TCP、加 CSRF、错误页不泄露细节。
- 代理应用被攻破只影响自己的目标与配置，不需要 root。
- 安装代理仍需管理员，因为 `install-fpk` 由托管台以 root 调；普通用户只访问界面。

## 被否方案

- **托管台也 run-as package + sudo**：飞牛没有为应用配置免密 sudo 的稳定方式，且会把权限问题推给用户。
- **两者都 root**：代理没有 root 需求，扩大攻击面。
- **代理调 CLI 自安装**：每个包都要 root，且会把安装逻辑复制到每个包。
