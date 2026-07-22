package main

import (
	"errors"
	"reflect"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

func TestAvailableProfilesRespectAdvertisedAudioTraits(t *testing.T) {
	tests := []struct {
		name   string
		traits []string
		want   []string
	}{
		{"lossless", []string{"lossless"}, []string{profileALAC48, profileAAC}},
		{"hi-res", []string{"lossless", "hi-res-lossless"}, []string{profileALAC48, profileALAC96, profileALAC192, profileAAC}},
		{"atmos lossless", []string{"lossless", "atmos"}, []string{profileALAC48, profileAtmos, profileAAC}},
		{"lossy", []string{"lossy-stereo"}, []string{profileAAC}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			track := TrackMetadata{AudioTraits: tt.traits}
			if got := availableProfiles(track); !reflect.DeepEqual(got, tt.want) {
				t.Fatalf("profiles = %#v, want %#v", got, tt.want)
			}
		})
	}
}

func TestDefaultProfileUsesBestAdvertisedLosslessCap(t *testing.T) {
	if got := defaultProfile(TrackMetadata{AudioTraits: []string{"lossless"}}); got != profileALAC48 {
		t.Fatalf("lossless default = %q", got)
	}
	if got := defaultProfile(TrackMetadata{AudioTraits: []string{"lossless", "hi-res-lossless"}}); got != profileALAC192 {
		t.Fatalf("hi-res default = %q", got)
	}
	if got := defaultProfile(TrackMetadata{AudioTraits: []string{"atmos"}}); got != profileAAC {
		t.Fatalf("atmos-only default = %q, want conservative AAC", got)
	}
}

func TestMetadataSuccessEntersTrackSelectionAndSelectsAll(t *testing.T) {
	m := initialModel()
	m.stage = sMetadata
	m.metadataRequestID = 8
	album := AlbumMetadata{ID: "1842050720", Name: "The Long Way Home", Tracks: []TrackMetadata{
		{ID: "1", Name: "Chinese Version", TrackNumber: 1, AudioTraits: []string{"lossless"}},
		{ID: "2", Name: "English Version", TrackNumber: 2, AudioTraits: []string{"lossless", "hi-res-lossless"}},
	}}
	next, cmd := m.Update(metadataResultMsg{requestID: 8, album: album})
	got := next.(model)
	if cmd != nil || got.stage != sTracks || len(got.tracks) != 2 {
		t.Fatalf("metadata update = %#v cmd=%v", got, cmd != nil)
	}
	for i, choice := range got.tracks {
		if !choice.selected {
			t.Fatalf("track %d not selected by default", i)
		}
	}
	if got.tracks[0].profile != profileALAC48 || got.tracks[1].profile != profileALAC192 {
		t.Fatalf("defaults = %q, %q", got.tracks[0].profile, got.tracks[1].profile)
	}
}

func TestAlbumSongQuerySelectsOnlyRequestedTrack(t *testing.T) {
	m := initialModel()
	m.stage = sMetadata
	m.metadataRequestID = 2
	m.appleURL = AppleMusicURL{Kind: "album", ID: "10", SongID: "2"}
	album := AlbumMetadata{ID: "10", Tracks: []TrackMetadata{{ID: "1"}, {ID: "2"}, {ID: "3"}}}
	next, _ := m.Update(metadataResultMsg{requestID: 2, album: album})
	got := next.(model)
	want := []bool{false, true, false}
	for i := range got.tracks {
		if got.tracks[i].selected != want[i] {
			t.Fatalf("selected[%d] = %v, want %v", i, got.tracks[i].selected, want[i])
		}
	}
}

func TestAlbumSongQueryMissingFromCatalogSelectsNothing(t *testing.T) {
	m := initialModel()
	m.stage = sMetadata
	m.metadataRequestID = 3
	m.appleURL = AppleMusicURL{Kind: "album", ID: "10", SongID: "999"}
	album := AlbumMetadata{ID: "10", Tracks: []TrackMetadata{{ID: "1"}, {ID: "2"}}}
	next, _ := m.Update(metadataResultMsg{requestID: 3, album: album})
	got := next.(model)
	if got.stage != sTracks || selectedTrackCount(got.tracks) != 0 || !strings.Contains(got.errMsg, "999") {
		t.Fatalf("missing song selection = %#v", got)
	}
}

func TestMetadataFailureFallsBackToWholeURLFormat(t *testing.T) {
	m := initialModel()
	m.stage = sMetadata
	m.metadataRequestID = 4
	next, cmd := m.Update(metadataResultMsg{requestID: 4, err: errors.New("catalog unavailable")})
	got := next.(model)
	if cmd != nil || got.stage != sFormat || !strings.Contains(got.errMsg, "catalog unavailable") {
		t.Fatalf("fallback = %#v cmd=%v", got, cmd != nil)
	}
}

func TestEmptyAlbumCatalogFallsBackToWholeURLFormat(t *testing.T) {
	m := initialModel()
	m.stage = sMetadata
	m.metadataRequestID = 5
	next, _ := m.Update(metadataResultMsg{requestID: 5, album: AlbumMetadata{ID: "1"}})
	got := next.(model)
	if got.stage != sFormat || !strings.Contains(strings.ToLower(got.errMsg), "no tracks") {
		t.Fatalf("empty catalog fallback = %#v", got)
	}
}

func TestCanonicalAppleMusicURLNormalizesSupportedHost(t *testing.T) {
	got, err := canonicalAppleMusicURL("HTTPS://www.music.apple.com./us/album/title/123?l=en-US")
	if err != nil {
		t.Fatal(err)
	}
	if got != "https://music.apple.com/us/album/title/123?l=en-US" {
		t.Fatalf("canonical URL = %q", got)
	}
}

func TestTrackSelectionKeysToggleAndSkipUnavailableAtmos(t *testing.T) {
	m := initialModel()
	m.stage = sTracks
	m.tracks = []trackChoice{
		{track: TrackMetadata{ID: "1", AudioTraits: []string{"lossless"}}, selected: true, profile: profileALAC48},
		{track: TrackMetadata{ID: "2", AudioTraits: []string{"lossless", "atmos"}}, selected: true, profile: profileALAC48},
	}

	next, _ := m.Update(tea.KeyMsg{Type: tea.KeySpace})
	got := next.(model)
	if got.tracks[0].selected {
		t.Fatal("space did not deselect current track")
	}

	next, _ = got.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'a'}})
	got = next.(model)
	if !got.tracks[0].selected || !got.tracks[1].selected {
		t.Fatalf("a did not select all: %#v", got.tracks)
	}

	next, _ = got.Update(tea.KeyMsg{Type: tea.KeyRight})
	got = next.(model)
	if got.tracks[0].profile != profileAAC {
		t.Fatalf("lossless-only cycle = %q, want AAC without Atmos", got.tracks[0].profile)
	}

	got.trackCursor = 1
	next, _ = got.Update(tea.KeyMsg{Type: tea.KeyRight})
	got = next.(model)
	if got.tracks[1].profile != profileAtmos {
		t.Fatalf("atmos track cycle = %q", got.tracks[1].profile)
	}
}

func TestBuildDownloadJobsGroupsByProfileDeterministically(t *testing.T) {
	choices := []trackChoice{
		{track: TrackMetadata{ID: "3"}, selected: true, profile: profileAAC},
		{track: TrackMetadata{ID: "1"}, selected: true, profile: profileALAC48},
		{track: TrackMetadata{ID: "2"}, selected: false, profile: profileAtmos},
		{track: TrackMetadata{ID: "4"}, selected: true, profile: profileAAC},
	}
	want := []downloadJob{
		{profile: profileALAC48, trackIDs: []string{"1"}},
		{profile: profileAAC, trackIDs: []string{"3", "4"}},
	}
	if got := buildDownloadJobs(choices); !reflect.DeepEqual(got, want) {
		t.Fatalf("jobs = %#v, want %#v", got, want)
	}
}

func TestStageFromLinePrefersExplicitMarkersAndRecognizesFallbacks(t *testing.T) {
	tests := map[string]string{
		"[stage] validating":                 progressValidating,
		"[stage] wrapper":                    progressWrapper,
		"[stage] container":                  progressContainer,
		"[stage] download":                   progressDownload,
		"Decrypting media segment":           progressFinalize,
		"Embedding tags":                     progressFinalize,
		"=======  [complete] Completed: 3/3": progressComplete,
	}
	for line, want := range tests {
		if got := stageFromLine(line); got != want {
			t.Errorf("stageFromLine(%q) = %q, want %q", line, got, want)
		}
	}
}

func TestAppendRecentLogIsBoundedByLinesAndBytes(t *testing.T) {
	log := ""
	for _, line := range []string{"one", "two", "three", "four"} {
		log = appendRecentLog(log, line, 3, 64)
	}
	if log != "two\nthree\nfour" {
		t.Fatalf("line-bounded log = %q", log)
	}
	log = appendRecentLog("1234567890", "abcdefghij", 10, 12)
	if len(log) > 12 || !strings.HasSuffix(log, "abcdefghij") {
		t.Fatalf("byte-bounded log = %q len=%d", log, len(log))
	}
}

func TestStaleProcessLineIsIgnored(t *testing.T) {
	m := initialModel()
	m.stage = sDownload
	m.runID = 9
	m.jobIndex = 1
	m.dlLog = "current"
	next, cmd := m.Update(processLineMsg{runID: 8, jobIndex: 0, line: "stale"})
	got := next.(model)
	if cmd != nil || got.dlLog != "current" {
		t.Fatalf("stale line changed model: %#v cmd=%v", got, cmd != nil)
	}
}

func TestViewsExposeMetadataAndTrackSelection(t *testing.T) {
	m := initialModel()
	m.width = 100
	m.height = 30
	m.stage = sMetadata
	if got := m.View(); got == "" || !strings.Contains(got, "正在读取专辑曲目") {
		t.Fatalf("metadata view = %q", got)
	}
	m.stage = sTracks
	m.album = AlbumMetadata{Name: "The Long Way Home", ArtistName: "HOYO-MiX"}
	m.tracks = []trackChoice{{track: TrackMetadata{TrackNumber: 1, Name: "Chinese Version", AudioTraits: []string{"lossless"}}, selected: true, profile: profileALAC48}}
	if got := m.View(); got == "" || !strings.Contains(got, "Chinese Version") || !strings.Contains(got, "ALAC <=48 kHz") {
		t.Fatalf("track view = %q", got)
	}
}

func TestSuccessfulProcessDoneAdvancesToNextProfileGroup(t *testing.T) {
	m := initialModel()
	m.stage = sDownload
	m.runID = 12
	m.jobIndex = 0
	m.jobs = []downloadJob{{profile: profileALAC48, trackIDs: []string{"1"}}, {profile: profileAAC, trackIDs: []string{"2"}}}
	next, cmd := m.Update(processDoneMsg{runID: 12, jobIndex: 0})
	got := next.(model)
	if cmd == nil || got.stage != sDownload || got.jobIndex != 1 || got.jobs[1].profile != profileAAC {
		t.Fatalf("advance = %#v cmd=%v", got, cmd != nil)
	}
}

func TestFailedProcessDoneStopsRemainingGroups(t *testing.T) {
	m := initialModel()
	m.stage = sDownload
	m.runID = 13
	m.jobIndex = 0
	m.jobs = []downloadJob{{profile: profileALAC48, trackIDs: []string{"1"}}, {profile: profileAAC, trackIDs: []string{"2"}}}
	next, cmd := m.Update(processDoneMsg{runID: 13, jobIndex: 0, err: errors.New("exit status 1")})
	got := next.(model)
	if cmd != nil || got.stage != sDone || !got.failed || !strings.Contains(got.dlLog, "exit status 1") || got.completedJobs != 0 {
		t.Fatalf("failure = %#v cmd=%v", got, cmd != nil)
	}
}

func TestFailedLaterGroupReportsPartialCompletion(t *testing.T) {
	m := initialModel()
	m.stage = sDownload
	m.runID = 14
	m.jobIndex = 1
	m.completedJobs = 1
	m.jobs = []downloadJob{{profile: profileALAC48, trackIDs: []string{"1"}}, {profile: profileAAC, trackIDs: []string{"2"}}}
	next, _ := m.Update(processDoneMsg{runID: 14, jobIndex: 1, err: errors.New("exit status 1")})
	got := next.(model)
	view := got.View()
	if !strings.Contains(view, "1/2") || !strings.Contains(view, "AAC 256") {
		t.Fatalf("partial failure view = %q", view)
	}
}

func TestTrackSelectionViewFitsSmallCJKWindow(t *testing.T) {
	m := initialModel()
	m.stage = sTracks
	m.width = 40
	m.height = 12
	m.album = AlbumMetadata{Name: "非常非常长的中文专辑名称", ArtistName: "HOYO-MiX"}
	for i := 0; i < 8; i++ {
		m.tracks = append(m.tracks, trackChoice{track: TrackMetadata{TrackNumber: i + 1, Name: "很长很长的中文曲目名称", AudioTraits: []string{"lossless"}}, selected: true, profile: profileALAC48})
	}
	view := m.View()
	for _, line := range strings.Split(view, "\n") {
		if width := lipgloss.Width(line); width > 40 {
			t.Fatalf("line width = %d, want <= 40: %q", width, line)
		}
	}
}

func TestSplitOutputLinesHandlesNewlineAndCarriageReturn(t *testing.T) {
	data := []byte("one\rtwo\nthree")
	var got []string
	for len(data) > 0 {
		advance, token, err := splitOutputLines(data, false)
		if err != nil {
			t.Fatal(err)
		}
		if advance == 0 {
			_, token, _ = splitOutputLines(data, true)
			got = append(got, string(token))
			break
		}
		got = append(got, string(token))
		data = data[advance:]
	}
	if !reflect.DeepEqual(got, []string{"one", "two", "three"}) {
		t.Fatalf("split output = %#v", got)
	}
}

func TestEventLineWriterEmitsCompleteLinesAndFlushesTail(t *testing.T) {
	var got []string
	w := &eventLineWriter{emit: func(line string) { got = append(got, line) }}
	if _, err := w.Write([]byte("one\rtw")); err != nil {
		t.Fatal(err)
	}
	if _, err := w.Write([]byte("o\nthree")); err != nil {
		t.Fatal(err)
	}
	w.Flush()
	if !reflect.DeepEqual(got, []string{"one", "two", "three"}) {
		t.Fatalf("writer output = %#v", got)
	}
}
