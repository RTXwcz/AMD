package media

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
)

func TestParseAppleMusicURLPreservesCatalogParameters(t *testing.T) {
	raw := "https://music.apple.com/US/album/genshin-impact/1842050720?l=zh-Hans-CN&app=music&i=1842050721"

	got, err := ParseAppleMusicURL(raw)
	if err != nil {
		t.Fatal(err)
	}
	if got.Storefront != "us" || got.Kind != "album" || got.ID != "1842050720" {
		t.Fatalf("parsed identity = %#v", got)
	}
	if got.Language != "zh-Hans-CN" {
		t.Fatalf("language = %q, want zh-Hans-CN", got.Language)
	}
	if got.SongID != "1842050721" {
		t.Fatalf("song ID = %q, want 1842050721", got.SongID)
	}
	if got.Query.Get("app") != "music" || got.Query.Get("l") != "zh-Hans-CN" {
		t.Fatalf("query = %#v", got.Query)
	}
	if got.Raw != raw {
		t.Fatalf("raw URL = %q, want %q", got.Raw, raw)
	}
}

func TestParseAppleMusicURLRejectsNonAppleAndMalformedAlbumURLs(t *testing.T) {
	for _, raw := range []string{
		"https://example.com/us/album/title/1842050720",
		"https://music.apple.com.evil.example/us/album/title/1842050720",
		"http://music.apple.com/us/album/title/1842050720",
		"https://music.apple.com/us/artist/title/1842050720",
		"https://music.apple.com/usa/album/title/1842050720",
		"https://music.apple.com/us/album/title/not-a-number",
		"https://music.apple.com/us/album/title/",
		"https://music.apple.com/us/album/title/1842050720?i=not-a-number",
		"https://music.apple.com:443/us/album/title/1842050720",
	} {
		t.Run(raw, func(t *testing.T) {
			if _, err := ParseAppleMusicURL(raw); err == nil {
				t.Fatalf("ParseAppleMusicURL(%q) succeeded", raw)
			}
		})
	}
}

func TestCatalogClientFetchesPublicStorefrontToken(t *testing.T) {
	const token = "eyJhbGciOiJFUzI1NiJ9.eyJpc3MiOiJhcHBsZSJ9.signature_123"
	var requested []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requested = append(requested, r.URL.Path)
		switch r.URL.Path {
		case "/us":
			fmt.Fprint(w, `<html><script type="module" src="/assets/index~catalog.js"></script></html>`)
		case "/assets/index~catalog.js":
			fmt.Fprintf(w, `window.__developerToken=%q`, token)
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	client := NewCatalogClient(server.Client())
	client.MusicBaseURL = server.URL
	got, err := client.FetchPublicStorefrontToken(context.Background(), "US")
	if err != nil {
		t.Fatal(err)
	}
	if got != token {
		t.Fatalf("token = %q, want public fixture token", got)
	}
	if !reflect.DeepEqual(requested, []string{"/us", "/assets/index~catalog.js"}) {
		t.Fatalf("requested paths = %#v", requested)
	}
}

func TestCatalogClientFetchAlbumDecodesTracksAndPagination(t *testing.T) {
	const token = "public-token"
	var requests int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		if got := r.Header.Get("Authorization"); got != "Bearer "+token {
			t.Errorf("authorization = %q", got)
		}
		if got := r.Header.Get("Origin"); got != "https://music.apple.com" {
			t.Errorf("origin = %q", got)
		}
		switch r.URL.Path {
		case "/v1/catalog/us/albums/1842050720":
			if got := r.URL.Query().Get("l"); got != "zh-Hans-CN" {
				t.Errorf("language query = %q", got)
			}
			if got := r.URL.Query().Get("include"); !strings.Contains(got, "tracks") {
				t.Errorf("include = %q", got)
			}
			fmt.Fprint(w, `{
			  "data": [{
			    "id": "1842050720",
			    "attributes": {
			      "name": "Genshin Impact - The Long Way Home",
			      "artistName": "HOYO-MiX",
			      "trackCount": 3,
			      "audioTraits": ["lossless", "hi-res-lossless"]
			    },
			    "relationships": {"tracks": {
			      "data": [{"id":"1842050721","type":"songs","attributes":{"name":"The Long Way Home","artistName":"HOYO-MiX","trackNumber":1,"discNumber":1,"audioTraits":["lossless","hi-res-lossless"]}}],
			      "next": "/v1/catalog/us/albums/1842050720/tracks?offset=1"
			    }}
			  }]
			}`)
		case "/v1/catalog/us/albums/1842050720/tracks":
			if got := r.URL.Query().Get("offset"); got != "1" {
				t.Errorf("pagination offset = %q", got)
			}
			fmt.Fprint(w, `{
			  "data": [
			    {"id":"1842050722","type":"songs","attributes":{"name":"Wind and Fire","artistName":"HOYO-MiX","trackNumber":2,"discNumber":1,"audioTraits":["lossless"]}},
			    {"id":"1842050723","type":"songs","attributes":{"name":"A New Journey","artistName":"HOYO-MiX","trackNumber":3,"discNumber":1,"audioTraits":["lossless","atmos"]}}
			  ]
			}`)
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	link, err := ParseAppleMusicURL("https://music.apple.com/us/album/genshin-impact/1842050720?l=zh-Hans-CN")
	if err != nil {
		t.Fatal(err)
	}
	client := NewCatalogClient(server.Client())
	client.APIBaseURL = server.URL
	album, err := client.FetchAlbum(context.Background(), link, token)
	if err != nil {
		t.Fatal(err)
	}
	if requests != 2 {
		t.Fatalf("request count = %d, want 2", requests)
	}
	if album.ID != "1842050720" || album.Name != "Genshin Impact - The Long Way Home" || album.ArtistName != "HOYO-MiX" || album.TrackCount != 3 {
		t.Fatalf("album = %#v", album)
	}
	if !reflect.DeepEqual(album.AudioTraits, []string{"lossless", "hi-res-lossless"}) {
		t.Fatalf("album traits = %#v", album.AudioTraits)
	}
	wantTracks := []TrackMetadata{
		{ID: "1842050721", Name: "The Long Way Home", ArtistName: "HOYO-MiX", TrackNumber: 1, DiscNumber: 1, AudioTraits: []string{"lossless", "hi-res-lossless"}},
		{ID: "1842050722", Name: "Wind and Fire", ArtistName: "HOYO-MiX", TrackNumber: 2, DiscNumber: 1, AudioTraits: []string{"lossless"}},
		{ID: "1842050723", Name: "A New Journey", ArtistName: "HOYO-MiX", TrackNumber: 3, DiscNumber: 1, AudioTraits: []string{"lossless", "atmos"}},
	}
	if !reflect.DeepEqual(album.Tracks, wantTracks) {
		t.Fatalf("tracks = %#v, want %#v", album.Tracks, wantTracks)
	}
}

func TestCatalogClientFetchAlbumObtainsPublicTokenWhenEmpty(t *testing.T) {
	const token = "eyJhbGciOiJFUzI1NiJ9.eyJpc3MiOiJhcHBsZSJ9.signature"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/us":
			fmt.Fprint(w, `<script src="/assets/index.js"></script>`)
		case "/assets/index.js":
			fmt.Fprint(w, token)
		case "/v1/catalog/us/albums/1":
			if r.Header.Get("Authorization") != "Bearer "+token {
				t.Fatalf("album request did not use fetched public token")
			}
			fmt.Fprint(w, `{"data":[{"id":"1","attributes":{"name":"A"},"relationships":{"tracks":{"data":[]}}}]}`)
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	client := NewCatalogClient(server.Client())
	client.MusicBaseURL = server.URL
	client.APIBaseURL = server.URL
	album, err := client.FetchAlbum(context.Background(), AppleMusicURL{Storefront: "us", Kind: "album", ID: "1"}, "")
	if err != nil {
		t.Fatal(err)
	}
	if album.Name != "A" {
		t.Fatalf("album = %#v", album)
	}
}

func TestCatalogClientRejectsMalformedResponsesAndPaginationLoops(t *testing.T) {
	t.Run("empty album", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			fmt.Fprint(w, `{"data":[]}`)
		}))
		defer server.Close()
		client := NewCatalogClient(server.Client())
		client.APIBaseURL = server.URL
		_, err := client.FetchAlbum(context.Background(), AppleMusicURL{Storefront: "us", Kind: "album", ID: "1"}, "token")
		if err == nil {
			t.Fatal("empty album response succeeded")
		}
	})

	t.Run("pagination loop", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path == "/v1/catalog/us/albums/1" {
				fmt.Fprint(w, `{"data":[{"id":"1","attributes":{"name":"A"},"relationships":{"tracks":{"data":[],"next":"/loop"}}}]}`)
				return
			}
			fmt.Fprint(w, `{"data":[],"next":"/loop"}`)
		}))
		defer server.Close()
		client := NewCatalogClient(server.Client())
		client.APIBaseURL = server.URL
		_, err := client.FetchAlbum(context.Background(), AppleMusicURL{Storefront: "us", Kind: "album", ID: "1"}, "token")
		if err == nil || !strings.Contains(err.Error(), "pagination") {
			t.Fatalf("loop error = %v", err)
		}
	})

	t.Run("cross-origin pagination", func(t *testing.T) {
		foreignHits := 0
		foreign := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			foreignHits++
			fmt.Fprint(w, `{"data":[]}`)
		}))
		defer foreign.Close()
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			fmt.Fprintf(w, `{"data":[{"id":"1","attributes":{"name":"A"},"relationships":{"tracks":{"data":[],"next":%q}}}]}`, foreign.URL+"/steal")
		}))
		defer server.Close()
		client := NewCatalogClient(server.Client())
		client.APIBaseURL = server.URL
		_, err := client.FetchAlbum(context.Background(), AppleMusicURL{Storefront: "us", Kind: "album", ID: "1"}, "token")
		if err == nil || !strings.Contains(err.Error(), "origin") {
			t.Fatalf("cross-origin error = %v", err)
		}
		if foreignHits != 0 {
			t.Fatalf("public token was forwarded to a foreign origin")
		}
	})
}
