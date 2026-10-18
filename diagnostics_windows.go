//go:build windows

package main

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
type diagnostics struct {
	log      *log.Logger
	writer   *rotatingLog
	cancel   context.CancelFunc
	wg       sync.WaitGroup
	wake     chan string
	sequence atomic.Uint64
}

func startDiagnostics(parent context.Context, dir string, operational *log.Logger) (*diagnostics, error) {
	w, err := newRotatingLog(filepath.Join(dir, "network-diagnostic.log"), 32<<20)
	if err != nil {
		return nil, err
	}
	w.silent = true
	ctx, cancel := context.WithCancel(parent)
	d := &diagnostics{log: log.New(w, "", log.Ldate|log.Ltime|log.Lmicroseconds), writer: w, cancel: cancel, wake: make(chan string, 32)}
	d.log.Printf("SESSION START pid=%d go=%s build=%s timezone=%s interval: probes=5s snapshot=15s events=30s; normal recovery enabled", os.Getpid(), runtime.Version(), diagnosticBuild, time.Now().Format("-0700"))
	operational.Print("持续网络诊断已开启：network-diagnostic.log（32 MiB 分卷，历史日志保留）；正常使用及开关 VPN 即可")
	d.wg.Add(4)
	go func() { defer d.wg.Done(); d.probes(ctx) }()
	go func() { defer d.wg.Done(); d.snapshots(ctx) }()
	go func() { defer d.wg.Done(); d.events(ctx) }()
	go func() { defer d.wg.Done(); d.proxyProbes(ctx) }()
	return d, nil
}

var diagnosticBuild = "development"

func (d *diagnostics) close() {
	d.cancel()
	d.wg.Wait()
	d.log.Print("SESSION END")
	d.writer.Close()
}

func (d *diagnostics) trigger(reason string) {
	d.log.Printf("snapshot requested reason=%q", reason)
	select {
	case d.wake <- reason:
	default:
		d.log.Printf("snapshot queue full reason=%q; periodic snapshots remain active", reason)
	}
}

// url.Error includes credential-bearing request URLs. Unwrap before logging;
// neither request bodies, query strings nor authentication responses are logged.
func diagnosticError(err error) string {
	if err == nil {
		return ""
	}
	var u *url.Error
	if errors.As(err, &u) {
		return diagnosticError(u.Err)
	}
	return fmt.Sprintf("%T: %s", err, err.Error())
}

func (d *diagnostics) request(client *http.Client, r *http.Request, label string) (*http.Response, error) {
	id := d.sequence.Add(1)
	started := time.Now()
	target := safePortal(r.URL.String())
	d.log.Printf("http begin id=%d kind=%q target=%q", id, label, target)
	trace := &httptrace.ClientTrace{
		DNSStart: func(i httptrace.DNSStartInfo) { d.log.Printf("http dns-start id=%d host=%q", id, i.Host) },
		DNSDone: func(i httptrace.DNSDoneInfo) {
			d.log.Printf("http dns-done id=%d addresses=%v coalesced=%t error=%q elapsed=%s", id, i.Addrs, i.Coalesced, diagnosticError(i.Err), time.Since(started))
		},
		ConnectStart: func(network, address string) {
			d.log.Printf("http connect-start id=%d network=%q address=%q", id, network, address)
		},
		ConnectDone: func(network, address string, err error) {
			d.log.Printf("http connect-done id=%d network=%q address=%q error=%q elapsed=%s", id, network, address, diagnosticError(err), time.Since(started))
		},
		GotConn: func(i httptrace.GotConnInfo) {
			d.log.Printf("http connected id=%d local=%q remote=%q reused=%t", id, i.Conn.LocalAddr().String(), i.Conn.RemoteAddr().String(), i.Reused)
		},
		TLSHandshakeStart: func() { d.log.Printf("http tls-start id=%d", id) },
		TLSHandshakeDone: func(state tls.ConnectionState, err error) {
			d.log.Printf("http tls-done id=%d version=%d error=%q elapsed=%s", id, state.Version, diagnosticError(err), time.Since(started))
		},
		WroteRequest: func(i httptrace.WroteRequestInfo) {
			d.log.Printf("http request-written id=%d error=%q elapsed=%s", id, diagnosticError(i.Err), time.Since(started))
		},
		GotFirstResponseByte: func() { d.log.Printf("http first-byte id=%d elapsed=%s", id, time.Since(started)) },
	}
	r = r.WithContext(httptrace.WithClientTrace(r.Context(), trace))
	resp, err := client.Do(r)
	status, redirect := 0, ""
	if resp != nil {
		status = resp.StatusCode
		if location, e := resp.Location(); e == nil {
			redirect = safePortal(location.String())
		}
	}
	d.log.Printf("http end id=%d kind=%q target=%q status=%d redirect=%q error=%q elapsed=%s", id, label, target, status, redirect, diagnosticError(err), time.Since(started))
	return resp, err
}

func (p *portalClient) doDiagnostic(r *http.Request, label string) (*http.Response, error) {
	if p.diagnostic == nil {
		return p.client.Do(r)
	}
	return p.diagnostic.request(p.client, r, label)
}

func (d *diagnostics) probes(ctx context.Context) {
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
		link := readLink(linkState{})
		d.log.Printf("independent link sample link=%+v", link)
		if link.ready() {
			gateway, mac, err := gatewayFingerprint(link.index)
			d.log.Printf("gateway sample ifindex=%d gateway=%q mac=%q error=%q", link.index, gateway, mac, diagnosticError(err))
			if gateway != "" {
				jobs.Add(1)
				go func() {
					defer jobs.Done()
					d.command(ctx, "campus-gateway-ping", 4*time.Second, "ping.exe", "-4", "-S", link.ipv4, "-n", "1", "-w", "1500", gateway)
				}()
			}
		}
		for _, host := range []string{"www.baidu.com", "www.google.com"} {
			jobs.Add(1)
			go func(host string) {
				defer jobs.Done()
				r, _ := http.NewRequestWithContext(ctx, http.MethodGet, "https://"+host+"/", nil)
				resp, err := d.request(client, r, "system-route-https")
				if resp != nil {
					_, bodyErr := io.Copy(io.Discard, io.LimitReader(resp.Body, 4096))
					resp.Body.Close()
					d.log.Printf("system https body host=%q error=%q", host, diagnosticError(bodyErr))
				}
				if err != nil && ctx.Err() == nil {
					d.log.Printf("system target failed host=%q (not a login trigger)", host)
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
func (d *diagnostics) command(ctx context.Context, label string, timeout time.Duration, name string, args ...string) error {
	child, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	start := time.Now()
	cmd := exec.CommandContext(child, name, args...)
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
	out, err := cmd.CombinedOutput()
	d.log.Printf("command kind=%q elapsed=%s error=%q context=%q bytes=%d output=%q", label, time.Since(start), diagnosticError(err), diagnosticError(child.Err()), len(out), string(out))
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

func (d *diagnostics) proxyProbes(ctx context.Context) {
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

func (d *diagnostics) snapshots(ctx context.Context) {
	ticker := time.NewTicker(15 * time.Second)
	defer ticker.Stop()
	reason := "startup"
	for {
		if ctx.Err() != nil {
			return
		}
		d.log.Printf("snapshot begin reason=%q", reason)
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

func (d *diagnostics) events(ctx context.Context) {
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
