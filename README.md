# 文昌校区校园网守护程序

本程序用于 Windows 有线校园网。版本 `version1.0` 是首次跑通认证的基线；当前代码在此基础上重构了网络监视、认证重试、日志和开机运行。

## 运行方式

程序从可执行文件同目录的 `.env` 读取 `STUDENT_ID` 和 `PASSWORD`。文昌认证页默认的“校园用户”会使用 `@xyw` 后缀。若你选择其他运营商，可在 `.env` 中填写 `ACCOUNT_SUFFIX=@dx` 或 `ACCOUNT_SUFFIX=@lt`；若明确需要空后缀，填写 `ACCOUNT_SUFFIX=`。账号、密码不会写入日志。

默认在系统托盘运行，不显示终端：

```powershell
cd D:\dev\workspace\workspace-26-b\try-codex\wenchang-campus-keeper
Start-Process .\wenchang-campus-keeper.exe
```

双击托盘图标可显示或隐藏日志窗口；点击日志窗口右上角的 × 只会隐藏窗口。只有右键托盘图标选择“退出程序”才会停止程序。启动时直接显示日志窗口可运行 `Start-Process .\wenchang-campus-keeper.exe -ArgumentList '-console'`。程序会在网卡断开、重新接入、取得 IPv4 地址时报告变化；每 10 秒显示一次当前状态，每 20 秒检查一次 IPv4 外网。Windows 的网络变化通知用于及时唤醒，另有每 5 秒的本地网卡核查兜底。断网后才会向认证接口提交账号密码。

## 重试和恢复

- 认证失败时指数退避，从 5 秒增加到最多 5 分钟；退避期间仍检查外网和网卡，若网络自行恢复会立即清零。
- 每天北京时间 **06:00** 清零退避。程序重启后也从基础退避开始。
- 只在有线接口拥有校园网 IPv4 地址时尝试认证。若网线断开，等待接口恢复，不重复发送登录请求。
- 当前认证页为 `10.2.5.251/a79.htm`，其配置对应 `:801/eportal/?c=Portal&a=login`。程序优先通过认证跳转获取 IP、MAC 和 BRAS 名称。若跳转发现失败，可在 `.env` 指定 `PORTAL_URL=http://10.2.5.251/a79.htm` 作为后备入口；此时可能缺少跳转中的 MAC 和 BRAS 参数。
- 认证接口的返回值只表示请求被接受；程序以实际 IPv4 外网探测作为恢复成功的最终判据。

## 开机常驻

完成本机断网验证后，在当前用户的 PowerShell 运行：

```powershell
.\install-task.ps1 -StartNow
```

这会创建 `WenchangCampusKeeper` 计划任务，在当前用户登录 Windows 后运行并显示托盘图标；程序异常退出时任务尝试重启，每天 06:05 还有一次补启动触发。后台使用 `-quiet`，仅记录状态变化和操作，减少日志量。托盘图标需要该用户保持登录。

查看任务：`Get-ScheduledTask -TaskName WenchangCampusKeeper`。停止：`Stop-ScheduledTask -TaskName WenchangCampusKeeper`。计划任务与手动运行不可同时启动，程序用单实例锁防止重复登录。

远程使用期间，电脑需保持通电且不进入睡眠；程序无法在关机、休眠或网线物理断开时维持远程连接。

日志写在同目录 `keeper.log`，达到 5 MiB 后归档为带时间戳的文件，不自动永久删除。`.env`、日志、可执行文件和缓存均已加入 `.gitignore`。

## 构建

```powershell
& 'D:\dev\envs\Go\bin\go.exe' test -buildvcs=false .
& 'D:\dev\envs\Go\bin\go.exe' build -buildvcs=false -ldflags='-H=windowsgui' -o .\wenchang-campus-keeper.exe .
```

Go 代码只依赖标准库。原南湖脚本 `clogin.bat` 留在工作区作为历史参考，不参与文昌登录。
