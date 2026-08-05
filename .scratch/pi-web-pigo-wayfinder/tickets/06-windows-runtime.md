# 06 Windows 运行前提

**Type:** research

## Question

pi-web 在 Windows 上运行 node-pty/sessiond 的前提是否齐全；`pigo --acp` 默认命令与本地 exe 覆盖路径；pigo bash 工具在无 Git Bash/WSL 环境已回退 PowerShell 的验证；端口/服务注册的 Windows 差异。

## Status

resolved

## Resolution

Node v24.11.1 / npm 11.6.2 满足 pi-web 要求；node-pty 需在 `npm install` 时使用 Windows prebuild（本机未装 node_modules，M1 安装时验证）；pi-web 支持 Windows 绝对路径；pigo 的 WSL relay 误选已修复并回退 PowerShell。剩余 Windows 运行验证随 M1 联调完成。
