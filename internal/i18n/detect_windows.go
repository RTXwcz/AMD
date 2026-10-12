//go:build windows

package i18n

import "syscall"

// GetUserDefaultUILanguage reports the Windows UI language; its primary
// language id is the low byte (0x04 is Chinese).
var (
	kernel32                     = syscall.NewLazyDLL("kernel32.dll")
	procGetUserDefaultUILanguage = kernel32.NewProc("GetUserDefaultUILanguage")
)

const primaryLanguageChinese = 0x04

func systemLang() Lang {
	value, _, _ := procGetUserDefaultUILanguage.Call()
	if value == 0 {
		return EN
	}
	if byte(value) == primaryLanguageChinese {
		return ZH
	}
	return EN
}
