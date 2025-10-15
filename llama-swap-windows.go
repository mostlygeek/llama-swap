//go:build windows

package main

import (
	_ "embed"
	"flag"
	"log"
	"os"
	"os/exec"
	"syscall"

	"github.com/getlantern/systray"
	"golang.org/x/sys/windows"
)

const TargetURL = "http://localhost"

//go:embed ui/public/favicon.ico
var iconData []byte
var tray *bool

func addFlagsIfNeed(flag *flag.FlagSet) {
	tray = flag.Bool("tray", false, "add tray icon")
}

func restartIfNeed() {
	if !*tray {
		return
	}

	kernel32 := syscall.MustLoadDLL("kernel32.dll")
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
		log.Fatalf("Fatal restart error: %v\n", err)
	}
	os.Exit(0)
}

func runTrayIfAvailable() {
	if !*tray {
		return
	}

	systray.Run(onReady, onExit)
}

func onReady() {

	systray.SetIcon(iconData)
	systray.SetTitle("llamaSwap")
	systray.SetTooltip("llamaSwap")

	mOpenLog := systray.AddMenuItem("Open web ui", "Open llamaSwap-Web front page")
	mOpenModel := systray.AddMenuItem("Open model", "Open llamaSwap-Web model page")
	mTerminateChild := systray.AddMenuItem("Exit", "Exit llamaSwap")

	go func() {
		for {
			select {
			case <-mOpenLog.ClickedCh:
				openBrowser("/ui")

			case <-mOpenModel.ClickedCh:
				openBrowser("/ui/models")

			case <-mTerminateChild.ClickedCh:
				systray.Quit()
			}
		}
	}()
}
func onExit() {
	sigChan <- syscall.SIGINT
}

func openBrowser(page string) {
	err := exec.Command("rundll32", "url.dll,FileProtocolHandler", TargetURL+*listenStr+page).Start()
	if err != nil {
		showErrorMessageBox("Can't launch browser\n" + err.Error())
	}
}

func showErrorMessageBox(message string) {
	titlePtr, _ := windows.UTF16PtrFromString("Error")
	messagePtr, _ := windows.UTF16PtrFromString(message)
	windows.MessageBox(0, messagePtr, titlePtr, 0)
}
