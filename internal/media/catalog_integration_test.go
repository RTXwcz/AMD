package media

import (
	"context"
	"os"
	"testing"
	"time"
)

func TestLiveTargetCatalog(t *testing.T) {
	if os.Getenv("EZ_LIVE_CATALOG") != "1" {
		t.Skip("set EZ_LIVE_CATALOG=1 for the live Apple Music catalog check")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	album, err := FetchAlbumCatalog(ctx, "https://music.apple.com/us/album/genshin-impact-the-long-way-home-the-genshin-impact/1842050720?l=zh-Hans-CN")
	if err != nil {
		t.Fatal(err)
	}
	if album.ID != "1842050720" || len(album.Tracks) != 3 {
		t.Fatalf("album = %#v", album)
	}
	for _, track := range album.Tracks {
		t.Logf("track %02d: %s (%s)", track.TrackNumber, track.Name, track.ID)
		if track.ID == "" || track.Name == "" {
			t.Fatalf("incomplete track metadata: %#v", track)
		}
		if hasAudioTrait(track, "atmos") {
			t.Fatalf("target track unexpectedly advertises Atmos: %#v", track)
		}
		if got := defaultProfile(track); got != profileALAC48 {
			t.Fatalf("default profile for %q = %q, want %q", track.Name, got, profileALAC48)
		}
	}
}
