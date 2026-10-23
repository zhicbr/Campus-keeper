//go:build windows

package ui

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
	"time"
	"unsafe"

	webview2 "github.com/jchv/go-webview2"
	"github.com/jchv/go-webview2/webviewloader"

	"wenchang-campus-keeper/internal/config"
	"wenchang-campus-keeper/internal/diagnostics"
)

const (
	trayMessage   = 0x0401 // WM_USER + 1
	statusMessage = 0x0402 // WM_USER + 2
	WakeMessage   = 0x0404 // WM_USER + 4

	nifMessageIconTip = 0x0007
	nimAdd            = 0x00000000
	nimModify         = 0x00000001
	nimDelete         = 0x00000002

	wmClose         = 0x0010
	wmDestroy       = 0x0002
	wmRButtonDown   = 0x0204
	wmRButtonUp     = 0x0205
	wmLButtonDblClk = 0x0203
	wmContextMenu   = 0x007B
	wmSetIcon       = 0x0080
	iconSmall       = 0
	iconBig         = 1

	mfString    = 0x00000000
	mfGrayed    = 0x00000001
	mfDisabled  = 0x00000002
	mfSeparator = 0x00000800

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
	setWindowPos          = user32.NewProc("SetWindowPos")
	setWindowLongPtr      = user32.NewProc("SetWindowLongPtrW")
	setWindowLong         = user32.NewProc("SetWindowLongW")
	callWindowProc        = user32.NewProc("CallWindowProcW")
	dwmSetWindowAttribute = dwmapi.NewProc("DwmSetWindowAttribute")
	getMessage            = user32.NewProc("GetMessageW")
	translateMessage      = user32.NewProc("TranslateMessage")
	dispatchMessage       = user32.NewProc("DispatchMessageW")
	postMessage           = user32.NewProc("PostMessageW")
	postQuitMessage       = user32.NewProc("PostQuitMessage")
	sendMessage           = user32.NewProc("SendMessageW")
	loadImage             = user32.NewProc("LoadImageW")
	getSystemMetrics      = user32.NewProc("GetSystemMetrics")
)

type winPoint struct{ x, y int32 }

type winMessage struct {
	hwnd     uintptr
	message  uint32
	_        uint32
	wparam   uintptr
	lparam   uintptr
	time     uint32
	point    winPoint
	lprivate uint32
}

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

type DashboardInfo struct {
	Status   string
	Network  string
	Operator string
	IP       string
	IsCampus bool
	State    string
}

type TrayUI struct {
	mu            sync.Mutex
	wvOnce        sync.Once
	wvReady       chan struct{}
	hwndTray      uintptr
	hwndWebview   uintptr
	oldWndProc    uintptr
	wv            webview2.WebView
	windowVisible bool
	uiRendered    bool
	pendingTab    string
	dash          DashboardInfo
	logText       string
	currentIcon   uintptr

	quit         func()
	onReconnect  func()
	getConfig    func() config.Config
	onSaveConfig func(config.Config)
	done         chan struct{}
}

func (t *TrayUI) setWindowIcons(hwnd uintptr) {
	if hwnd == 0 {
		return
	}
	instance, _, _ := getModuleHandle.Call(0)
	if loadImage.Find() == nil && getSystemMetrics.Find() == nil {
		icow, _, _ := getSystemMetrics.Call(11) // SM_CXICON
		icoh, _, _ := getSystemMetrics.Call(12) // SM_CYICON
		hIconBig, _, _ := loadImage.Call(instance, 1, 1 /* IMAGE_ICON */, icow, icoh, 0)
		if hIconBig != 0 {
			sendMessage.Call(hwnd, wmSetIcon, iconBig, hIconBig)
		}

		smw, _, _ := getSystemMetrics.Call(49) // SM_CXSMICON
		smh, _, _ := getSystemMetrics.Call(50) // SM_CYSMICON
		hIconSm, _, _ := loadImage.Call(instance, 1, 1 /* IMAGE_ICON */, smw, smh, 0)
		if hIconSm != 0 {
			sendMessage.Call(hwnd, wmSetIcon, iconSmall, hIconSm)
		}
	}
	if t.currentIcon != 0 {
		sendMessage.Call(hwnd, wmSetIcon, iconSmall, t.currentIcon)
	}
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

func StartTray(quit func(), onReconnect func(), getConfig func() config.Config, onSaveConfig func(config.Config), showConsole bool) (*TrayUI, error) {
	t := &TrayUI{
		dash: DashboardInfo{
			Status:  "检测中",
			Network: "检测中",
		},
		quit:         quit,
		onReconnect:  onReconnect,
		getConfig:    getConfig,
		onSaveConfig: onSaveConfig,
		done:         make(chan struct{}),
	}

	initDpiAwareness()

	trayReady := make(chan error, 1)
	go t.trayLoop(trayReady)
	if err := <-trayReady; err != nil {
		return nil, err
	}

	if showConsole {
		go t.ensureWebview("logs")
	}

	diagnostics.SetConsoleWriter(t)
	return t, nil
}

func (t *TrayUI) trayLoop(ready chan<- error) {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()

	// Attach this GUI thread to the interactive user desktop if running from background session
	openDesktop := user32.NewProc("OpenDesktopW")
	setThreadDesktop := user32.NewProc("SetThreadDesktop")
	closeDesktop := user32.NewProc("CloseDesktop")
	if hDesk, _, _ := openDesktop.Call(uintptr(unsafe.Pointer(syscall.StringToUTF16Ptr("Default"))), 0, 0, 0x10000000); hDesk != 0 {
		setThreadDesktop.Call(hDesk)
		closeDesktop.Call(hDesk)
	}

	instance, _, _ := getModuleHandle.Call(0)
	trayClassName, _ := syscall.UTF16PtrFromString("WenchangCampusKeeperTray")

	trayCallback := syscall.NewCallback(func(hwnd, message, wparam, lparam uintptr) uintptr {
		switch uint32(message) {
		case trayMessage:
			switch uint16(lparam) {
			case wmRButtonUp:
				t.popupMenu()
			case wmLButtonDblClk:
				t.toggleWindow()
			}
			return 0
		case statusMessage:
			t.updateIcon(nimModify)
			return 0
		case WakeMessage:
			if t.getConfig != nil && !t.getConfig().Valid() {
				t.openSettings()
			} else {
				t.showConsole()
			}
			return 0
		case wmClose:
			destroyWindow.Call(hwnd)
			return 0
		case wmDestroy:
			postQuitMessage.Call(0)
			return 0
		}
		res, _, _ := defWindowProc.Call(hwnd, message, wparam, lparam)
		return res
	})

	wc := windowClass{size: uint32(unsafe.Sizeof(windowClass{})), proc: trayCallback, instance: instance, className: trayClassName}
	registerClassEx.Call(uintptr(unsafe.Pointer(&wc)))

	hwndTray, _, _ := createWindowEx.Call(
		0,
		uintptr(unsafe.Pointer(trayClassName)),
		uintptr(unsafe.Pointer(trayClassName)),
		0x80000000, // WS_POPUP
		0, 0, 0, 0,
		0, 0, instance, 0,
	)
	if hwndTray == 0 {
		ready <- errors.New("无法创建系统托盘监听窗口")
		return
	}
	t.mu.Lock()
	t.hwndTray = hwndTray
	t.mu.Unlock()

	var lastErr error
	added := false
	for attempt := 0; attempt < 10; attempt++ {
		ok, err := t.updateIconWithErr(nimAdd)
		if ok {
			added = true
			break
		}
		lastErr = err
		t.updateIcon(nimDelete)
		time.Sleep(200 * time.Millisecond)
	}
	if !added {
		ready <- fmt.Errorf("无法添加系统托盘图标 (err=%v, hwnd=%x)", lastErr, hwndTray)
		destroyWindow.Call(hwndTray)
		return
	}

	ready <- nil

	// Pure Win32 message loop - completely independent from WebView2
	var msg winMessage
	for {
		ret, _, _ := getMessage.Call(uintptr(unsafe.Pointer(&msg)), 0, 0, 0)
		if ret == 0 || ret == ^uintptr(0) {
			break
		}
		translateMessage.Call(uintptr(unsafe.Pointer(&msg)))
		dispatchMessage.Call(uintptr(unsafe.Pointer(&msg)))
	}

	t.updateIcon(nimDelete)
	destroyWindow.Call(hwndTray)
	t.mu.Lock()
	icon := t.currentIcon
	t.mu.Unlock()
	if icon != 0 {
		destroyIcon.Call(icon)
	}
	close(t.done)
}

func (t *TrayUI) webviewLoop(ready chan struct{}) {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()

	if _, err := webviewloader.GetInstalledVersion(); err != nil {
		close(ready)
		return
	}

	wvDataDir := filepath.Join(config.AppDataDir(), "wv2")
	_ = os.MkdirAll(wvDataDir, 0755)

	wv := webview2.NewWithOptions(webview2.WebViewOptions{
		Debug:    false,
		DataPath: wvDataDir,
		WindowOptions: webview2.WindowOptions{
			Title:  "CampusKeeper v" + config.AppVersion,
			Width:  680,
			Height: 560,
			Center: true,
			IconId: 1,
		},
	})
	if wv == nil {
		close(ready)
		return
	}

	hwndWebview := uintptr(wv.Window())
	// Hide immediately to keep background clean
	showWindow.Call(hwndWebview, 0) // SW_HIDE

	t.mu.Lock()
	t.wv = wv
	t.hwndWebview = hwndWebview
	t.mu.Unlock()

	t.setWindowIcons(hwndWebview)
	applyModernWindowAttributes(hwndWebview)
	wv.SetSize(680, 560, webview2.HintNone)
	wv.SetSize(520, 420, webview2.HintMin)

	// Intercept WM_CLOSE so clicking 'X' hides the window instead of destroying WebView2
	var oldWndProc uintptr
	subclassCallback := syscall.NewCallback(func(hwnd, msg, wp, lp uintptr) uintptr {
		if uint32(msg) == wmClose {
			t.hideWindow()
			return 0
		}
		res, _, _ := callWindowProc.Call(oldWndProc, hwnd, msg, wp, lp)
		return res
	})
	oldWndProc = setWndProc(hwndWebview, subclassCallback)
	t.mu.Lock()
	t.oldWndProc = oldWndProc
	t.mu.Unlock()

	// Bind Go RPC methods
	wv.Bind("saveConfig", func(account, password, opStr string) (bool, error) {
		op := config.OperatorCMCC
		switch opStr {
		case "移动", "中国移动":
			op = config.OperatorCMCC
		case "电信", "中国电信":
			op = config.OperatorTelecom
		case "联通", "中国联通":
			op = config.OperatorUnicom
		case "校园网":
			op = config.OperatorCampus
		}
		if t.onSaveConfig != nil {
			go t.onSaveConfig(config.Config{
				Account:  account,
				Password: password,
				Operator: op,
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
		dir := config.AppDataDir()
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
			res.Account = cfg.Account
			res.Password = cfg.Password
			res.Operator = cfg.OperatorName()
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
			Status:   t.dash.Status,
			Network:  t.dash.Network,
			Operator: t.dash.Operator,
			IP:       t.dash.IP,
			IsCampus: t.dash.IsCampus,
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

	wv.Bind("onUIRendered", func() (bool, error) {
		t.mu.Lock()
		t.uiRendered = true
		visible := t.windowVisible
		hwnd := t.hwndWebview
		tab := t.pendingTab
		t.mu.Unlock()

		if tab != "" {
			wv.Dispatch(func() {
				wv.Eval(fmt.Sprintf("if (window.switchTab) window.switchTab('%s');", tab))
			})
		}
		if visible && hwnd != 0 {
			showWindow.Call(hwnd, 5) // SW_SHOW
			setForegroundWindow.Call(hwnd)
		}
		return true, nil
	})

	wv.SetHtml(GetAppHTML())
	close(ready)

	// WebView2 dedicated message loop
	wv.Run()

	t.mu.Lock()
	t.wv = nil
	t.hwndWebview = 0
	t.windowVisible = false
	t.uiRendered = false
	t.wvReady = nil
	t.wvOnce = sync.Once{}
	t.mu.Unlock()
}

func (t *TrayUI) ensureWebview(targetTab string) {
	t.wvOnce.Do(func() {
		t.wvReady = make(chan struct{})
		go t.webviewLoop(t.wvReady)
	})
	if t.wvReady != nil {
		<-t.wvReady
	}
	t.showWindowWithTab(targetTab)
}

func (t *TrayUI) showWindowWithTab(tab string) {
	t.mu.Lock()
	wv := t.wv
	hwnd := t.hwndWebview
	rendered := t.uiRendered
	t.windowVisible = true
	t.pendingTab = tab
	t.mu.Unlock()

	if wv == nil || hwnd == 0 {
		return
	}
	wv.Dispatch(func() {
		wv.Eval(fmt.Sprintf("if (window.switchTab) window.switchTab('%s'); if (window.refreshAll) window.refreshAll();", tab))
		if rendered {
			showWindow.Call(hwnd, 5) // SW_SHOW
			setForegroundWindow.Call(hwnd)
		}
	})
}

func (t *TrayUI) hideWindow() {
	t.mu.Lock()
	hwnd := t.hwndWebview
	t.windowVisible = false
	t.mu.Unlock()
	if hwnd != 0 {
		showWindow.Call(hwnd, 0) // SW_HIDE
	}
}

func (t *TrayUI) toggleWindow() {
	t.mu.Lock()
	wv := t.wv
	hwnd := t.hwndWebview
	visible := t.windowVisible
	t.mu.Unlock()

	if wv == nil || hwnd == 0 {
		go t.ensureWebview("settings")
		return
	}
	if visible {
		t.hideWindow()
	} else {
		t.showWindowWithTab("settings")
	}
}

func (t *TrayUI) openSettings() {
	t.mu.Lock()
	wv := t.wv
	t.mu.Unlock()
	if wv == nil {
		go t.ensureWebview("settings")
	} else {
		t.showWindowWithTab("settings")
	}
}

func (t *TrayUI) showConsole() {
	t.mu.Lock()
	wv := t.wv
	t.mu.Unlock()
	if wv == nil {
		go t.ensureWebview("logs")
	} else {
		t.showWindowWithTab("logs")
	}
}

func (t *TrayUI) UpdateDashboard(status, network, operator, ip string, isCampus bool, state string) {
	t.mu.Lock()
	t.dash = DashboardInfo{
		Status:   status,
		Network:  network,
		Operator: operator,
		IP:       ip,
		IsCampus: isCampus,
		State:    state,
	}
	wv := t.wv
	hwndTray := t.hwndTray
	t.mu.Unlock()

	if hwndTray != 0 {
		postMessage.Call(hwndTray, statusMessage, 0, 0)
	}

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

func (t *TrayUI) updateIcon(action uint32) bool {
	ok, _ := t.updateIconWithErr(action)
	return ok
}

func (t *TrayUI) updateIconWithErr(action uint32) (bool, error) {
	t.mu.Lock()
	hwnd := t.hwndTray
	dash := t.dash
	oldIcon := t.currentIcon
	t.mu.Unlock()

	if hwnd == 0 {
		return false, errors.New("hwnd is 0")
	}

	kind := iconKindForState(dash.State, dash.IsCampus)

	newIcon := CreateShieldIcon(kind)
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

	copy(data.tip[:], syscall.StringToUTF16("CampusKeeper · "+dash.Status))

	res, _, callErr := shellNotifyIcon.Call(uintptr(action), uintptr(unsafe.Pointer(&data)))
	if oldIcon != 0 {
		destroyIcon.Call(oldIcon)
	}
	t.mu.Lock()
	hwndWebview := t.hwndWebview
	t.mu.Unlock()
	if hwndWebview != 0 && newIcon != 0 {
		postMessage.Call(hwndWebview, wmSetIcon, iconSmall, newIcon)
	}
	if res == 0 {
		return false, callErr
	}
	return true, nil
}

func (t *TrayUI) popupMenu() {
	menu, _, _ := createPopupMenu.Call()
	if menu == 0 {
		return
	}
	defer destroyMenu.Call(menu)

	t.mu.Lock()
	dash := t.dash
	hwnd := t.hwndTray
	t.mu.Unlock()

	if hwnd == 0 {
		return
	}

	// 1. Dashboard Status (High contrast, clickable)
	appendMenu.Call(menu, mfString, menuCmdSettings, uintptr(unsafe.Pointer(syscall.StringToUTF16Ptr("状态："+dash.Status))))
	appendMenu.Call(menu, mfString, menuCmdSettings, uintptr(unsafe.Pointer(syscall.StringToUTF16Ptr("网络："+dash.Network))))
	if dash.IsCampus && dash.Operator != "" {
		appendMenu.Call(menu, mfString, menuCmdSettings, uintptr(unsafe.Pointer(syscall.StringToUTF16Ptr("运营商："+dash.Operator))))
	}
	if dash.IP != "" {
		appendMenu.Call(menu, mfString, menuCmdSettings, uintptr(unsafe.Pointer(syscall.StringToUTF16Ptr("地址："+dash.IP))))
	}
	appendMenu.Call(menu, mfSeparator, 0, 0)

	// 3. Actions
	appendMenu.Call(menu, mfString, menuCmdReconnect, uintptr(unsafe.Pointer(syscall.StringToUTF16Ptr("重新连接"))))
	appendMenu.Call(menu, mfString, menuCmdSettings, uintptr(unsafe.Pointer(syscall.StringToUTF16Ptr("账号设置..."))))
	appendMenu.Call(menu, mfString, menuCmdLogs, uintptr(unsafe.Pointer(syscall.StringToUTF16Ptr("运行日志..."))))
	appendMenu.Call(menu, mfString, menuCmdOpenDir, uintptr(unsafe.Pointer(syscall.StringToUTF16Ptr("数据目录"))))
	appendMenu.Call(menu, mfSeparator, 0, 0)
	appendMenu.Call(menu, mfString, menuCmdExit, uintptr(unsafe.Pointer(syscall.StringToUTF16Ptr("退出"))))

	var point winPoint
	getCursorPos.Call(uintptr(unsafe.Pointer(&point)))
	setForegroundWindow.Call(hwnd)
	selected, _, _ := trackPopupMenu.Call(menu, 0x100|0x2, uintptr(point.x), uintptr(point.y), 0, hwnd, 0)
	postMessage.Call(hwnd, 0, 0, 0) // WM_NULL (MSDN Q139526 fix)

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
		dir := config.AppDataDir()
		shellExecute.Call(0, uintptr(unsafe.Pointer(syscall.StringToUTF16Ptr("open"))), uintptr(unsafe.Pointer(syscall.StringToUTF16Ptr(dir))), 0, 0, 1)
	case menuCmdExit:
		t.mu.Lock()
		hwnd := t.hwndTray
		wv := t.wv
		hwndWebview := t.hwndWebview
		oldWndProc := t.oldWndProc
		quit := t.quit
		t.mu.Unlock()

		diagnostics.SetConsoleWriter(io.Discard)
		if hwnd != 0 {
			var data notifyIconData
			data.size = uint32(unsafe.Sizeof(data))
			data.hwnd = hwnd
			data.id = 1
			shellNotifyIcon.Call(uintptr(nimDelete), uintptr(unsafe.Pointer(&data)))
		}
		if hwndWebview != 0 && oldWndProc != 0 {
			setWndProc(hwndWebview, oldWndProc)
		}
		if wv != nil {
			wv.Dispatch(func() {
				wv.Destroy()
			})
		}
		if quit != nil {
			quit()
		}
		os.Exit(0)
	}
}

func (t *TrayUI) Write(p []byte) (int, error) {
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

func (t *TrayUI) Close() {
	diagnostics.SetConsoleWriter(io.Discard)
	t.mu.Lock()
	hwndTray := t.hwndTray
	wv := t.wv
	hwndWebview := t.hwndWebview
	oldWndProc := t.oldWndProc
	t.mu.Unlock()

	if hwndWebview != 0 && oldWndProc != 0 {
		setWndProc(hwndWebview, oldWndProc)
	}
	if wv != nil {
		wv.Dispatch(func() {
			wv.Destroy()
		})
	}
	if hwndTray != 0 {
		var data notifyIconData
		data.size = uint32(unsafe.Sizeof(data))
		data.hwnd = hwndTray
		data.id = 1
		shellNotifyIcon.Call(uintptr(nimDelete), uintptr(unsafe.Pointer(&data)))
		postMessage.Call(hwndTray, wmClose, 0, 0)
	}
}
