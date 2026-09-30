# 文昌校区校园网守护程序

该程序持续检测 IPv4 外网，每轮都输出当前状态，通常约每 5 秒检查一次；连续两次检测失败后报告断网并尝试认证。认证失败时从 5 秒开始指数退避，最多 5 分钟；退避期间仍约每 5 秒检查并输出状态。网线或校园网 IPv4 接口不可用时约每 5 秒检查一次，不向认证服务器重复发送请求。已识别文昌有线网的 `a79.htm` 认证跳转，并使用同主机 `:801/eportal/` 接口；一次断网实测中已成功恢复外网。

## 构建

```powershell
cd D:\dev\workspace\workspace-26-b\try-codex\wenchang-campus-keeper
& 'D:\dev\envs\Go\bin\go.exe' build -buildvcs=false -o .\wenchang-campus-keeper.exe .
```

登录凭据从同目录 `.env` 读取，沿用 `STUDENT_ID` 和 `PASSWORD`。文昌有线认证页默认选择“校园用户”（账号后缀 `@xyw`），程序会在识别到 `a79.htm` 跳转时自动添加。若你手动登录时选择了其他运营商，请在 `.env` 添加相应的 `ACCOUNT_SUFFIX`，例如 `@dx`、`@lt`；若明确要空后缀，添加空的 `ACCOUNT_SUFFIX=`。如自动发现的认证入口不正确，可添加 `PORTAL_URL=http://认证服务器/a79.htm`。不要把 `.env`、日志分享出去。

## 断网验证

请在电脑本机操作，先启动程序：

```powershell
.\wenchang-campus-keeper.exe
```

看到“外网连接正常”后，再手动退出校园网认证，**保持网线连接和程序窗口运行**。程序应检测掉线并尝试恢复；查看同目录 `keeper.log` 中的结果。若恢复失败，请先通过浏览器手动重新登录，再把日志中的错误信息发来。按 Ctrl+C 停止测试程序。

`-once` 仅在已经断网、且本机可以操作终端时使用。若日志出现“不是已知协议”，需要根据实际认证页面补充适配。

## 常驻运行

验证通过后，管理员 PowerShell 中运行：

```powershell
.\install-task.ps1 -StartNow
```

这会注册名为 `WenchangCampusKeeper` 的开机计划任务，以 SYSTEM 账户运行，无须用户登录。查看状态：`Get-ScheduledTask -TaskName WenchangCampusKeeper`；停止：`Stop-ScheduledTask -TaskName WenchangCampusKeeper`。电脑仍须保持通电、网线连接和网络适配器正常工作。

原南湖脚本 `clogin.bat` 已按要求移入本工作区作为参考；它的 Wi-Fi 登录逻辑不用于文昌有线网。
