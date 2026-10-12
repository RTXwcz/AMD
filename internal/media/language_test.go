package media

import (
	"os"
	"strings"
	"testing"

	"ez-am/internal/i18n"
)

// The downloader's view assertions were written against the Chinese UI, which
// is also what a zh-CN Windows selects by default.
func TestMain(m *testing.M) {
	i18n.Set(i18n.ZH)
	code := m.Run()
	i18n.Set(i18n.EN)
	os.Exit(code)
}

// TestEnglishRendering keeps the English catalog honest for the same views the
// Chinese assertions cover.
func TestEnglishRendering(t *testing.T) {
	i18n.Set(i18n.EN)
	defer i18n.Set(i18n.ZH)

	m := initialModel()
	if view := m.View(); !strings.Contains(view, "Paste an Apple Music album, song or playlist link:") {
		t.Fatalf("english link view = %q", view)
	}

	m.stage = sMetadata
	if view := m.View(); !strings.Contains(view, "Reading album tracks") {
		t.Fatalf("english metadata view = %q", view)
	}

	m.stage = sFormat
	if view := m.View(); !strings.Contains(view, "Choose audio format") {
		t.Fatalf("english format view = %q", view)
	}

	m.stage = sDone
	if view := m.View(); !strings.Contains(view, "Download complete") {
		t.Fatalf("english done view = %q", view)
	}
}

// TestProgressLabelsFollowTheLanguage guards the stage labels, which are easy
// to forget because they live outside the view functions.
func TestProgressLabelsFollowTheLanguage(t *testing.T) {
	defer i18n.Set(i18n.ZH)

	i18n.Set(i18n.EN)
	if got := progressLabel(progressValidating); got != "Validate arguments" {
		t.Fatalf("english stage label = %q", got)
	}
	if got := progressLabel("unknown-stage"); got != "Preparing the download" {
		t.Fatalf("english default label = %q", got)
	}

	i18n.Set(i18n.ZH)
	if got := progressLabel(progressFinalize); got != "解密、封装与写入标签" {
		t.Fatalf("chinese stage label = %q", got)
	}
}
