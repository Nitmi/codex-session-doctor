//go:build windows

package main

import "syscall"

func detectSystemLanguage() language {
	kernel32 := syscall.NewLazyDLL("kernel32.dll")
	getUserDefaultUILanguage := kernel32.NewProc("GetUserDefaultUILanguage")

	langID, _, err := getUserDefaultUILanguage.Call()
	if langID == 0 && err != syscall.Errno(0) {
		return ""
	}

	// LANGID lower 10 bits are the primary language id. 0x04 is Chinese.
	if uint16(langID)&0x03ff == 0x04 {
		return langZH
	}
	if uint16(langID)&0x03ff == 0x09 {
		return langEN
	}

	return ""
}
