// Package i18n holds the user-facing strings of ez-am in English and Chinese
// and resolves which language to use: an explicit flag, the environment, a
// saved choice, or the operating system locale, in that order.
package i18n

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

// Lang is a supported interface language.
type Lang string

const (
	// EN is the default, and also the fallback for a missing translation.
	EN Lang = "en"
	// ZH is Simplified Chinese.
	ZH Lang = "zh"
)

// EnvVar overrides the saved/detected language.
const EnvVar = "EZ_AM_LANG"

var (
	mutex   sync.RWMutex
	current = EN
)

// Set selects the active language. Unknown values fall back to EN.
func Set(l Lang) {
	normalized := Normalize(string(l))
	mutex.Lock()
	current = normalized
	mutex.Unlock()
}

// Current reports the active language.
func Current() Lang {
	mutex.RLock()
	defer mutex.RUnlock()
	return current
}

// Normalize maps user input ("zh", "zh-CN", "chinese", "") to a Lang.
func Normalize(value string) Lang {
	lowered := strings.ToLower(strings.TrimSpace(value))
	switch {
	case lowered == "":
		return EN
	case strings.HasPrefix(lowered, "zh"), lowered == "chinese", lowered == "cn", lowered == "中文":
		return ZH
	default:
		return EN
	}
}

// T returns the message for key in the active language. A missing translation
// falls back to English, and a completely unknown key returns the key itself so
// that a gap is visible rather than silent.
func T(key string) string {
	mutex.RLock()
	lang := current
	mutex.RUnlock()

	if messages, ok := catalog[lang]; ok {
		if text, ok := messages[key]; ok && text != "" {
			return text
		}
	}
	if text, ok := catalog[EN][key]; ok && text != "" {
		return text
	}
	return key
}

// Tf formats a translated message; a key that needs no formatting is returned
// unchanged.
func Tf(key string, args ...any) string {
	format := T(key)
	if len(args) == 0 {
		return format
	}
	return sprintf(format, args...)
}

// Toggle switches between the supported languages and reports the new one.
func Toggle() Lang {
	if Current() == ZH {
		Set(EN)
	} else {
		Set(ZH)
	}
	return Current()
}

// Resolve picks the language to start with: explicit flag, then EZ_AM_LANG,
// then a saved choice, then the operating system locale.
func Resolve(flagValue string) Lang {
	if strings.TrimSpace(flagValue) != "" {
		return Normalize(flagValue)
	}
	if env := os.Getenv(EnvVar); strings.TrimSpace(env) != "" {
		return Normalize(env)
	}
	if saved, ok := loadSaved(); ok {
		return saved
	}
	return systemLang()
}

// settingsPath is where the chosen language is remembered.
func settingsPath() (string, error) {
	dir, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "ez-am", "settings.json"), nil
}

type settings struct {
	Lang string `json:"lang"`
}

func loadSaved() (Lang, bool) {
	path, err := settingsPath()
	if err != nil {
		return EN, false
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return EN, false
	}
	var stored settings
	if err := json.Unmarshal(data, &stored); err != nil || stored.Lang == "" {
		return EN, false
	}
	return Normalize(stored.Lang), true
}

// Save remembers the language for the next run. A failure to write is not fatal
// for the current session.
func Save(l Lang) error {
	path, err := settingsPath()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	data, err := json.MarshalIndent(settings{Lang: string(l)}, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, append(data, '\n'), 0o600)
}
