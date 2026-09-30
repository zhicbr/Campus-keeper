param([switch]$StartNow)

$ErrorActionPreference = 'Stop'
$folder = Split-Path -Parent $MyInvocation.MyCommand.Path
$exe = Join-Path $folder 'wenchang-campus-keeper.exe'
if (-not (Test-Path -LiteralPath $exe)) { throw "请先构建 $exe" }
if (-not (Test-Path -LiteralPath (Join-Path $folder '.env'))) { throw '缺少 .env' }

$identity = [Security.Principal.WindowsIdentity]::GetCurrent()
$name = 'WenchangCampusKeeper'
$action = New-ScheduledTaskAction -Execute $exe -Argument '-quiet' -WorkingDirectory $folder
$logon = New-ScheduledTaskTrigger -AtLogOn -User $identity.Name
$daily = New-ScheduledTaskTrigger -Daily -At '06:05'
$settings = New-ScheduledTaskSettingsSet -ExecutionTimeLimit ([TimeSpan]::Zero) -StartWhenAvailable -RestartCount 999 -RestartInterval (New-TimeSpan -Minutes 1) -MultipleInstances IgnoreNew
$taskPrincipal = New-ScheduledTaskPrincipal -UserId $identity.Name -LogonType Interactive -RunLevel Limited
Register-ScheduledTask -TaskName $name -Action $action -Trigger @($logon, $daily) -Settings $settings -Principal $taskPrincipal -Force | Out-Null
Write-Host "已注册登录任务 $name，托盘图标将在当前用户登录后显示。"
if ($StartNow) {
    Start-ScheduledTask -TaskName $name
    Write-Host '任务已启动。'
}
