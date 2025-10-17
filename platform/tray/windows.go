//go:build windows

package tray

import (
	_ "embed"
	"log"
	"os"
	"os/exec"
	"syscall"

	"fyne.io/systray"
	"golang.org/x/sys/windows"
)

//go:embed favicon.ico
var iconData []byte

func New(onexit func(), webpage string) Tray {
	return &WindowsTray{
		onExit:  onexit,
		webPage: webpage,
	}
}

// restartIfNeeded checks if the program is running with a console window attached.
// If a console window is detected, it stops the current process and restarts it
// without the console window, allowing it to run as a system tray application.
func restartIfNeeded() {

	kernel32 := windows.MustLoadDLL("kernel32.dll")
	defer kernel32.Release()
	getConsoleWindow := kernel32.MustFindProc("GetConsoleWindow")
	ret, _, _ := getConsoleWindow.Call()
	if ret == 0 {
		return
	}

	programPath := os.Args[0]
	cmd := exec.Command(programPath, os.Args[1:]...)
	cmd.SysProcAttr = &syscall.SysProcAttr{
		HideWindow:    true,
		CreationFlags: windows.CREATE_NO_WINDOW,
	}
	err := cmd.Start()
	if err != nil {
		log.Fatalf("Failed to restart after closing console window: %v\n", err)
	}
	os.Exit(0)
}

type WindowsTray struct {
	webPage string
	onExit  func()
}

func (t *WindowsTray) Start() {
	restartIfNeeded()
	systray.Run(t.onReady, t.onExit)
}

func (t *WindowsTray) onReady() {

	systray.SetIcon(iconData)
	systray.SetTitle("llama-swap")
	systray.SetTooltip("show llama-swap options")

	mOpenWeb := systray.AddMenuItem("Show UI", "open user interface in default browser")
	mTerminate := systray.AddMenuItem("Quit", "quit llama-swap process")

	go func() {
		for {
			select {
			case <-mOpenWeb.ClickedCh:
				openBrowser(t.webPage)
			case <-mTerminate.ClickedCh:
				systray.Quit()
				return
			}
		}
	}()
}

func openBrowser(page string) {
	err := exec.Command("rundll32", "url.dll,FileProtocolHandler", page).Start()
	if err != nil {
		showErrorMessageBox("Can't launch browser\n" + err.Error())
	}
}

func showErrorMessageBox(message string) {
	titlePtr, _ := windows.UTF16PtrFromString("Error")
	messagePtr, _ := windows.UTF16PtrFromString(message)
	windows.MessageBox(0, messagePtr, titlePtr, 0)
}
