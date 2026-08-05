# 03 sessiond 进程生命周期

**Type:** grilling

## Question

按 workspace 一个常驻 pigo 进程：spawn 时机、会话到进程的路由、空闲回收、配置保存后重启、pigo 崩溃/断开的检测与重连、sessiond 重启后已打开会话的恢复。崩溃时正在跑的会话如何呈现给用户。

## Status

resolved

## Resolution

3.1: 检测到 pigo 进程退出后，正在跑的会话标记“中断”，用户可重新打开恢复；空闲会话不受影响。
3.2: pigo 进程按需 spawn，无打开会话且空闲 10 分钟后关闭。
3.3: 配置保存时提示“配置变更将重启 pigo”，确认后重启，运行中会话标记中断。
