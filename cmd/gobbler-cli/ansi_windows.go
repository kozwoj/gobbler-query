package main

import (
	"os"
	"syscall"
)

var (
	kernel32       = syscall.NewLazyDLL("kernel32.dll")
	setConsoleMode = kernel32.NewProc("SetConsoleMode")
)

const enableVirtualTerminalProcessing = 0x0004

func enableANSI() {
	stdout := syscall.Handle(os.Stdout.Fd())
	var mode uint32
	if err := syscall.GetConsoleMode(stdout, &mode); err != nil {
		return
	}
	setConsoleMode.Call(uintptr(stdout), uintptr(mode|enableVirtualTerminalProcessing))
}
