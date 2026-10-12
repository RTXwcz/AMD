//go:build !windows

package i18n

import "os"

// systemLang reads the POSIX locale environment.
func systemLang() Lang {
	for _, key := range []string{"LC_ALL", "LC_MESSAGES", "LANG"} {
		if value := os.Getenv(key); value != "" {
			return Normalize(value)
		}
	}
	return EN
}
