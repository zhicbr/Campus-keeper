//go:build windows

package diagnostics

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"net/http/httptrace"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sync"
	"sync/atomic"
	"syscall"
	"time"
)

// Diagnostics observe independently of the controller, including while login
// blocks. They never submit credentials or change routes/WLAN configuration.
type Diagnostics struct {
	Log         *log.Logger
	Writer      *RotatingLog
	cancel      context.CancelFunc
	wg          sync.WaitGroup
	wake        chan string
	sequence    atomic.Uint64
	linkSampler func() (ifIndex int, ipv4 string, gateway string)
}

func StartDiagnostics(parent context.Context, dir string, operational *log.Logger) (*Diagnostics, error) {
	w, err := NewRotatingLog(filepath.Join(dir, "network-diagnostic.log"), 32<<20)
	if err != nil {
		return nil, err
	}
	w.Silent = true
	ctx, cancel := context.WithCancel(parent)
	d := &Diagnostics{
		Log:    log.New(w, "", log.Ldate|log.Ltime|log.Lmicroseconds),
		Writer: w,
		cancel: cancel,
		wake:   make(chan string, 32),
	}
	d.Log.Printf("SESSION START pid=%d go=%s build=%s timezone=%s interval: probes=5s snapshot=15s events=30s; normal recovery enabled", os.Getpid(), runtime.Version(), diagnosticBuild, time.Now().Format("-0700"))
	operational.Print("持续网络诊断已开启：network-diagnostic.log（32 MiB 分卷，历史日志保留）；正常使用及开关 VPN 即可")
	d.wg.Add(4)
	go func() { defer d.wg.Done(); d.probes(ctx) }()
	go func() { defer d.wg.Done(); d.snapshots(ctx) }()
	go func() { defer d.wg.Done(); d.events(ctx) }()
	go func() { defer d.wg.Done(); d.proxyProbes(ctx) }()
	return d, nil
}

func (d *Diagnostics) SetLinkSampler(sampler func() (ifIndex int, ipv4 string, gateway string)) {
	d.linkSampler = sampler
}

var diagnosticBuild = "development"

func (d *Diagnostics) Close() {
	d.cancel()
	d.wg.Wait()
	d.Log.Print("SESSION END")
	d.Writer.Close()
}

func (d *Diagnostics) Trigger(reason string) {
	d.Log.Printf("snapshot requested reason=%q", reason)
	select {
	case d.wake <- reason:
	default:
		d.Log.Printf("snapshot queue full reason=%q; periodic snapshots remain active", reason)
	}
}

// DiagnosticError unwraps credential-bearing request URLs. Unwrap before logging;
// neither request bodies, query strings nor authentication responses are logged.
func DiagnosticError(err error) string {
	if err == nil {
		return ""
	}
	var u *url.Error
	if errors.As(err, &u) {
		return DiagnosticError(u.Err)
	}
	return fmt.Sprintf("%T: %s", err, err.Error())
}

func SafePortal(raw string) string {
	if raw == "" {
		return ""
	}
	u, err := url.Parse(raw)
	if err != nil {
		return "<invalid>"
	}
	return u.Scheme + "://" + u.Host + u.Path
}

func (d *Diagnostics) Request(client *http.Client, r *http.Request, label string) (*http.Response, error) {
	id := d.sequence.Add(1)
	started := time.Now()
	target := SafePortal(r.URL.String())
	d.Log.Printf("http begin id=%d kind=%q target=%q", id, label, target)
	trace := &httptrace.ClientTrace{
		DNSStart: func(i httptrace.DNSStartInfo) { d.Log.Printf("http dns-start id=%d host=%q", id, i.Host) },
		DNSDone: func(i httptrace.DNSDoneInfo) {
			d.Log.Printf("http dns-done id=%d addresses=%v coalesced=%t error=%q elapsed=%s", id, i.Addrs, i.Coalesced, DiagnosticError(i.Err), time.Since(started))
		},
		ConnectStart: func(network, address string) {
			d.Log.Printf("http connect-start id=%d network=%q address=%q", id, network, address)
		},
		ConnectDone: func(network, address string, err error) {
			d.Log.Printf("http connect-done id=%d network=%q address=%q error=%q elapsed=%s", id, network, address, DiagnosticError(err), time.Since(started))
		},
		TLSHandshakeStart: func() { d.Log.Printf("http tls-start id=%d", id) },
		TLSHandshakeDone: func(s tls.ConnectionState, err error) {
			d.Log.Printf("http tls-done id=%d version=%x cipher=%x error=%q elapsed=%s", id, s.Version, s.CipherSuite, DiagnosticError(err), time.Since(started))
		},
		WroteRequest: func(i httptrace.WroteRequestInfo) {
			d.Log.Printf("http wrote id=%d error=%q elapsed=%s", id, DiagnosticError(i.Err), time.Since(started))
		},
		GotFirstResponseByte: func() {
			d.Log.Printf("http ttfb id=%d elapsed=%s", id, time.Since(started))
		},
	}
	resp, err := client.Do(r.WithContext(httptrace.WithClientTrace(r.Context(), trace)))
	status := 0
	redirect := ""
	if resp != nil {
		status = resp.StatusCode
		if location, locErr := resp.Location(); locErr == nil {
			redirect = SafePortal(location.String())
		}
	}
	d.Log.Printf("http end id=%d kind=%q target=%q status=%d redirect=%q error=%q elapsed=%s", id, label, target, status, redirect, DiagnosticError(err), time.Since(started))
	return resp, err
}

func (d *Diagnostics) probes(ctx context.Context) {
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.Proxy = nil // Observe OS routes/TUN, without silently using an environment proxy.
	transport.DisableKeepAlives = true
	transport.DialContext = (&net.Dialer{Timeout: 3 * time.Second}).DialContext
	client := &http.Client{Transport: transport, Timeout: 4 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	defer transport.CloseIdleConnections()
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()
	for {
		if ctx.Err() != nil {
			return
		}
		var jobs sync.WaitGroup
		if d.linkSampler != nil {
			ifIndex, ipv4, gateway := d.linkSampler()
			if ifIndex != 0 && ipv4 != "" {
				d.Log.Printf("independent link sample ifindex=%d ipv4=%s gateway=%s", ifIndex, ipv4, gateway)
				if gateway != "" {
					jobs.Add(1)
					go func() {
						defer jobs.Done()
						d.command(ctx, "campus-gateway-ping", 4*time.Second, "ping.exe", "-4", "-S", ipv4, "-n", "1", "-w", "1500", gateway)
					}()
				}
			}
		}
		for _, host := range []string{"www.baidu.com", "www.google.com"} {
			jobs.Add(1)
			go func(host string) {
				defer jobs.Done()
				r, _ := http.NewRequestWithContext(ctx, http.MethodGet, "https://"+host+"/", nil)
				resp, err := d.Request(client, r, "system-route-https")
				if resp != nil {
					_, bodyErr := io.Copy(io.Discard, io.LimitReader(resp.Body, 4096))
					resp.Body.Close()
					d.Log.Printf("system https body host=%q error=%q", host, DiagnosticError(bodyErr))
				}
				if err != nil && ctx.Err() == nil {
					d.Log.Printf("system target failed host=%q (not a login trigger)", host)
				}
			}(host)
			jobs.Add(1)
			go func(host string) {
				defer jobs.Done()
				d.command(ctx, "system-route-ping:"+host, 4*time.Second, "ping.exe", "-n", "1", "-w", "1500", host)
			}(host)
		}
		jobs.Wait()
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

// Powershell emits UTF-8, but native ping/netsh can use the OEM code page.
// Keep their original bytes in quoted output rather than guessing the encoding.
func (d *Diagnostics) command(ctx context.Context, label string, timeout time.Duration, name string, args ...string) error {
	child, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	start := time.Now()
	cmd := exec.CommandContext(child, name, args...)
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
	out, err := cmd.CombinedOutput()
	d.Log.Printf("command kind=%q elapsed=%s error=%q context=%q bytes=%d output=%q", label, time.Since(start), DiagnosticError(err), DiagnosticError(child.Err()), len(out), string(out))
	return err
}

const snapshotScript = `
$OutputEncoding = [Console]::OutputEncoding = [System.Text.UTF8Encoding]::new($false)
$ErrorActionPreference = 'Continue'
function Section($name, [scriptblock]$body) {
  Write-Output ('SECTION ' + $name)
  try { & $body | ConvertTo-Json -Depth 7 -Compress } catch { Write-Output ('ERROR ' + $_.Exception.Message) }
}
Section 'adapters' { Get-NetAdapter -IncludeHidden | Select-Object Name,InterfaceDescription,ifIndex,Status,MacAddress,LinkSpeed }
Section 'addresses' { Get-NetIPAddress | Select-Object InterfaceIndex,InterfaceAlias,IPAddress,AddressFamily,AddressState,PrefixLength }
Section 'routes' { Get-NetRoute | Select-Object InterfaceIndex,InterfaceAlias,DestinationPrefix,NextHop,RouteMetric,State }
Section 'interfaces' { Get-NetIPInterface | Select-Object InterfaceIndex,InterfaceAlias,AddressFamily,ConnectionState,InterfaceMetric,NlMtu }
Section 'neighbors' { Get-NetNeighbor | Select-Object InterfaceIndex,IPAddress,LinkLayerAddress,State }
Section 'dns-servers' { Get-DnsClientServerAddress | Select-Object InterfaceIndex,InterfaceAlias,AddressFamily,ServerAddresses }
Section 'tcp-connections' { Get-NetTCPConnection | Select-Object LocalAddress,LocalPort,RemoteAddress,RemotePort,State,OwningProcess }
Section 'internet-proxy' {
  $p = Get-ItemProperty 'HKCU:\Software\Microsoft\Windows\CurrentVersion\Internet Settings'
  [pscustomobject]@{ProxyEnable=$p.ProxyEnable;AutoDetect=$p.AutoDetect;ProxyServer=([string]$p.ProxyServer -replace '[^;=/\s]+:[^;@\s]+@','<redacted>@');PacConfigured=([bool]$p.AutoConfigURL)}
}
Section 'proxy-env-presence' { 'HTTP_PROXY','HTTPS_PROXY','ALL_PROXY','NO_PROXY' | ForEach-Object { [pscustomobject]@{Name=$_;Present=([bool][Environment]::GetEnvironmentVariable($_))} } }
Write-Output 'SECTION wlan'; netsh.exe wlan show interfaces
Write-Output 'SECTION native-ipconfig'; ipconfig.exe /all
Write-Output 'SECTION native-routes'; route.exe print
Section 'target-route' {
  foreach ($hostName in 'www.baidu.com','www.google.com') {
    foreach ($address in [System.Net.Dns]::GetHostAddresses($hostName)) {
      [pscustomobject]@{Target=$hostName;Address=$address.IPAddressToString;Route=(Find-NetRoute -RemoteIPAddress $address.IPAddressToString | Select-Object InterfaceIndex,InterfaceAlias,IPAddress,DestinationPrefix,NextHop,RouteMetric)}
    }
  }
}
`

// Some VPNs are local HTTP proxies rather than TUN adapters. Observe the
// Windows user's default proxy path separately from direct socket routing.
const proxyProbeScript = `
$OutputEncoding = [Console]::OutputEncoding = [System.Text.UTF8Encoding]::new($false)
foreach ($hostName in 'www.baidu.com','www.google.com') {
  $watch = [Diagnostics.Stopwatch]::StartNew()
  $response = $null
  $request = $null
  try {
    $uri = [Uri]('https://' + $hostName + '/')
    $proxy = [System.Net.WebRequest]::GetSystemWebProxy()
    $destination = $proxy.GetProxy($uri)
    [pscustomobject]@{Phase='begin';Target=$hostName;ProxyHost=$destination.Host;ProxyPort=$destination.Port;Bypassed=$proxy.IsBypassed($uri)} | ConvertTo-Json -Compress
    $request = [System.Net.HttpWebRequest]::Create($uri)
    $request.Proxy = $proxy
    $request.Timeout = 4000
    $request.ReadWriteTimeout = 3000
    $request.AllowAutoRedirect = $false
    $request.KeepAlive = $false
    $response = $request.GetResponse()
    [pscustomobject]@{Phase='end';Target=$hostName;Status=[int]$response.StatusCode;ElapsedMs=$watch.ElapsedMilliseconds} | ConvertTo-Json -Compress
  } catch {
    [pscustomobject]@{Phase='error';Target=$hostName;Type=$_.Exception.GetType().FullName;WebStatus=[string]$_.Exception.InnerException.Status;ElapsedMs=$watch.ElapsedMilliseconds} | ConvertTo-Json -Compress
  } finally {
    if ($null -ne $response) { $response.Close() }
    if ($null -ne $request) { $request.Abort() }
  }
}
`

func (d *Diagnostics) proxyProbes(ctx context.Context) {
	ticker := time.NewTicker(15 * time.Second)
	defer ticker.Stop()
	for {
		if ctx.Err() != nil {
			return
		}
		d.command(ctx, "windows-default-proxy-https", 12*time.Second, "powershell.exe", "-NoProfile", "-NonInteractive", "-Command", proxyProbeScript)
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

func (d *Diagnostics) snapshots(ctx context.Context) {
	ticker := time.NewTicker(15 * time.Second)
	defer ticker.Stop()
	reason := "startup"
	for {
		if ctx.Err() != nil {
			return
		}
		d.Log.Printf("snapshot begin reason=%q", reason)
		d.command(ctx, "windows-snapshot", 12*time.Second, "powershell.exe", "-NoProfile", "-NonInteractive", "-Command", snapshotScript)
		select {
		case <-ctx.Done():
			return
		case reason = <-d.wake:
			// Merge already queued triggers into this snapshot, without losing the reasons.
			for len(d.wake) > 0 {
				reason += ";" + <-d.wake
			}
		case <-ticker.C:
			reason = "periodic"
		}
	}
}

func (d *Diagnostics) events(ctx context.Context) {
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()
	// Each session owns an append-only export. Duplicates across sessions are
	// intentional; RecordId makes them identifiable. No event logs are cleared.
	last := time.Now().Add(-2 * time.Minute)
	for {
		if ctx.Err() != nil {
			return
		}
		until := time.Now()
		script := fmt.Sprintf(`
$OutputEncoding = [Console]::OutputEncoding = [System.Text.UTF8Encoding]::new($false)
$start = [DateTimeOffset]::FromUnixTimeMilliseconds(%d).LocalDateTime
$end = [DateTimeOffset]::FromUnixTimeMilliseconds(%d).LocalDateTime
foreach ($name in 'Microsoft-Windows-WLAN-AutoConfig/Operational','System','Microsoft-Windows-NetworkProfile/Operational','Microsoft-Windows-RasClient/Operational') {
  Write-Output ('EVENT CHANNEL ' + $name)
  try {
    Get-WinEvent -ListLog $name -ErrorAction Stop | Select-Object LogName,IsEnabled,RecordCount,LogMode,FileSize,MaximumSizeInBytes | ConvertTo-Json -Compress
    Get-WinEvent -FilterHashtable @{LogName=$name;StartTime=$start;EndTime=$end} -ErrorAction Stop |
      Where-Object { $name -ne 'System' -or $_.ProviderName -match 'Netwtw|Tcpip|NDIS|Dhcp|DNS|Ras|Kernel-Power|Power-Troubleshooter|NlaSvc' } |
      Select-Object TimeCreated,RecordId,Id,ProviderName,LevelDisplayName,Message,@{Name='Xml';Expression={$_.ToXml()}} | ConvertTo-Json -Depth 5 -Compress
  } catch { Write-Output ('EVENT READ: ' + $_.Exception.Message) }
}
`, last.UnixMilli(), until.UnixMilli())
		err := d.command(ctx, "windows-events", 20*time.Second, "powershell.exe", "-NoProfile", "-NonInteractive", "-Command", script)
		// Overlap protects events committed slightly late; preserve RecordId for deduplication.
		if err == nil {
			last = until.Add(-30 * time.Second)
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}
