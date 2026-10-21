<# :
@echo off
:: -------------------------------------------------------------
:: CUMT 校园网终端 
:: -------------------------------------------------------------
title CUMT 校园网终端
mode con cols=100 lines=30
color 07
chcp 65001 >nul
set "__CLOGIN_DIR__=%~dp0"

powershell -NoProfile -ExecutionPolicy Bypass -Command "iex (${%~f0} | Out-String)"
echo.
pause
exit /b
#>

# ================= 配置区 =================
$SSID = "CUMT_Stu"
$Interface = "WLAN"
try {
    $lines = netsh wlan show interfaces
    $blocks = ($lines -join "`n") -split "\n\n"

    foreach ($b in $blocks) {
        if ($b -match "已连接" -or $b -match "connected") {
            $nameLine = ($b -split "`n") | Where-Object { $_ -match "名称" }
            if ($nameLine) {
                $Interface = ($nameLine -split ":")[1].Trim()
                break
            }
        }
    }
} catch {}
$Account = $null
$Password = $null
$TargetHost = "10.2.5.251"
# ==========================================

$ScriptDir = if ($env:__CLOGIN_DIR__) { $env:__CLOGIN_DIR__ } else { (Get-Location).Path }
$EnvPath = Join-Path $ScriptDir ".env"
$EnvVars = @{}

if (-not (Test-Path $EnvPath)) {
    Write-Host ".env 文件不存在: $EnvPath" -ForegroundColor Red
    exit 1
}

foreach ($line in [System.IO.File]::ReadAllLines($EnvPath, [System.Text.Encoding]::UTF8)) {
    $trim = $line.Trim()
    if ([string]::IsNullOrWhiteSpace($trim) -or $trim.StartsWith("#")) { continue }

    $parts = $trim -split "=", 2
    if ($parts.Count -ne 2) { continue }

    $key = $parts[0].Trim()
    $value = $parts[1].Trim()

    if ($value.Length -ge 2) {
        if (($value.StartsWith('"') -and $value.EndsWith('"')) -or ($value.StartsWith("'") -and $value.EndsWith("'"))) {
            $value = $value.Substring(1, $value.Length - 2)
        }
    }

    $EnvVars[$key] = $value
}

$StudentId = $EnvVars["STUDENT_ID"]
$Password = $EnvVars["PASSWORD"]

if ([string]::IsNullOrWhiteSpace($StudentId) -or [string]::IsNullOrWhiteSpace($Password)) {
    Write-Host ".env 中必须包含 STUDENT_ID 和 PASSWORD" -ForegroundColor Red
    exit 1
}

$Account = "$StudentId@cmcc"

$ProgressPreference = 'SilentlyContinue'

# --- 基础日志与显示函数 ---
function Log($level, $msg) {
    $c = "Gray"; $t = "Cyan"
    switch ($level) {
        "INFO"    { $c="Gray";   $t="Cyan" }
        "WARN"    { $c="Yellow"; $t="Yellow" }
        "ERROR"   { $c="Red";    $t="Red" }
        "SUCCESS" { $c="Green";  $t="Green" }
        "ACTION"  { $c="White";  $t="White" }
        "RAW"     { $c="DarkGray"; $t="Magenta" }
    }
    $time = Get-Date -Format "HH:mm:ss"
    Write-Host "[$time] " -NoNewline -ForegroundColor DarkGray
    Write-Host "[$level]" -NoNewline -ForegroundColor $t
    Write-Host "`t$msg" -ForegroundColor $c
}

function Show-RawData($data) {
    if ([string]::IsNullOrWhiteSpace($data)) { return }
    Write-Host "    [原始响应] " -NoNewline -ForegroundColor Magenta
    Write-Host $data -ForegroundColor DarkGray
}

function Ask-Retry($msg) {
    Write-Host ""
    Log "WARN" "$msg"
    Write-Host "    >>> 按 [回车] 再次尝试，或按 [N] 退出..." -NoNewline -ForegroundColor Yellow
    $k = Read-Host
    if ($k -eq "n" -or $k -eq "N") { return $false }
    return $true
}

# --- 编码修复函数 ---
function Repair-Encoding($str) {
    # 将被误读为 Latin-1 (ISO-8859-1) 的字符串还原为 UTF-8
    try {
        if ([string]::IsNullOrEmpty($str)) { return $str }
        $iso = [System.Text.Encoding]::GetEncoding("ISO-8859-1")
        $utf8 = [System.Text.Encoding]::UTF8
        return $utf8.GetString($iso.GetBytes($str))
    } catch {
        return $str
    }
}

# --- 核心功能函数 ---

function Check-WLAN-Ready {
    $out = netsh wlan show interfaces 2>&1

    if ($out -match "没有无线接口" -or $out -match "There is no wireless interface") {
        return $false
    }

    return $true
}

function Repair-WLAN {
    Log "WARN" "检测到 WLAN 接口异常，正在尝试修复..."

    # 1. 重启 WLAN 服务
    net stop WlanSvc > $null 2>&1
    net start WlanSvc > $null 2>&1

    Start-Sleep 2

    # 2. 尝试启用网卡
    netsh interface set interface name="$Interface" admin=ENABLED > $null 2>&1

    Start-Sleep 2

    # 3. 再检测一次
    if (Check-WLAN-Ready) {
        Log "SUCCESS" "WLAN 接口已恢复。"
        return $true
    }

    Log "ERROR" "WLAN 接口仍不可用（可能是驱动/硬件问题）"
    return $false
}

function Check-Wifi-Adapter {
    $adapters = Get-NetAdapter -Physical | Where-Object { $_.Name -match "Wi-Fi|WLAN" }

    if (-not $adapters) {
        Log "ERROR" "系统未检测到任何 WiFi 网卡（驱动可能掉了）"
        return $false
    }

    return $true
}
function Check-Ping {
    ping -n 1 -w 1000 www.baidu.com > $null
    return ($LASTEXITCODE -eq 0)
}

function Get-IP-Internal {
    try {
        $s = New-Object System.Net.Sockets.Socket([System.Net.Sockets.AddressFamily]::InterNetwork, [System.Net.Sockets.SocketType]::Dgram, [System.Net.Sockets.ProtocolType]::Udp)
        $s.Connect($TargetHost, 80)
        $ip = $s.LocalEndPoint.Address.ToString()
        $s.Close()
        return $ip
    } catch { return $null }
}

function Check-Wifi-Connection {
    $out = netsh wlan show interfaces
    $clean = $out -replace "\s",""
    # 检查 SSID 是否匹配且状态为已连接
    if ($clean -match "SSID:$SSID" -and ($clean -match "已连接" -or $clean -match "connected")) { return $true }
    return $false
}

# --- 业务逻辑流程 ---

function Connect-Wifi-Flow {

    if (-not (Check-Wifi-Adapter)) { return $false }

    if (-not (Check-WLAN-Ready)) {
        if (-not (Repair-WLAN)) {
            Log "ERROR" "无法修复 WLAN，终止。"
            return $false
        }
    }

    while ($true) {
        if (Check-Wifi-Connection) {
            Log "INFO" "Wi-Fi 连接正常 ($SSID)。"
            return $true
        }

        Log "INFO" "尝试连接 Wi-Fi: $SSID ..."
        # 尝试连接
        netsh wlan connect name="$SSID" interface="$Interface" | Out-Null
        
        # 等待连接建立 (5秒)
        for ($i=0; $i -lt 5; $i++) {
            if (Check-Wifi-Connection) { 
                Log "SUCCESS" "Wi-Fi 连接成功。"
                return $true 
            }
            sleep 1
        }

        # 失败处理：提醒用户检查硬件开关
        if (-not (Ask-Retry "连接 Wi-Fi 失败。请检查：1. WiFi开关是否打开 2. 信号是否良好")) { return $false }
    }
}

function Get-IP-Flow {
    while ($true) {
        Log "INFO" "正在获取 IP 地址..."
        
        # 尝试 8 次自动获取 (8秒)
        for ($i=0; $i -lt 8; $i++) {
            $ip = Get-IP-Internal
            if ($ip) { 
                Log "SUCCESS" "获取 IP 成功: $ip"
                return $ip 
            }
            sleep 1
        }

        # 失败处理
        if (-not (Ask-Retry "无法获取 IP 地址。请检查是否被分配了 IP 或信号是否中断")) { return $null }
    }
}

function Login-Flow {
    # 1. 确保 WiFi 连接
    if (-not (Connect-Wifi-Flow)) { return }

    # 2. 确保获取 IP
    $ip = Get-IP-Flow
    if (-not $ip) { return }

    # 3. 循环尝试登录
    while ($true) {
        Log "INFO" "发送认证请求..."
        $ts = [int64](([datetime]::UtcNow - [datetime]::new(1970, 1, 1)).TotalMilliseconds)
        $url = "http://10.2.5.251:801/eportal/?c=Portal&a=login&callback=dr$ts&login_method=1&user_account=$Account&user_password=$Password&wlan_user_ip=$ip&wlan_user_mac=000000000000&jsVersion=3.0&_=$ts"
        
        try {
            $res = Invoke-WebRequest -Uri $url -UseBasicParsing -TimeoutSec 5
            
            # 修复并打印原始数据
            $fixedContent = Repair-Encoding $res.Content
            Show-RawData $fixedContent

            # --- 智能判定逻辑 ---
            # 成功标准：
            # 1. result:1 (标准成功)
            # 2. 包含 "已经在线" (中文提示)
            # 3. ret_code:2 (错误码2，即IP已在线)
            if ($fixedContent -match '"result":"1"' -or $fixedContent -match '已经在线' -or $fixedContent -match '"ret_code":"2"') {
                Log "SUCCESS" "认证成功！(或已在线)"
                return
            } 
            # 4. 兜底策略：如果接口返回看不懂，但 Ping 百度通了，也算成功
            elseif (Check-Ping) {
                Log "WARN" "接口返回异常，但检测到互联网已连通。"
                Log "SUCCESS" "强制判定为成功。"
                return
            }
            else {
                Log "ERROR" "认证未通过，服务器返回了错误代码。"
            }
        } catch {
            Log "ERROR" "请求发生异常: $($_.Exception.Message)"
        }

        if (-not (Ask-Retry "登录请求失败")) { return }
    }
}

function Logout-Flow {
    $ip = Get-IP-Internal
    if (-not $ip) { 
        Log "WARN" "无法获取本机 IP，可能已断网，无需注销。"
        Show-RawData "Local IP Not Found"
        return 
    }
    
    while ($true) {
        Log "INFO" "正在发送注销请求..."
        $ts = [int64](([datetime]::UtcNow - [datetime]::new(1970, 1, 1)).TotalMilliseconds)
        $url = "http://10.2.5.251:801/eportal/?c=Portal&a=logout&callback=dr$ts&login_method=1&user_account=$Account&user_password=$Password&wlan_user_ip=$ip&wlan_user_mac=000000000000&jsVersion=3.0&_=$ts"
        
        try {
            $res = Invoke-WebRequest -Uri $url -UseBasicParsing -TimeoutSec 5
            
            # 修复并打印原始数据
            $fixedContent = Repair-Encoding $res.Content
            Show-RawData $fixedContent

            if ($fixedContent -match '"result":"1"') {
                Log "SUCCESS" "注销成功。"
                return
            } else {
                Log "WARN" "注销响应异常，请检查上方原始数据。"
            }
        } catch {
            Log "ERROR" "注销请求出错: $($_.Exception.Message)"
        }

        if (-not (Ask-Retry "注销失败")) { return }
    }
}

# ================= 主控制台 =================
Clear-Host
Write-Host "==========================================" -ForegroundColor Cyan
Write-Host "      CUMT 校园网自动连接 " -ForegroundColor White
Write-Host "==========================================" -ForegroundColor Cyan
Write-Host ""

if (Check-Ping) {
    Log "SUCCESS" "互联网连接正常。"
    if (Check-Wifi-Connection) {
        Write-Host ""
        Write-Host "[$(Get-Date -Format "HH:mm:ss")] " -NoNewline -ForegroundColor DarkGray
        Write-Host "[ACTION]" -NoNewline -ForegroundColor White
        Write-Host "`t检测到已在线。是否" -NoNewline -ForegroundColor White
        Write-Host "注销" -NoNewline -ForegroundColor Red
        Write-Host "？(回车=确认 / n=取消)" -ForegroundColor White
        $in = Read-Host "     >>>"
        if ($in -eq "") { Logout-Flow }
    } else {
        Log "INFO" "当前使用的是非校园网，无需操作。"
    }
} else {
    Log "WARN" "当前未连接互联网。"
    Log "ACTION" "正在自动执行连接与登录..."
    Login-Flow
}

Write-Host ""
Write-Host "------------------------------------------" -ForegroundColor DarkGray