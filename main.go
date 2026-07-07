package main

import (
	"embed"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	webview2 "github.com/jchv/go-webview2"
)

//go:embed static/index.html
var staticFiles embed.FS

// ═══════════════════════════════════════════════
// 配置常量
// ═══════════════════════════════════════════════

const (
	ua    = "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36"
	debug = false
)

func plFile() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".melody3_playlist.json")
}

// ═══════════════════════════════════════════════
// DPI 感知
// ═══════════════════════════════════════════════

func setDPIAware() {
	user32 := syscall.NewLazyDLL("user32.dll")
	user32.NewProc("SetProcessDPIAware").Call()
}

// ═══════════════════════════════════════════════
// 端口清理
// ═══════════════════════════════════════════════

func killPort() {
	cmd := exec.Command("cmd", "/c",
		fmt.Sprintf("netstat -ano | findstr :%d", defaultConfig.Port))
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
	out, err := cmd.Output()
	if err != nil {
		return
	}

	killed := make(map[string]bool)
	for _, line := range strings.Split(string(out), "\n") {
		if !strings.Contains(line, "LISTENING") {
			continue
		}
		parts := strings.Fields(line)
		if len(parts) > 0 {
			pid := parts[len(parts)-1]
			if !killed[pid] {
				killCmd := exec.Command("taskkill", "/f", "/pid", pid)
				killCmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
				killCmd.Run()
				killed[pid] = true
			}
		}
	}
}

// ═══════════════════════════════════════════════
// 主入口
// ═══════════════════════════════════════════════

func main() {
	setDPIAware()
	killPort()
	time.Sleep(1 * time.Second)

	// 启动 HTTP 服务器
	server := NewServer(defaultConfig)
	go func() {
		if err := server.Start(); err != nil && err != http.ErrServerClosed {
			fmt.Printf("Server error: %v\n", err)
		}
	}()

	// 等待服务器就绪
	addr := fmt.Sprintf("http://127.0.0.1:%d", defaultConfig.Port)
	for i := 0; i < 50; i++ {
		resp, err := http.Get(addr + "/")
		if err == nil {
			resp.Body.Close()
			break
		}
		time.Sleep(200 * time.Millisecond)
	}

	// 打开 webview 窗口（无边框，使用自定义标题栏）
	w := webview2.New(debug)
	defer w.Destroy()

	w.SetTitle("MelodyV3")
	w.Navigate(addr + "?v=" + fmt.Sprintf("%d_%d", time.Now().UnixNano(), time.Now().Unix()%1000))

	// 设置窗口为无边框（去掉 Windows 默认标题栏）
	hwnd := w.Window()
	if hwnd != nil {
		user32 := syscall.NewLazyDLL("user32.dll")
		getWindowLong := user32.NewProc("GetWindowLongW")
		setWindowLong := user32.NewProc("SetWindowLongW")
		setWindowPos := user32.NewProc("SetWindowPos")

		style, _, _ := getWindowLong.Call(uintptr(hwnd), uintptr(0xFFFFFFFFFFFFFFF0)) // GWL_STYLE = -16
		style &^= 0x00C00000 // WS_CAPTION
		style &^= 0x00040000 // WS_THICKFRAME
		setWindowLong.Call(uintptr(hwnd), uintptr(0xFFFFFFFFFFFFFFF0), style)
		setWindowPos.Call(uintptr(hwnd), 0, 0, 0, 0, 0,
			0x0020|0x0002|0x0001|0x0004) // SWP_FRAMECHANGED|SWP_NOMOVE|SWP_NOSIZE|SWP_NOZORDER

		// 最大化
		showWindow := user32.NewProc("ShowWindow")
		showWindow.Call(uintptr(hwnd), 3) // SW_MAXIMIZE
	}

	// 处理自定义标题栏的消息（最小化/最大化/关闭）
	w.Bind("titlebarAction", func(action string) string {
		if hwnd == nil {
			return "error"
		}
		user32 := syscall.NewLazyDLL("user32.dll")
		switch action {
		case "minimize":
			showWindow := user32.NewProc("ShowWindow")
			showWindow.Call(uintptr(hwnd), 6) // SW_MINIMIZE
		case "maximize":
			showWindow := user32.NewProc("ShowWindow")
			isZoomed, _, _ := user32.NewProc("IsZoomed").Call(uintptr(hwnd))
			if isZoomed != 0 {
				showWindow.Call(uintptr(hwnd), 9) // SW_RESTORE
			} else {
				showWindow.Call(uintptr(hwnd), 3) // SW_MAXIMIZE
			}
		case "close":
			w.Destroy()
		}
		return "ok"
	})

	w.Run()
}
