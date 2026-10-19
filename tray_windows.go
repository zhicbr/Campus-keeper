//go:build windows

package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"syscall"
	"unsafe"

	webview2 "github.com/jchv/go-webview2"
	"github.com/jchv/go-webview2/webviewloader"
)

const (
	trayMessage       = 0x0401 // WM_USER + 1
	nifMessageIconTip = 0x0007
	nimAdd            = 0x00000000
	nimModify         = 0x00000001
	nimDelete         = 0x00000002

	wmClose         = 0x0010
	wmDestroy       = 0x0002
	wmRButtonUp     = 0x0205
	wmLButtonDblClk = 0x0203

	mfString    = 0x00000000
	mfGrayed    = 0x00000001
	mfSeparator = 0x00000800

	wakeMessage      = 0x0404

	menuCmdHeader    = 2001
	menuCmdReconnect = 2002
	menuCmdSettings  = 2003
	menuCmdLogs      = 2004
	menuCmdOpenDir   = 2005
	menuCmdExit      = 2006
)

var (
	user32                = syscall.NewLazyDLL("user32.dll")
	kernel32              = syscall.NewLazyDLL("kernel32.dll")
	shell32               = syscall.NewLazyDLL("shell32.dll")
	dwmapi                = syscall.NewLazyDLL("dwmapi.dll")
	registerClassEx       = user32.NewProc("RegisterClassExW")
	createWindowEx        = user32.NewProc("CreateWindowExW")
	defWindowProc         = user32.NewProc("DefWindowProcW")
	destroyWindow         = user32.NewProc("DestroyWindow")
	showWindow            = user32.NewProc("ShowWindow")
	createPopupMenu       = user32.NewProc("CreatePopupMenu")
	appendMenu            = user32.NewProc("AppendMenuW")
	trackPopupMenu        = user32.NewProc("TrackPopupMenu")
	destroyMenu           = user32.NewProc("DestroyMenu")
	getCursorPos          = user32.NewProc("GetCursorPos")
	setForegroundWindow   = user32.NewProc("SetForegroundWindow")
	shellNotifyIcon       = shell32.NewProc("Shell_NotifyIconW")
	getModuleHandle       = kernel32.NewProc("GetModuleHandleW")
	shellExecute          = shell32.NewProc("ShellExecuteW")
	openDesktop           = user32.NewProc("OpenDesktopW")
	setThreadDesktop      = user32.NewProc("SetThreadDesktop")
	setWindowLongPtr      = user32.NewProc("SetWindowLongPtrW")
	setWindowLong         = user32.NewProc("SetWindowLongW")
	callWindowProc        = user32.NewProc("CallWindowProcW")
	dwmSetWindowAttribute = dwmapi.NewProc("DwmSetWindowAttribute")
)

type winPoint struct{ x, y int32 }

type windowClass struct {
	size        uint32
	style       uint32
	proc        uintptr
	classExtra  int32
	windowExtra int32
	instance    uintptr
	icon        uintptr
	cursor      uintptr
	background  uintptr
	menuName    *uint16
	className   *uint16
	smallIcon   uintptr
}

type notifyIconData struct {
	size        uint32
	hwnd        uintptr
	id          uint32
	flags       uint32
	callback    uint32
	icon        uintptr
	tip         [128]uint16
	state       uint32
	stateMask   uint32
	info        [256]uint16
	version     uint32
	infoTitle   [64]uint16
	infoFlags   uint32
	guid        [16]byte
	balloonIcon uintptr
}

type dashboardInfo struct {
	status   string
	network  string
	operator string
	ip       string
	isCampus bool
}

type trayUI struct {
	mu            sync.Mutex
	hwndTray      uintptr
	hwndWebview   uintptr
	oldWndProc    uintptr
	wv            webview2.WebView
	windowVisible bool
	dash          dashboardInfo
	logText       string
	currentIcon   uintptr

	quit         func()
	onReconnect  func()
	getConfig    func() config
	onSaveConfig func(config)
	done         chan struct{}
}

func initDpiAwareness() {
	if proc := user32.NewProc("SetProcessDpiAwarenessContext"); proc.Find() == nil {
		// DPI_AWARENESS_CONTEXT_PER_MONITOR_AWARE_V2 = -4
		proc.Call(^uintptr(3))
		return
	}
	if shcore := syscall.NewLazyDLL("shcore.dll"); shcore.Load() == nil {
		if proc := shcore.NewProc("SetProcessDpiAwareness"); proc.Find() == nil {
			// PROCESS_PER_MONITOR_DPI_AWARE = 2
			proc.Call(2)
			return
		}
	}
	if proc := user32.NewProc("SetProcessDPIAware"); proc.Find() == nil {
		proc.Call()
	}
}

func setWndProc(hwnd uintptr, newProc uintptr) uintptr {
	if setWindowLongPtr.Find() == nil {
		res, _, _ := setWindowLongPtr.Call(hwnd, uintptr(^uintptr(3)), newProc) // GWLP_WNDPROC = -4
		return res
	}
	res, _, _ := setWindowLong.Call(hwnd, uintptr(^uintptr(3)), newProc)
	return res
}

func applyModernWindowAttributes(hwnd uintptr) {
	if dwmSetWindowAttribute.Find() == nil {
		roundCorner := int32(2) // DWMWCP_ROUND (Windows 11)
		dwmSetWindowAttribute.Call(hwnd, 33, uintptr(unsafe.Pointer(&roundCorner)), 4)
	}
}

func startTray(quit func(), onReconnect func(), getConfig func() config, onSaveConfig func(config), showConsole bool) (*trayUI, error) {
	t := &trayUI{
		dash: dashboardInfo{
			status:  "检测中",
			network: "检测中",
		},
		quit:         quit,
		onReconnect:  onReconnect,
		getConfig:    getConfig,
		onSaveConfig: onSaveConfig,
		done:         make(chan struct{}),
	}
	ready := make(chan error, 1)
	go t.loop(ready, showConsole)
	if err := <-ready; err != nil {
		return nil, err
	}
	return t, nil
}

func (t *trayUI) loop(ready chan<- error, initialShow bool) {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	defer close(t.done)

	initDpiAwareness()

	if hDesk, _, _ := openDesktop.Call(uintptr(unsafe.Pointer(syscall.StringToUTF16Ptr("Default"))), 0, 0, 0x10000000); hDesk != 0 {
		setThreadDesktop.Call(hDesk)
	}

	instance, _, _ := getModuleHandle.Call(0)
	trayClassName := syscall.StringToUTF16Ptr("WenchangCampusKeeperTray")

	trayCallback := syscall.NewCallback(func(hwnd uintptr, message uint32, wparam, lparam uintptr) uintptr {
		switch message {
		case trayMessage:
			switch lparam {
			case wmLButtonDblClk:
				t.toggleWindow()
			case wmRButtonUp:
				t.popupMenu()
			}
			return 0
		case wakeMessage:
			if t.getConfig != nil && !t.getConfig().valid() {
				t.openSettings()
			} else {
				t.showConsole()
			}
			return 0
		}
		res, _, _ := defWindowProc.Call(hwnd, uintptr(message), wparam, lparam)
		return res
	})

	wc := windowClass{size: uint32(unsafe.Sizeof(windowClass{})), proc: trayCallback, instance: instance, className: trayClassName}
	registerClassEx.Call(uintptr(unsafe.Pointer(&wc)))

	hwndTray, _, _ := createWindowEx.Call(0, uintptr(unsafe.Pointer(trayClassName)), 0, 0, 0, 0, 0, 0, 0, 0, instance, 0)
	if hwndTray == 0 {
		ready <- errors.New("无法创建系统托盘监听窗口")
		return
	}
	t.mu.Lock()
	t.hwndTray = hwndTray
	t.mu.Unlock()

	if !t.updateIcon(nimAdd) {
		ready <- errors.New("无法添加系统托盘图标")
		destroyWindow.Call(hwndTray)
		return
	}

	// Initialize WebView2
	if _, err := webviewloader.GetInstalledVersion(); err != nil {
		ready <- fmt.Errorf("系统中未找到 WebView2 运行时: %w", err)
		destroyWindow.Call(hwndTray)
		return
	}

	wvDataDir := filepath.Join(appDataDir(), "wv2")
	_ = os.MkdirAll(wvDataDir, 0755)

	wv := webview2.NewWithOptions(webview2.WebViewOptions{
		Debug:    false,
		DataPath: wvDataDir,
		WindowOptions: webview2.WindowOptions{
			Title:  "CampusKeeper",
			Width:  500,
			Height: 560,
			Center: true,
		},
	})
	if wv == nil {
		ready <- errors.New("无法创建现代卡片窗口")
		destroyWindow.Call(hwndTray)
		return
	}

	hwndWebview := uintptr(wv.Window())
	t.mu.Lock()
	t.wv = wv
	t.hwndWebview = hwndWebview
	t.mu.Unlock()

	// Subclass to intercept WM_CLOSE -> hide window instead of destroy
	var oldWndProc uintptr
	subclassCallback := syscall.NewCallback(func(hwnd uintptr, msg uint32, wp, lp uintptr) uintptr {
		if msg == wmClose {
			t.hideWindow()
			return 0
		}
		res, _, _ := callWindowProc.Call(oldWndProc, hwnd, uintptr(msg), wp, lp)
		return res
	})
	oldWndProc = setWndProc(hwndWebview, subclassCallback)
	t.oldWndProc = oldWndProc

	applyModernWindowAttributes(hwndWebview)
	wv.SetSize(460, 500, webview2.HintMin)

	// Bind Go RPC methods
	wv.Bind("saveConfig", func(account, password, opStr string) (bool, error) {
		op := operatorCMCC
		switch opStr {
		case "移动", "中国移动":
			op = operatorCMCC
		case "电信", "中国电信":
			op = operatorTelecom
		case "联通", "中国联通":
			op = operatorUnicom
		case "校园网":
			op = operatorCampus
		}
		if t.onSaveConfig != nil {
			go t.onSaveConfig(config{
				account:  account,
				password: password,
				operator: op,
			})
		}
		return true, nil
	})

	wv.Bind("reconnect", func() (bool, error) {
		if t.onReconnect != nil {
			go t.onReconnect()
		}
		return true, nil
	})

	wv.Bind("openDataDir", func() (bool, error) {
		dir := appDataDir()
		shellExecute.Call(0, uintptr(unsafe.Pointer(syscall.StringToUTF16Ptr("open"))), uintptr(unsafe.Pointer(syscall.StringToUTF16Ptr(dir))), 0, 0, 1)
		return true, nil
	})

	type jsConfig struct {
		Account  string `json:"account"`
		Password string `json:"password"`
		Operator string `json:"operator"`
	}
	wv.Bind("getConfig", func() (jsConfig, error) {
		var res jsConfig
		if t.getConfig != nil {
			cfg := t.getConfig()
			res.Account = cfg.account
			res.Password = cfg.password
			res.Operator = cfg.operatorName()
		}
		return res, nil
	})

	type jsDashboard struct {
		Status   string `json:"status"`
		Network  string `json:"network"`
		Operator string `json:"operator"`
		IP       string `json:"ip"`
		IsCampus bool   `json:"isCampus"`
	}
	wv.Bind("getDashboard", func() (jsDashboard, error) {
		t.mu.Lock()
		defer t.mu.Unlock()
		return jsDashboard{
			Status:   t.dash.status,
			Network:  t.dash.network,
			Operator: t.dash.operator,
			IP:       t.dash.ip,
			IsCampus: t.dash.isCampus,
		}, nil
	})

	wv.Bind("getLogs", func() (string, error) {
		t.mu.Lock()
		defer t.mu.Unlock()
		return t.logText, nil
	})

	wv.Bind("hideWindow", func() (bool, error) {
		t.hideWindow()
		return true, nil
	})

	wv.SetHtml(appHTML)

	if initialShow {
		t.windowVisible = true
		showWindow.Call(hwndWebview, 5) // SW_SHOW
		setForegroundWindow.Call(hwndWebview)
	} else {
		t.windowVisible = false
		showWindow.Call(hwndWebview, 0) // SW_HIDE
	}

	setConsoleWriter(t)
	ready <- nil

	// Run message loop
	wv.Run()

	// Clean up
	setConsoleWriter(io.Discard)
	t.updateIcon(nimDelete)
	if t.hwndTray != 0 {
		destroyWindow.Call(t.hwndTray)
	}
	t.mu.Lock()
	icon := t.currentIcon
	t.mu.Unlock()
	if icon != 0 {
		destroyIcon.Call(icon)
	}
}

func (t *trayUI) showWindowWithTab(tab string) {
	t.mu.Lock()
	wv := t.wv
	hwnd := t.hwndWebview
	t.mu.Unlock()
	if wv == nil || hwnd == 0 {
		return
	}
	wv.Dispatch(func() {
		wv.Eval(fmt.Sprintf("if (window.switchTab) window.switchTab('%s');", tab))
		showWindow.Call(hwnd, 5)
		setForegroundWindow.Call(hwnd)
		t.mu.Lock()
		t.windowVisible = true
		t.mu.Unlock()
	})
}

func (t *trayUI) hideWindow() {
	t.mu.Lock()
	wv := t.wv
	hwnd := t.hwndWebview
	t.mu.Unlock()
	if wv == nil || hwnd == 0 {
		return
	}
	wv.Dispatch(func() {
		showWindow.Call(hwnd, 0)
		t.mu.Lock()
		t.windowVisible = false
		t.mu.Unlock()
	})
}

func (t *trayUI) toggleWindow() {
	t.mu.Lock()
	visible := t.windowVisible
	t.mu.Unlock()
	if visible {
		t.hideWindow()
	} else {
		t.showWindowWithTab("settings")
	}
}

func (t *trayUI) openSettings() {
	t.showWindowWithTab("settings")
}

func (t *trayUI) showConsole() {
	t.showWindowWithTab("logs")
}

func (t *trayUI) updateDashboard(status, network, operator, ip string, isCampus bool) {
	t.mu.Lock()
	t.dash = dashboardInfo{
		status:   status,
		network:  network,
		operator: operator,
		ip:       ip,
		isCampus: isCampus,
	}
	wv := t.wv
	t.mu.Unlock()

	t.updateIcon(nimModify)

	if wv != nil {
		dashData, _ := json.Marshal(map[string]interface{}{
			"status":   status,
			"network":  network,
			"operator": operator,
			"ip":       ip,
			"isCampus": isCampus,
		})
		wv.Dispatch(func() {
			wv.Eval(fmt.Sprintf("if (window.onStatusUpdate) window.onStatusUpdate(%s);", string(dashData)))
		})
	}
}

func (t *trayUI) updateIcon(action uint32) bool {
	t.mu.Lock()
	hwnd := t.hwndTray
	dash := t.dash
	oldIcon := t.currentIcon
	t.mu.Unlock()

	if hwnd == 0 {
		return false
	}

	var kind iconKind
	if dash.isCampus {
		kind = iconCampusOnline
	} else if strings.Contains(dash.status, "正常") || strings.Contains(dash.status, "已连接") {
		kind = iconExternalOnline
	} else if strings.Contains(dash.status, "重试") || strings.Contains(dash.status, "认证中") || strings.Contains(dash.status, "正在") {
		kind = iconConnecting
	} else {
		kind = iconOffline
	}

	newIcon := createShieldIcon(kind)
	t.mu.Lock()
	t.currentIcon = newIcon
	t.mu.Unlock()

	data := notifyIconData{
		size:     uint32(unsafe.Sizeof(notifyIconData{})),
		hwnd:     hwnd,
		id:       1,
		flags:    nifMessageIconTip,
		callback: trayMessage,
		icon:     newIcon,
	}

	tip := "CampusKeeper"
	if dash.status != "" {
		tip += " - " + dash.status
	}
	utfTip := syscall.StringToUTF16(tip)
	copy(data.tip[:], utfTip)

	res, _, _ := shellNotifyIcon.Call(uintptr(action), uintptr(unsafe.Pointer(&data)))
	if oldIcon != 0 {
		destroyIcon.Call(oldIcon)
	}
	return res != 0
}

func (t *trayUI) popupMenu() {
	t.mu.Lock()
	hwnd := t.hwndTray
	dash := t.dash
	t.mu.Unlock()

	if hwnd == 0 {
		return
	}

	menu, _, _ := createPopupMenu.Call()
	if menu == 0 {
		return
	}
	defer destroyMenu.Call(menu)

	headerText := fmt.Sprintf("CampusKeeper: %s", dash.status)
	appendMenu.Call(menu, mfString|mfGrayed, menuCmdHeader, uintptr(unsafe.Pointer(syscall.StringToUTF16Ptr(headerText))))
	appendMenu.Call(menu, mfSeparator, 0, 0)
	appendMenu.Call(menu, mfString, menuCmdReconnect, uintptr(unsafe.Pointer(syscall.StringToUTF16Ptr("立即重连"))))
	appendMenu.Call(menu, mfString, menuCmdSettings, uintptr(unsafe.Pointer(syscall.StringToUTF16Ptr("设置"))))
	appendMenu.Call(menu, mfString, menuCmdLogs, uintptr(unsafe.Pointer(syscall.StringToUTF16Ptr("运行日志"))))
	appendMenu.Call(menu, mfString, menuCmdOpenDir, uintptr(unsafe.Pointer(syscall.StringToUTF16Ptr("数据目录"))))
	appendMenu.Call(menu, mfSeparator, 0, 0)
	appendMenu.Call(menu, mfString, menuCmdExit, uintptr(unsafe.Pointer(syscall.StringToUTF16Ptr("退出"))))

	var point winPoint
	getCursorPos.Call(uintptr(unsafe.Pointer(&point)))
	setForegroundWindow.Call(hwnd)
	selected, _, _ := trackPopupMenu.Call(menu, 0x100|0x2, uintptr(point.x), uintptr(point.y), 0, hwnd, 0)
	switch selected {
	case menuCmdReconnect:
		if t.onReconnect != nil {
			go t.onReconnect()
		}
	case menuCmdSettings:
		t.openSettings()
	case menuCmdLogs:
		t.showConsole()
	case menuCmdOpenDir:
		dir := appDataDir()
		shellExecute.Call(0, uintptr(unsafe.Pointer(syscall.StringToUTF16Ptr("open"))), uintptr(unsafe.Pointer(syscall.StringToUTF16Ptr(dir))), 0, 0, 1)
	case menuCmdExit:
		t.close()
		if t.quit != nil {
			t.quit()
		}
	}
}

func (t *trayUI) Write(p []byte) (int, error) {
	t.mu.Lock()
	chunk := string(p)
	t.logText += chunk
	if len(t.logText) > 48<<10 {
		t.logText = t.logText[len(t.logText)-(40<<10):]
		if i := strings.IndexByte(t.logText, '\n'); i >= 0 {
			t.logText = t.logText[i+1:]
		}
	}
	visible := t.windowVisible
	wv := t.wv
	t.mu.Unlock()

	if visible && wv != nil {
		jsonChunk, _ := json.Marshal(chunk)
		wv.Dispatch(func() {
			wv.Eval(fmt.Sprintf("if (window.onNewLog) window.onNewLog(%s);", string(jsonChunk)))
		})
	}
	return len(p), nil
}

func (t *trayUI) close() {
	setConsoleWriter(io.Discard)
	t.mu.Lock()
	wv := t.wv
	t.mu.Unlock()

	if wv != nil {
		wv.Dispatch(func() {
			wv.Terminate()
		})
	}
	<-t.done
}
