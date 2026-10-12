package i18n

import (
	"os"
	"path/filepath"
	"testing"
)

func TestCatalogIsComplete(t *testing.T) {
	if len(catalog[EN]) == 0 || len(catalog[ZH]) == 0 {
		t.Fatal("both languages must have a catalog")
	}
	for key, english := range catalog[EN] {
		chinese, ok := catalog[ZH][key]
		if !ok {
			t.Errorf("key %q is missing from the Chinese catalog", key)
			continue
		}
		if english == "" || chinese == "" {
			t.Errorf("key %q must be non-empty in both languages", key)
		}
	}
	for key := range catalog[ZH] {
		if _, ok := catalog[EN][key]; !ok {
			t.Errorf("key %q is missing from the English catalog", key)
		}
	}
}

func TestTranslationsSwitch(t *testing.T) {
	defer Set(EN)

	Set(EN)
	if got := T("doctor.wsl.title"); got != "WSL" {
		t.Fatalf("english title = %q", got)
	}
	Set(ZH)
	if got := T("doctor.wsl.title"); got != "WSL" {
		t.Fatalf("chinese title = %q", got)
	}
	if got := T("control.action.login"); got == "" || got == "Log in to Apple (2FA)" {
		t.Fatalf("chinese action text = %q", got)
	}
	if got := T("doctor.header"); got != "ez-am 环境自检" {
		t.Fatalf("chinese header = %q", got)
	}
}

func TestMissingKeyIsVisible(t *testing.T) {
	Set(EN)
	if got := T("does.not.exist"); got != "does.not.exist" {
		t.Fatalf("unknown key = %q, want the key itself", got)
	}
}

func TestFormatting(t *testing.T) {
	defer Set(EN)
	Set(ZH)
	got := Tf("doctor.runtime.missing", "/home/a/amd", "dl.sh")
	if got != "/home/a/amd 下缺少：dl.sh" {
		t.Fatalf("formatted = %q", got)
	}
	Set(EN)
	if got := Tf("doctor.runtime.ok", "/home/a/amd"); got != "installed under /home/a/amd" {
		t.Fatalf("formatted = %q", got)
	}
}

func TestNormalize(t *testing.T) {
	cases := map[string]Lang{
		"":            EN,
		"en":          EN,
		"en-US":       EN,
		"zh":          ZH,
		"zh-CN":       ZH,
		"zh_TW.UTF-8": ZH,
		"chinese":     ZH,
		"中文":          ZH,
		"de":          EN,
		"fr-FR":       EN,
	}
	for input, want := range cases {
		if got := Normalize(input); got != want {
			t.Errorf("Normalize(%q) = %q, want %q", input, got, want)
		}
	}
}

func TestToggleFlipsTheLanguage(t *testing.T) {
	defer Set(EN)
	Set(EN)
	if got := Toggle(); got != ZH {
		t.Fatalf("toggle from EN = %q", got)
	}
	if got := Toggle(); got != EN {
		t.Fatalf("toggle from ZH = %q", got)
	}
}

func TestResolvePrecedence(t *testing.T) {
	configDir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", configDir)
	t.Setenv(EnvVar, "")
	t.Setenv("LANG", "zh_CN.UTF-8")

	if got := Resolve("en"); got != EN {
		t.Fatalf("explicit flag must win, got %q", got)
	}
	if got := Resolve(""); got != ZH {
		t.Fatalf("system locale must apply, got %q", got)
	}
	t.Setenv(EnvVar, "en")
	if got := Resolve(""); got != EN {
		t.Fatalf("environment must win over the locale, got %q", got)
	}
}

func TestSavedLanguageRoundTrip(t *testing.T) {
	configDir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", configDir)
	t.Setenv(EnvVar, "")
	t.Setenv("LANG", "en_US.UTF-8")

	if got := Resolve(""); got != EN {
		t.Fatalf("initial resolution = %q", got)
	}
	if err := Save(ZH); err != nil {
		t.Fatalf("Save: %v", err)
	}
	if got := Resolve(""); got != ZH {
		t.Fatalf("saved language must win over the locale, got %q", got)
	}
	path := filepath.Join(configDir, "ez-am", "settings.json")
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("settings file: %v", err)
	}
}

func TestSaveIsPrivate(t *testing.T) {
	configDir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", configDir)
	if err := Save(ZH); err != nil {
		t.Fatalf("Save: %v", err)
	}
	info, err := os.Stat(filepath.Join(configDir, "ez-am", "settings.json"))
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("settings mode = %v, want 0600", info.Mode().Perm())
	}
}
