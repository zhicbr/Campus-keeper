# 南湖／文昌校园网守护程序

本程序用于 Windows 校园网有线和 Wi-Fi 连接。`version1.0` 标签保留首次跑通文昌认证的基线。可执行文件仍叫 `wenchang-campus-keeper.exe`，以兼容现有启动方式和计划任务。

排查间歇断网时，可带 `-diagnostic` 参数启动或运行名称包含 `diagnostic` 的程序，以开启详细的系统路由及网络现场记录。

## 运行方式

程序从可执行文件同目录的 `.env` 读取 `STUDENT_ID` 和 `PASSWORD`。默认 `CAMPUS=auto`：读取所选网卡的 IPv4、默认网关及网关 MAC，三项同时匹配已记录样本时识别文昌或南湖。两个校区都可能出现 `/a79.htm`，也共用 `CUMT_Stu` 和认证服务器；这些单独都不能判断校区。若当前网络未匹配样本，可在 `.env` 设置 `CAMPUS=nanhu` 或 `CAMPUS=wenchang`；显式设置优先。两个校区目前各只有少量现场样本，其他网段仍需后续验证。

当前账号已在两校区通过 `@xyw` 登录，因此默认后缀为 `@xyw`，与校区无关。若使用旧脚本的中国移动账号或其他运营商，在 `.env` 填 `ACCOUNT_SUFFIX=@cmcc`、`ACCOUNT_SUFFIX=@dx`、`ACCOUNT_SUFFIX=@lt` 等；明确需要空后缀则填 `ACCOUNT_SUFFIX=`。若 `STUDENT_ID` 已含 `@`，直接使用该完整账号。账号和密码不写入日志。

默认在系统托盘运行，不显示终端：

```powershell
Start-Process .\wenchang-campus-keeper.exe
```

双击托盘图标可显示或隐藏日志窗口；点击日志窗口右上角的 × 只会隐藏窗口。只有右键托盘图标选择“退出程序”才会停止程序。启动时直接显示日志窗口可运行 `Start-Process .\wenchang-campus-keeper.exe -ArgumentList '-console'`。程序会在网卡断开、重新接入、取得 IPv4 地址时报告变化；每 20 秒检查一次 IPv4 外网。外网正常时约每分钟记录一条状态，不重复记录每次正常检查；断网时保持较密集的状态输出。Windows 的网络变化通知用于及时唤醒，另有每 5 秒的本地网卡核查兜底。无线网卡短暂消失会先等待 2 秒复核。断网后才会向认证接口提交账号密码。

## 重试和恢复

- 认证失败时指数退避，从 5 秒增加到最多 5 分钟；退避期间仍检查外网和网卡，若网络自行恢复会立即清零。
- 每天北京时间 **06:00** 清零退避。程序重启后也从基础退避开始。
- 优先使用已有 IPv4 的有线网卡；有线不可用时，调用 Windows WLAN API 连接已保存的 `CUMT_Stu` 配置，取得 IPv4 后再检查外网并尝试网页认证。配置即使设为“手动连接”，程序运行时仍可发起连接。若无线网已连接其他网络，程序不会强制切换。两种接口都不可用时持续重试，Wi-Fi 连接请求采用指数退避，最多五分钟。程序不会主动断开现有连接。
- 外网检测及认证请求绑定到当前选中网卡的 IPv4 地址；网络切换时清除旧认证缓存。未呈现校园网特征的网络只检测外网，不发送校园网账号密码。
- 两校区都可能出现 `10.2.5.251/a79.htm`，认证请求送往固定的 `:801/eportal/?c=Portal&a=login`。程序优先从本轮认证跳转读取 MAC；跳转未携带 MAC 时自动使用当前网卡的物理 MAC 地址，防止网关因全零 MAC 分配 12 分钟临时租约。
- 处于校园 Wi-Fi 且外网中断时，程序核查网关 ARP 邻居状态；若网关持续无响应（如漫游至故障 AP），自动触发断开重连以促使 Windows 漫游至健康 AP。
- 发送账号密码前重新核验外网，已经恢复就取消登录。跳转发现失败时，不再仅凭 `PORTAL_URL`、缓存或默认入口自动登录，继续探测并记录缺少认证证据。账号密码只发送到固定的已知服务器 `10.2.5.251:801`。
- 认证接口的返回值只表示请求被接受；程序以实际 IPv4 外网探测作为恢复成功的最终判据。

## 开机常驻

完成本机断网验证后，在当前用户的 PowerShell 运行：

```powershell
.\install-task.ps1 -StartNow
```

这会创建 `WenchangCampusKeeper` 计划任务，在当前用户登录 Windows 后运行并显示托盘图标；程序异常退出时任务尝试重启，每天 06:05 还有一次补启动触发。后台使用 `-quiet`，仅记录状态变化和操作，减少日志量。托盘图标需要该用户保持登录。

查看任务：`Get-ScheduledTask -TaskName WenchangCampusKeeper`。停止：`Stop-ScheduledTask -TaskName WenchangCampusKeeper`。计划任务与手动运行不可同时启动，程序用单实例锁防止重复登录。

远程使用期间，电脑需保持通电且不进入睡眠。有线断开后程序可转接已保存的 Wi-Fi，但切换、DHCP 和认证期间仍会短暂断网，远程会话能否自动恢复取决于远程软件。

日志写在同目录 `keeper.log`，达到 5 MiB 后归档为带时间戳的文件，不自动永久删除。`.env`、日志、可执行文件和缓存均已加入 `.gitignore`。

## 构建

```powershell
go test -buildvcs=false .
go build -buildvcs=false -ldflags="-H=windowsgui -s -w" -o .\wenchang-campus-keeper.exe .
```

> **注意**：必须使用 `-ldflags="-H=windowsgui"` 参数将程序标记为 Windows GUI 子系统，否则双击运行时 Windows 会自动附带弹出黑色控制台终端窗口。

Go 代码只依赖标准库。南湖请求根据原 `clogin.bat` 实现；该脚本不再参与运行。明天到南湖后需由用户现场验证实际认证跳转及登录结果，详见 [DESIGN.md](DESIGN.md)。
