//go:build windows

package main

import (
	"errors"
	"io"
	"runtime"
	"strings"
	"sync"
	"syscall"
	"unsafe"
)

const (
	trayMessage       = 0x8001
	statusMessage     = 0x8002
	logMessage        = 0x8003
	wmClose           = 0x0010
	wmDestroy         = 0x0002
	wmSize            = 0x0005
	wmRButtonUp       = 0x0205
	wmLButtonDblClk   = 0x0203
	nimAdd            = 0
	nimModify         = 1
	nimDelete         = 2
	nifMessageIconTip = 0x7
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
	loadIcon            = user32.NewProc("LoadIconW")
	showWindow          = user32.NewProc("ShowWindow")
	moveWindow          = user32.NewProc("MoveWindow")
	setWindowText       = user32.NewProc("SetWindowTextW")
	createPopupMenu     = user32.NewProc("CreatePopupMenu")
	appendMenu          = user32.NewProc("AppendMenuW")
	trackPopupMenu      = user32.NewProc("TrackPopupMenu")
	destroyMenu         = user32.NewProc("DestroyMenu")
	getCursorPos        = user32.NewProc("GetCursorPos")
	setForegroundWindow = user32.NewProc("SetForegroundWindow")
	shellNotifyIcon     = shell32.NewProc("Shell_NotifyIconW")
	getModuleHandle     = kernel32.NewProc("GetModuleHandleW")
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

type trayUI struct {
	mu             sync.Mutex
	hwnd           uintptr
	logWindow      uintptr
	logEdit        uintptr
	logText        string
	status         string
	consoleVisible bool
	quit           func()
	done           chan struct{}
}

func startTray(quit func(), showConsole bool) (*trayUI, error) {
	t := &trayUI{status: "启动中", quit: quit, done: make(chan struct{})}
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
	instance, _, _ := getModuleHandle.Call(0)
	className := syscall.StringToUTF16Ptr("WenchangCampusKeeperTray")
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
			if t.logWindow != 0 {
				destroyWindow.Call(t.logWindow)
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
	logTitle := syscall.StringToUTF16Ptr("文昌校园网守护程序 - 运行日志")
	logWindow, _, _ := createWindowEx.Call(0, uintptr(unsafe.Pointer(className)), uintptr(unsafe.Pointer(logTitle)), 0x00CF0000, 100, 100, 820, 480, 0, 0, instance, 0)
	if logWindow == 0 {
		ready <- errors.New("无法创建日志窗口")
		destroyWindow.Call(hwnd)
		return
	}
	t.logWindow = logWindow
	editClass := syscall.StringToUTF16Ptr("EDIT")
	// Read-only multiline text control, with a vertical scrollbar.
	edit, _, _ := createWindowEx.Call(0x200, uintptr(unsafe.Pointer(editClass)), 0, 0x50200844, 0, 0, 800, 440, logWindow, 0, instance, 0)
	if edit == 0 {
		ready <- errors.New("无法创建日志文本框")
		destroyWindow.Call(logWindow)
		destroyWindow.Call(hwnd)
		return
	}
	t.logEdit = edit
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
		translateMessage.Call(uintptr(unsafe.Pointer(&msg)))
		dispatchMessage.Call(uintptr(unsafe.Pointer(&msg)))
	}
}

func (t *trayUI) setStatus(status string) {
	t.mu.Lock()
	t.status = status
	hwnd := t.hwnd
	t.mu.Unlock()
	if hwnd != 0 {
		postMessage.Call(hwnd, statusMessage, 0, 0)
	}
}

func (t *trayUI) updateIcon(action uintptr) bool {
	t.mu.Lock()
	status, hwnd := t.status, t.hwnd
	t.mu.Unlock()
	iconID := uintptr(32512) // application icon
	switch status {
	case "外网正常":
		iconID = 32516 // information
	case "外网中断":
		iconID = 32515 // warning
	case "网线或接口未就绪":
		iconID = 32513 // error
	}
	icon, _, _ := loadIcon.Call(0, iconID)
	data := notifyIconData{size: uint32(unsafe.Sizeof(notifyIconData{})), hwnd: hwnd, id: 1, flags: nifMessageIconTip, callback: trayMessage, icon: icon}
	copy(data.tip[:], syscall.StringToUTF16("校园网："+status))
	result, _, _ := shellNotifyIcon.Call(action, uintptr(unsafe.Pointer(&data)))
	return result != 0
}

func (t *trayUI) popupMenu() {
	menu, _, _ := createPopupMenu.Call()
	if menu == 0 {
		return
	}
	defer destroyMenu.Call(menu)
	t.mu.Lock()
	visible := t.consoleVisible
	hwnd := t.hwnd
	t.mu.Unlock()
	label := "显示日志窗口"
	if visible {
		label = "隐藏日志窗口"
	}
	appendMenu.Call(menu, 0, 1, uintptr(unsafe.Pointer(syscall.StringToUTF16Ptr(label))))
	appendMenu.Call(menu, 0, 2, uintptr(unsafe.Pointer(syscall.StringToUTF16Ptr("退出程序"))))
	var point winPoint
	getCursorPos.Call(uintptr(unsafe.Pointer(&point)))
	setForegroundWindow.Call(hwnd)
	selected, _, _ := trackPopupMenu.Call(menu, 0x100|0x2, uintptr(point.x), uintptr(point.y), 0, hwnd, 0)
	switch selected {
	case 1:
		t.toggleConsole()
	case 2:
		t.quit()
	}
}

func (t *trayUI) toggleConsole() {
	t.mu.Lock()
	if t.consoleVisible {
		t.consoleVisible = false
		t.mu.Unlock()
		showWindow.Call(t.logWindow, 0)
		return
	}
	t.consoleVisible = true
	t.mu.Unlock()
	t.updateLogs()
	showWindow.Call(t.logWindow, 5)
	setForegroundWindow.Call(t.logWindow)
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
		setWindowText.Call(edit, uintptr(unsafe.Pointer(syscall.StringToUTF16Ptr(strings.ReplaceAll(content, "\n", "\r\n")))))
	}
}

func (t *trayUI) close() {
	setConsoleWriter(io.Discard)
	t.mu.Lock()
	hwnd := t.hwnd
	t.mu.Unlock()
	if hwnd != 0 {
		postMessage.Call(hwnd, wmClose, 0, 0)
	}
	<-t.done
}
