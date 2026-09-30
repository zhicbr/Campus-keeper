param([switch]$StartNow)

$ErrorActionPreference = 'Stop'
$folder = Split-Path -Parent $MyInvocation.MyCommand.Path
$exe = Join-Path $folder 'wenchang-campus-keeper.exe'
if (-not (Test-Path -LiteralPath $exe)) { throw "请先构建 $exe" }
if (-not (Test-Path -LiteralPath (Join-Path $folder '.env'))) { throw '缺少 .env' }

$identity = [Security.Principal.WindowsIdentity]::GetCurrent()
$principal = [Security.Principal.WindowsPrincipal]::new($identity)
if (-not $principal.IsInRole([Security.Principal.WindowsBuiltInRole]::Administrator)) {
    throw '注册开机任务需要以管理员身份运行 PowerShell。'
}

$name = 'WenchangCampusKeeper'
$action = New-ScheduledTaskAction -Execute $exe -WorkingDirectory $folder
$trigger = New-ScheduledTaskTrigger -AtStartup
$settings = New-ScheduledTaskSettingsSet -ExecutionTimeLimit ([TimeSpan]::Zero) -StartWhenAvailable -RestartCount 3 -RestartInterval (New-TimeSpan -Minutes 1) -MultipleInstances IgnoreNew
Register-ScheduledTask -TaskName $name -Action $action -Trigger $trigger -Settings $settings -User 'SYSTEM' -RunLevel Highest -Force | Out-Null
Write-Host "已注册开机任务 $name。"
if ($StartNow) {
    Start-ScheduledTask -TaskName $name
    Write-Host '任务已启动。'
}
