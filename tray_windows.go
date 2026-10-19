//go:build windows

package main

import (
	"errors"
	"io"
	"runtime"
	"strings"
	"sync"
	"syscall"
	"time"
	"unsafe"
)

const (
	trayMessage       = 0x8001
	statusMessage     = 0x8002
	logMessage        = 0x8003
	wakeMessage       = 0x8004
	settingsMessage   = 0x8005
	wmClose           = 0x0010
	wmDestroy         = 0x0002
	wmCommand         = 0x0111
	wmSize            = 0x0005
	wmRButtonUp       = 0x0205
	wmLButtonDblClk   = 0x0203
	wmSetFont         = 0x0030
	emSetSel          = 0x00B1
	emLineScroll      = 0x00B6
	emScrollCaret     = 0x00B7
	emGetFirstLine    = 0x00CE
	nimAdd            = 0
	nimModify         = 1
	nimDelete         = 2
	nifMessageIconTip = 0x7

	// Menu flags
	mfString    = 0x0000
	mfGrayed    = 0x0001
	mfDisabled  = 0x0002
	mfSeparator = 0x0800

	// Menu command IDs
	menuCmdReconnect = 101
	menuCmdSettings  = 102
	menuCmdLogs      = 103
	menuCmdOpenDir   = 104
	menuCmdExit      = 105

	// Settings dialog command IDs
	cmdSave   = 1001
	cmdCancel = 1002

	cbsDropdownList = 0x0003
	cbAddString     = 0x0143
	cbSetCurSel     = 0x014E
	cbGetCurSel     = 0x0147
	esPassword      = 0x0020
	esAutohscroll   = 0x0080
	bsDefPushButton = 0x0001
	wsChild         = 0x40000000
	wsVisible       = 0x10000000
	wsTabstop       = 0x00010000
	wsBorder        = 0x00800000
)

var (
	user32              = syscall.NewLazyDLL("user32.dll")
	kernel32            = syscall.NewLazyDLL("kernel32.dll")
	shell32             = syscall.NewLazyDLL("shell32.dll")
	registerClassEx     = user32.NewProc("RegisterClassExW")
	createWindowEx      = user32.NewProc("CreateWindowExW")
	defWindowProc       = user32.NewProc("DefWindowProcW")
	getMessage          = user32.NewProc("GetMessageW")
	translateMessage    = user32.NewProc("TranslateMessage")
	dispatchMessage     = user32.NewProc("DispatchMessageW")
	postMessage         = user32.NewProc("PostMessageW")
	postQuitMessage     = user32.NewProc("PostQuitMessage")
	destroyWindow       = user32.NewProc("DestroyWindow")
	showWindow          = user32.NewProc("ShowWindow")
	moveWindow          = user32.NewProc("MoveWindow")
	setWindowText       = user32.NewProc("SetWindowTextW")
	sendMessage         = user32.NewProc("SendMessageW")
	getScrollInfo       = user32.NewProc("GetScrollInfo")
	createPopupMenu     = user32.NewProc("CreatePopupMenu")
	appendMenu          = user32.NewProc("AppendMenuW")
	trackPopupMenu      = user32.NewProc("TrackPopupMenu")
	destroyMenu         = user32.NewProc("DestroyMenu")
	getCursorPos        = user32.NewProc("GetCursorPos")
	setForegroundWindow = user32.NewProc("SetForegroundWindow")
	shellNotifyIcon     = shell32.NewProc("Shell_NotifyIconW")
	getModuleHandle     = kernel32.NewProc("GetModuleHandleW")
	getSystemMetrics    = user32.NewProc("GetSystemMetrics")
	createFont          = gdi32.NewProc("CreateFontW")
	getWindowText       = user32.NewProc("GetWindowTextW")
	getWindowTextLength = user32.NewProc("GetWindowTextLengthW")
	messageBox          = user32.NewProc("MessageBoxW")
	shellExecute        = shell32.NewProc("ShellExecuteW")
	openDesktop         = user32.NewProc("OpenDesktopW")
	setThreadDesktop    = user32.NewProc("SetThreadDesktop")
	isDialogMessage     = user32.NewProc("IsDialogMessageW")
)

type winPoint struct{ x, y int32 }
type scrollInfo struct {
	size     uint32
	mask     uint32
	minimum  int32
	maximum  int32
	page     uint32
	position int32
	track    int32
}

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

type dashboardInfo struct {
	status   string
	network  string
	operator string
	ip       string
	isCampus bool
}

type trayUI struct {
	mu             sync.Mutex
	hwnd           uintptr
	logWindow      uintptr
	logEdit        uintptr
	settingsWindow uintptr
	accountInput   uintptr
	passwordInput  uintptr
	operatorCombo  uintptr
	logText        string
	dash           dashboardInfo
	consoleVisible bool
	currentIcon    uintptr
	fontUI         uintptr
	fontLog        uintptr
	quit           func()
	onReconnect    func()
	getConfig      func() config
	onSaveConfig   func(config)
	done           chan struct{}
}

func createUIFont(name string, size int32) uintptr {
	fontName := syscall.StringToUTF16Ptr(name)
	hFont, _, _ := createFont.Call(
		uintptr(-size), 0, 0, 0, 400, 0, 0, 0, 1, 0, 0, 5, 0,
		uintptr(unsafe.Pointer(fontName)),
	)
	return hFont
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

func (t *trayUI) loop(ready chan<- error, initialConsole bool) {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	defer close(t.done)

	// Attach this GUI thread to the interactive user desktop if needed
	if hDesk, _, _ := openDesktop.Call(uintptr(unsafe.Pointer(syscall.StringToUTF16Ptr("Default"))), 0, 0, 0x10000000); hDesk != 0 {
		setThreadDesktop.Call(hDesk)
	}

	instance, _, _ := getModuleHandle.Call(0)
	className := syscall.StringToUTF16Ptr("WenchangCampusKeeperTray")

	t.fontUI = createUIFont("Microsoft YaHei UI", 12)
	t.fontLog = createUIFont("Consolas", 14)

	callback := syscall.NewCallback(func(hwnd uintptr, message uint32, wparam, lparam uintptr) uintptr {
		switch message {
		case trayMessage:
			switch lparam {
			case wmLButtonDblClk:
				t.toggleConsole()
			case wmRButtonUp:
				t.popupMenu()
			}
			return 0
		case statusMessage:
			t.updateIcon(nimModify)
			return 0
		case logMessage:
			t.updateLogs()
			return 0
		case wakeMessage:
			if t.getConfig != nil && !t.getConfig().valid() {
				t.doOpenSettings()
			} else {
				t.showConsole()
			}
			return 0
		case settingsMessage:
			t.doOpenSettings()
			return 0
		case wmSize:
			if hwnd == t.logWindow && t.logEdit != 0 {
				moveWindow.Call(t.logEdit, 0, 0, uintptr(uint16(lparam)), uintptr(uint16(lparam>>16)), 1)
			}
			return 0
		case wmClose:
			if hwnd == t.logWindow {
				showWindow.Call(hwnd, 0)
				t.mu.Lock()
				t.consoleVisible = false
				t.mu.Unlock()
				return 0
			}
			if hwnd == t.settingsWindow {
				showWindow.Call(hwnd, 0)
				return 0
			}
			if t.logWindow != 0 {
				destroyWindow.Call(t.logWindow)
			}
			if t.settingsWindow != 0 {
				destroyWindow.Call(t.settingsWindow)
			}
			destroyWindow.Call(hwnd)
			return 0
		case wmDestroy:
			if hwnd == t.hwnd {
				t.updateIcon(nimDelete)
				postQuitMessage.Call(0)
			}
			return 0
		}
		result, _, _ := defWindowProc.Call(hwnd, uintptr(message), wparam, lparam)
		return result
	})

	wc := windowClass{size: uint32(unsafe.Sizeof(windowClass{})), proc: callback, instance: instance, className: className}
	if atom, _, _ := registerClassEx.Call(uintptr(unsafe.Pointer(&wc))); atom == 0 {
		ready <- errors.New("无法注册托盘窗口")
		return
	}

	hwnd, _, _ := createWindowEx.Call(0, uintptr(unsafe.Pointer(className)), uintptr(unsafe.Pointer(className)), 0, 0, 0, 0, 0, 0, 0, instance, 0)
	if hwnd == 0 {
		ready <- errors.New("无法创建托盘窗口")
		return
	}
	t.mu.Lock()
	t.hwnd = hwnd
	t.mu.Unlock()

	if !t.updateIcon(nimAdd) {
		ready <- errors.New("无法添加系统托盘图标")
		destroyWindow.Call(hwnd)
		return
	}

	// 1. Log window (centered, modern)
	screenWidth, _, _ := getSystemMetrics.Call(0)
	screenHeight, _, _ := getSystemMetrics.Call(1)
	logW, logH := int32(860), int32(520)
	logX := (int32(screenWidth) - logW) / 2
	logY := (int32(screenHeight) - logH) / 2

	logTitle := syscall.StringToUTF16Ptr("CampusKeeper - 日志")
	logWindow, _, _ := createWindowEx.Call(0, uintptr(unsafe.Pointer(className)), uintptr(unsafe.Pointer(logTitle)), 0x00CF0000, uintptr(logX), uintptr(logY), uintptr(logW), uintptr(logH), 0, 0, instance, 0)
	if logWindow == 0 {
		ready <- errors.New("无法创建日志窗口")
		destroyWindow.Call(hwnd)
		return
	}
	t.logWindow = logWindow

	editClass := syscall.StringToUTF16Ptr("EDIT")
	edit, _, _ := createWindowEx.Call(0x200, uintptr(unsafe.Pointer(editClass)), 0, 0x50200844, 0, 0, uintptr(logW), uintptr(logH), logWindow, 0, instance, 0)
	if edit == 0 {
		ready <- errors.New("无法创建日志文本框")
		destroyWindow.Call(logWindow)
		destroyWindow.Call(hwnd)
		return
	}
	t.logEdit = edit
	if t.fontLog != 0 {
		sendMessage.Call(edit, wmSetFont, t.fontLog, 1)
	}

	// 2. Settings window (centered, modern)
	dlgW, dlgH := int32(380), int32(250)
	dlgX := (int32(screenWidth) - dlgW) / 2
	dlgY := (int32(screenHeight) - dlgH) / 2

	settingsClassName := syscall.StringToUTF16Ptr("CampusKeeperSettingsClass")
	settingsCallback := syscall.NewCallback(func(hwnd uintptr, message uint32, wparam, lparam uintptr) uintptr {
		switch message {
		case wmCommand:
			cmd := uint16(wparam)
			if cmd == cmdSave {
				account := getEditText(t.accountInput)
				password := getEditText(t.passwordInput)
				if account == "" || password == "" {
					msg := syscall.StringToUTF16Ptr("请填写完整的账号和密码")
					title := syscall.StringToUTF16Ptr("提示")
					messageBox.Call(hwnd, uintptr(unsafe.Pointer(msg)), uintptr(unsafe.Pointer(title)), 0x30)
					return 0
				}
				sel, _, _ := sendMessage.Call(t.operatorCombo, cbGetCurSel, 0, 0)
				op := operatorCMCC
				switch sel {
				case 0:
					op = operatorCMCC
				case 1:
					op = operatorTelecom
				case 2:
					op = operatorUnicom
				case 3:
					op = operatorCampus
				}
				if t.onSaveConfig != nil {
					go t.onSaveConfig(config{
						account:  account,
						password: password,
						operator: op,
					})
				}
				showWindow.Call(hwnd, 0)
				return 0
			}
			if cmd == cmdCancel {
				showWindow.Call(hwnd, 0)
				return 0
			}
		case wmClose:
			showWindow.Call(hwnd, 0)
			return 0
		}
		res, _, _ := defWindowProc.Call(hwnd, uintptr(message), wparam, lparam)
		return res
	})
	swc := windowClass{size: uint32(unsafe.Sizeof(windowClass{})), proc: settingsCallback, instance: instance, className: settingsClassName}
	registerClassEx.Call(uintptr(unsafe.Pointer(&swc)))

	dlgTitle := syscall.StringToUTF16Ptr("CampusKeeper - 账号设置")
	settingsWindow, _, _ := createWindowEx.Call(
		0x00010000, // WS_EX_CONTROLPARENT
		uintptr(unsafe.Pointer(settingsClassName)),
		uintptr(unsafe.Pointer(dlgTitle)),
		0x80C80000, // WS_POPUP | WS_CAPTION | WS_SYSMENU
		uintptr(dlgX), uintptr(dlgY), uintptr(dlgW), uintptr(dlgH),
		t.hwnd, 0, instance, 0,
	)
	t.settingsWindow = settingsWindow

	staticClass := syscall.StringToUTF16Ptr("STATIC")
	comboClass := syscall.StringToUTF16Ptr("COMBOBOX")
	buttonClass := syscall.StringToUTF16Ptr("BUTTON")

	lblAcc, _, _ := createWindowEx.Call(0, uintptr(unsafe.Pointer(staticClass)), uintptr(unsafe.Pointer(syscall.StringToUTF16Ptr("账号:"))), wsChild|wsVisible, 35, 30, 60, 20, settingsWindow, 0, instance, 0)
	t.accountInput, _, _ = createWindowEx.Call(0x200, uintptr(unsafe.Pointer(editClass)), 0, wsChild|wsVisible|wsBorder|esAutohscroll|wsTabstop, 105, 27, 215, 24, settingsWindow, 0, instance, 0)

	lblPwd, _, _ := createWindowEx.Call(0, uintptr(unsafe.Pointer(staticClass)), uintptr(unsafe.Pointer(syscall.StringToUTF16Ptr("密码:"))), wsChild|wsVisible, 35, 70, 60, 20, settingsWindow, 0, instance, 0)
	t.passwordInput, _, _ = createWindowEx.Call(0x200, uintptr(unsafe.Pointer(editClass)), 0, wsChild|wsVisible|wsBorder|esPassword|esAutohscroll|wsTabstop, 105, 67, 215, 24, settingsWindow, 0, instance, 0)

	lblOp, _, _ := createWindowEx.Call(0, uintptr(unsafe.Pointer(staticClass)), uintptr(unsafe.Pointer(syscall.StringToUTF16Ptr("运营商:"))), wsChild|wsVisible, 35, 110, 60, 20, settingsWindow, 0, instance, 0)
	t.operatorCombo, _, _ = createWindowEx.Call(0, uintptr(unsafe.Pointer(comboClass)), 0, wsChild|wsVisible|cbsDropdownList|wsTabstop, 105, 107, 215, 200, settingsWindow, 0, instance, 0)

	for _, name := range []string{"中国移动", "中国电信", "中国联通", "校园网"} {
		sPtr := syscall.StringToUTF16Ptr(name)
		sendMessage.Call(t.operatorCombo, cbAddString, 0, uintptr(unsafe.Pointer(sPtr)))
	}

	btnSave, _, _ := createWindowEx.Call(0, uintptr(unsafe.Pointer(buttonClass)), uintptr(unsafe.Pointer(syscall.StringToUTF16Ptr("保存"))), wsChild|wsVisible|bsDefPushButton|wsTabstop, 95, 160, 85, 30, settingsWindow, cmdSave, instance, 0)
	btnCancel, _, _ := createWindowEx.Call(0, uintptr(unsafe.Pointer(buttonClass)), uintptr(unsafe.Pointer(syscall.StringToUTF16Ptr("取消"))), wsChild|wsVisible|wsTabstop, 205, 160, 85, 30, settingsWindow, cmdCancel, instance, 0)

	if t.fontUI != 0 {
		for _, c := range []uintptr{lblAcc, t.accountInput, lblPwd, t.passwordInput, lblOp, t.operatorCombo, btnSave, btnCancel} {
			sendMessage.Call(c, wmSetFont, t.fontUI, 1)
		}
	}

	setConsoleWriter(t)
	if initialConsole {
		t.toggleConsole()
	}

	ready <- nil

	var msg winMessage
	for {
		ret, _, _ := getMessage.Call(uintptr(unsafe.Pointer(&msg)), 0, 0, 0)
		if ret == 0 || ret == ^uintptr(0) {
			return
		}
		if t.settingsWindow != 0 {
			res, _, _ := isDialogMessage.Call(t.settingsWindow, uintptr(unsafe.Pointer(&msg)))
			if res != 0 {
				continue
			}
		}
		translateMessage.Call(uintptr(unsafe.Pointer(&msg)))
		dispatchMessage.Call(uintptr(unsafe.Pointer(&msg)))
	}
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
	hwnd := t.hwnd
	t.mu.Unlock()
	if hwnd != 0 {
		postMessage.Call(hwnd, statusMessage, 0, 0)
	}
}

func (t *trayUI) updateIcon(action uintptr) bool {
	t.mu.Lock()
	dash, hwnd := t.dash, t.hwnd
	t.mu.Unlock()

	var kind iconKind
	switch dash.status {
	case "已连接":
		if dash.isCampus {
			kind = iconCampusOnline
		} else {
			kind = iconExternalOnline
		}
	case "连接中", "获取地址中", "检测中":
		kind = iconConnecting
	default:
		kind = iconOffline
	}

	newIcon := createShieldIcon(kind)
	if newIcon == 0 {
		return false
	}

	data := notifyIconData{
		size:     uint32(unsafe.Sizeof(notifyIconData{})),
		hwnd:     hwnd,
		id:       1,
		flags:    nifMessageIconTip,
		callback: trayMessage,
		icon:     newIcon,
	}
	copy(data.tip[:], syscall.StringToUTF16("CampusKeeper"))
	result, _, _ := shellNotifyIcon.Call(action, uintptr(unsafe.Pointer(&data)))
	if result == 0 && action == nimAdd {
		for i := 0; i < 5; i++ {
			time.Sleep(200 * time.Millisecond)
			result, _, _ = shellNotifyIcon.Call(action, uintptr(unsafe.Pointer(&data)))
			if result != 0 {
				break
			}
		}
	}

	t.mu.Lock()
	if t.currentIcon != 0 {
		destroyIcon.Call(t.currentIcon)
	}
	t.currentIcon = newIcon
	t.mu.Unlock()

	return result != 0
}

func (t *trayUI) popupMenu() {
	menu, _, _ := createPopupMenu.Call()
	if menu == 0 {
		return
	}
	defer destroyMenu.Call(menu)

	t.mu.Lock()
	dash := t.dash
	hwnd := t.hwnd
	visible := t.consoleVisible
	t.mu.Unlock()

	// 1. Header (disabled)
	appendMenu.Call(menu, mfString|mfDisabled|mfGrayed, 0, uintptr(unsafe.Pointer(syscall.StringToUTF16Ptr("CampusKeeper"))))
	appendMenu.Call(menu, mfSeparator, 0, 0)

	// 2. Minimalist Dashboard
	appendMenu.Call(menu, mfString|mfDisabled|mfGrayed, 0, uintptr(unsafe.Pointer(syscall.StringToUTF16Ptr("状态："+dash.status))))
	appendMenu.Call(menu, mfString|mfDisabled|mfGrayed, 0, uintptr(unsafe.Pointer(syscall.StringToUTF16Ptr("网络："+dash.network))))
	if dash.operator != "" {
		appendMenu.Call(menu, mfString|mfDisabled|mfGrayed, 0, uintptr(unsafe.Pointer(syscall.StringToUTF16Ptr("运营商："+dash.operator))))
	}
	if dash.ip != "" {
		appendMenu.Call(menu, mfString|mfDisabled|mfGrayed, 0, uintptr(unsafe.Pointer(syscall.StringToUTF16Ptr("地址："+dash.ip))))
	}
	appendMenu.Call(menu, mfSeparator, 0, 0)

	// 3. Actions
	appendMenu.Call(menu, mfString, menuCmdReconnect, uintptr(unsafe.Pointer(syscall.StringToUTF16Ptr("重新连接"))))
	appendMenu.Call(menu, mfString, menuCmdSettings, uintptr(unsafe.Pointer(syscall.StringToUTF16Ptr("账号设置..."))))
	logLabel := "运行日志..."
	if visible {
		logLabel = "隐藏日志窗口"
	}
	appendMenu.Call(menu, mfString, menuCmdLogs, uintptr(unsafe.Pointer(syscall.StringToUTF16Ptr(logLabel))))
	appendMenu.Call(menu, mfString, menuCmdOpenDir, uintptr(unsafe.Pointer(syscall.StringToUTF16Ptr("日志目录"))))
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
		t.toggleConsole()
	case menuCmdOpenDir:
		dir := appDataDir()
		shellExecute.Call(0, uintptr(unsafe.Pointer(syscall.StringToUTF16Ptr("open"))), uintptr(unsafe.Pointer(syscall.StringToUTF16Ptr(dir))), 0, 0, 1)
	case menuCmdExit:
		t.quit()
	}
}

func (t *trayUI) showConsole() {
	t.mu.Lock()
	t.consoleVisible = true
	t.mu.Unlock()
	showWindow.Call(t.logWindow, 5)
	t.updateLogs()
	setForegroundWindow.Call(t.logWindow)
}

func (t *trayUI) toggleConsole() {
	t.mu.Lock()
	if t.consoleVisible {
		t.consoleVisible = false
		t.mu.Unlock()
		showWindow.Call(t.logWindow, 0)
		return
	}
	t.mu.Unlock()
	t.showConsole()
}

func getEditText(hwnd uintptr) string {
	length, _, _ := getWindowTextLength.Call(hwnd)
	if length == 0 {
		return ""
	}
	buf := make([]uint16, length+1)
	getWindowText.Call(hwnd, uintptr(unsafe.Pointer(&buf[0])), length+1)
	return strings.TrimSpace(syscall.UTF16ToString(buf))
}

func (t *trayUI) doOpenSettings() {
	if t.settingsWindow == 0 {
		return
	}
	if t.getConfig != nil {
		cfg := t.getConfig()
		setWindowText.Call(t.accountInput, uintptr(unsafe.Pointer(syscall.StringToUTF16Ptr(cfg.account))))
		setWindowText.Call(t.passwordInput, uintptr(unsafe.Pointer(syscall.StringToUTF16Ptr(cfg.password))))
		sel := 0
		switch cfg.operator {
		case operatorCMCC:
			sel = 0
		case operatorTelecom:
			sel = 1
		case operatorUnicom:
			sel = 2
		case operatorCampus:
			sel = 3
		}
		sendMessage.Call(t.operatorCombo, cbSetCurSel, uintptr(sel), 0)
	}
	showWindow.Call(t.settingsWindow, 5)
	setForegroundWindow.Call(t.settingsWindow)
}

func (t *trayUI) openSettings() {
	t.mu.Lock()
	hwnd := t.hwnd
	t.mu.Unlock()
	if hwnd != 0 {
		postMessage.Call(hwnd, settingsMessage, 0, 0)
	}
}

func (t *trayUI) Write(p []byte) (int, error) {
	t.mu.Lock()
	t.logText += string(p)
	if len(t.logText) > 48<<10 {
		t.logText = t.logText[len(t.logText)-(40<<10):]
		if i := strings.IndexByte(t.logText, '\n'); i >= 0 {
			t.logText = t.logText[i+1:]
		}
	}
	visible, hwnd := t.consoleVisible, t.hwnd
	t.mu.Unlock()
	if visible && hwnd != 0 {
		postMessage.Call(hwnd, logMessage, 0, 0)
	}
	return len(p), nil
}

func (t *trayUI) updateLogs() {
	t.mu.Lock()
	content := t.logText
	edit := t.logEdit
	t.mu.Unlock()
	if edit != 0 {
		followTail := logAtBottom(edit)
		firstLine, _, _ := sendMessage.Call(edit, emGetFirstLine, 0, 0)
		text := syscall.StringToUTF16(strings.ReplaceAll(content, "\n", "\r\n"))
		setWindowText.Call(edit, uintptr(unsafe.Pointer(&text[0])))
		if followTail {
			end := uintptr(len(text) - 1)
			sendMessage.Call(edit, emSetSel, end, end)
			sendMessage.Call(edit, emScrollCaret, 0, 0)
		} else {
			currentLine, _, _ := sendMessage.Call(edit, emGetFirstLine, 0, 0)
			if firstLine > currentLine {
				sendMessage.Call(edit, emLineScroll, 0, firstLine-currentLine)
			}
		}
	}
}

func logAtBottom(edit uintptr) bool {
	info := scrollInfo{size: uint32(unsafe.Sizeof(scrollInfo{})), mask: 0x7}
	ok, _, _ := getScrollInfo.Call(edit, 1, uintptr(unsafe.Pointer(&info)))
	if ok == 0 || info.page == 0 {
		return true
	}
	lastPage := int64(info.maximum) - int64(info.page) + 1
	return int64(info.position) >= lastPage-1
}

func (t *trayUI) close() {
	setConsoleWriter(io.Discard)
	t.mu.Lock()
	hwnd := t.hwnd
	icon := t.currentIcon
	fUI := t.fontUI
	fLog := t.fontLog
	t.mu.Unlock()

	if hwnd != 0 {
		postMessage.Call(hwnd, wmClose, 0, 0)
	}
	if icon != 0 {
		destroyIcon.Call(icon)
	}
	if fUI != 0 {
		deleteObject.Call(fUI)
	}
	if fLog != 0 {
		deleteObject.Call(fLog)
	}
	<-t.done
}
