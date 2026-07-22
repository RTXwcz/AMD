package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
)

const (
	defaultAppleMusicWebBase = "https://music.apple.com"
	defaultAppleMusicAPIBase = "https://amp-api.music.apple.com"
	defaultCatalogUserAgent  = "ez-am-downloader/catalog"
)

var publicTokenPattern = regexp.MustCompile(`eyJ[A-Za-z0-9_-]+=*(?:\.[A-Za-z0-9_-]+=*){2}`)
var indexScriptPattern = regexp.MustCompile(`(?i)<script[^>]+src=["']([^"']+\.js(?:\?[^"']*)?)["']`)
var assetIndexPattern = regexp.MustCompile(`(?i)/assets/[^"'\s>]*index[^"'\s>]*\.js(?:\?[^"'\s>]*)?`)

// AppleMusicURL is the public, non-authenticated identity extracted from an
// Apple Music link. Query is retained so callers can preserve language and
// track-selection parameters when presenting a catalog to the user.
type AppleMusicURL struct {
	Raw        string
	Storefront string
	Kind       string
	ID         string
	SongID     string
	Language   string
	Query      url.Values
}

// AppleMusicLink is kept as a descriptive alias for callers that do not need
// to distinguish a parsed URL from the URL type itself.
type AppleMusicLink = AppleMusicURL

// TrackMetadata contains the fields needed by the downloader UI and grouped
// download planner. AudioTraits comes from Apple's public catalog response.
type TrackMetadata struct {
	ID          string
	Name        string
	ArtistName  string
	AlbumName   string
	TrackNumber int
	DiscNumber  int
	AudioTraits []string
}

// AlbumMetadata is the typed subset of Apple's album catalog resource used by
// the downloader. It deliberately excludes private account/media credentials.
type AlbumMetadata struct {
	ID          string
	Name        string
	ArtistName  string
	Storefront  string
	TrackCount  int
	AudioTraits []string
	Tracks      []TrackMetadata
}

// CatalogClient accesses Apple's public web and catalog endpoints. Base URLs
// are exported so tests and local diagnostics can point to an httptest server;
// production callers should use NewCatalogClient with the defaults.
type CatalogClient struct {
	HTTPClient   *http.Client
	MusicBaseURL string
	APIBaseURL   string
	UserAgent    string
}

func NewCatalogClient(httpClient *http.Client) *CatalogClient {
	if httpClient == nil {
		httpClient = http.DefaultClient
	}
	return &CatalogClient{
		HTTPClient:   httpClient,
		MusicBaseURL: defaultAppleMusicWebBase,
		APIBaseURL:   defaultAppleMusicAPIBase,
		UserAgent:    defaultCatalogUserAgent,
	}
}

// ParseAppleMusicURL accepts public album and song URLs. A song URL in an
// album form (the usual ?i=<track-id> link) is represented as an album with
// the album ID; callers can inspect Query.Get("i") for the selected song.
func ParseAppleMusicURL(raw string) (AppleMusicURL, error) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return AppleMusicURL{}, errors.New("Apple Music URL is empty")
	}
	u, err := url.Parse(trimmed)
	if err != nil {
		return AppleMusicURL{}, fmt.Errorf("parse Apple Music URL: %w", err)
	}
	if !u.IsAbs() || !strings.EqualFold(u.Scheme, "https") {
		return AppleMusicURL{}, errors.New("Apple Music URL must use https")
	}
	host := strings.ToLower(strings.TrimSuffix(u.Hostname(), "."))
	if host != "music.apple.com" && host != "www.music.apple.com" {
		return AppleMusicURL{}, fmt.Errorf("unsupported Apple Music host %q", u.Host)
	}
	if u.User != nil || u.Port() != "" {
		return AppleMusicURL{}, errors.New("Apple Music URL must not contain user info or a port")
	}
	parts := strings.Split(strings.Trim(u.EscapedPath(), "/"), "/")
	if len(parts) < 4 {
		return AppleMusicURL{}, errors.New("Apple Music URL is missing storefront, kind, title, or ID")
	}
	storefront, err := url.PathUnescape(parts[0])
	if err != nil || !isStorefront(storefront) {
		return AppleMusicURL{}, errors.New("Apple Music URL has an invalid storefront")
	}
	kind, err := url.PathUnescape(parts[1])
	if err != nil {
		return AppleMusicURL{}, errors.New("Apple Music URL has an invalid resource kind")
	}
	kind = strings.ToLower(kind)
	if kind != "album" && kind != "song" {
		return AppleMusicURL{}, fmt.Errorf("unsupported Apple Music resource %q", kind)
	}
	id, err := url.PathUnescape(parts[len(parts)-1])
	if err != nil || !isDecimalID(id) {
		return AppleMusicURL{}, errors.New("Apple Music URL has an invalid numeric ID")
	}
	query := u.Query()
	if values := query["i"]; len(values) > 1 {
		return AppleMusicURL{}, errors.New("Apple Music URL must contain at most one i parameter")
	}
	songID := query.Get("i")
	if songID != "" && !isDecimalID(songID) {
		return AppleMusicURL{}, errors.New("Apple Music URL has an invalid song ID")
	}
	return AppleMusicURL{
		Raw:        trimmed,
		Storefront: strings.ToLower(storefront),
		Kind:       kind,
		ID:         id,
		SongID:     songID,
		Language:   query.Get("l"),
		Query:      query,
	}, nil
}

func isStorefront(value string) bool {
	if len(value) != 2 {
		return false
	}
	for _, r := range value {
		if (r < 'a' || r > 'z') && (r < 'A' || r > 'Z') {
			return false
		}
	}
	return true
}

func isDecimalID(value string) bool {
	if value == "" {
		return false
	}
	for _, r := range value {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

// FetchPublicStorefrontToken obtains Apple's public developer token from the
// storefront HTML and its hashed index JavaScript asset. This is not a media
// user token and does not require an Apple ID session.
func (c *CatalogClient) FetchPublicStorefrontToken(ctx context.Context, storefront string) (string, error) {
	if !isStorefront(storefront) {
		return "", errors.New("storefront must be a two-letter code")
	}
	c = c.withDefaults()
	root, err := resolveBase(c.MusicBaseURL, strings.ToLower(storefront))
	if err != nil {
		return "", fmt.Errorf("build storefront URL: %w", err)
	}
	body, err := c.getText(ctx, root)
	if err != nil {
		return "", fmt.Errorf("fetch storefront: %w", err)
	}
	if token := publicTokenPattern.FindString(body); token != "" {
		return token, nil
	}

	// Prefer the hashed index asset, but fall back to all script sources since
	// Apple's asset naming can change independently of the public API.
	var scripts []string
	for _, match := range assetIndexPattern.FindAllString(body, -1) {
		scripts = append(scripts, match)
	}
	for _, match := range indexScriptPattern.FindAllStringSubmatch(body, -1) {
		if len(match) > 1 {
			scripts = append(scripts, match[1])
		}
	}
	seen := make(map[string]struct{}, len(scripts))
	for _, script := range scripts {
		assetURL, err := resolveURL(root, script)
		if err != nil {
			continue
		}
		if _, ok := seen[assetURL]; ok {
			continue
		}
		seen[assetURL] = struct{}{}
		asset, err := c.getText(ctx, assetURL)
		if err != nil {
			continue
		}
		if token := publicTokenPattern.FindString(asset); token != "" {
			return token, nil
		}
	}
	return "", errors.New("public Apple Music storefront token not found")
}

// FetchAlbum fetches an album catalog resource and follows every tracks.next
// page. Passing an empty token causes the public storefront token flow above;
// passing a token is useful when callers have already cached the public token.
func (c *CatalogClient) FetchAlbum(ctx context.Context, link AppleMusicURL, token string) (AlbumMetadata, error) {
	if strings.ToLower(link.Kind) != "album" {
		return AlbumMetadata{}, errors.New("catalog fetch requires an album URL")
	}
	if !isStorefront(link.Storefront) || !isDecimalID(link.ID) {
		return AlbumMetadata{}, errors.New("album link has invalid storefront or ID")
	}
	c = c.withDefaults()
	if token == "" {
		var err error
		token, err = c.FetchPublicStorefrontToken(ctx, link.Storefront)
		if err != nil {
			return AlbumMetadata{}, err
		}
	}
	base, err := resolveBase(c.APIBaseURL, "")
	if err != nil {
		return AlbumMetadata{}, fmt.Errorf("build catalog URL: %w", err)
	}
	endpoint, err := resolveURL(base, fmt.Sprintf("v1/catalog/%s/albums/%s", strings.ToLower(link.Storefront), link.ID))
	if err != nil {
		return AlbumMetadata{}, fmt.Errorf("build album URL: %w", err)
	}
	query := url.Values{}
	query.Set("include", "tracks,artists")
	query.Set("include[songs]", "artists")
	query.Set("extend", "editorialVideo,extendedAssetUrls")
	if language := link.Language; language != "" {
		query.Set("l", language)
	}
	endpoint = appendQuery(endpoint, query)

	var envelope catalogAlbumEnvelope
	if err := c.getJSON(ctx, endpoint, token, &envelope); err != nil {
		return AlbumMetadata{}, fmt.Errorf("fetch album catalog: %w", err)
	}
	if len(envelope.Data) == 0 {
		return AlbumMetadata{}, errors.New("album catalog response contains no data")
	}
	resource := envelope.Data[0]
	metadata := AlbumMetadata{
		ID:          resource.ID,
		Name:        resource.Attributes.Name,
		ArtistName:  resource.Attributes.ArtistName,
		Storefront:  strings.ToLower(link.Storefront),
		TrackCount:  resource.Attributes.TrackCount,
		AudioTraits: append([]string(nil), resource.Attributes.AudioTraits...),
		Tracks:      tracksFrom(resource.Relationships.Tracks.Data),
	}
	if metadata.ID == "" {
		metadata.ID = link.ID
	}

	seenNext := make(map[string]struct{})
	for next := resource.Relationships.Tracks.Next; next != ""; {
		if _, seen := seenNext[next]; seen {
			return AlbumMetadata{}, fmt.Errorf("catalog pagination loop detected at %q", next)
		}
		seenNext[next] = struct{}{}
		pageURL, err := resolveURL(base, next)
		if err != nil {
			return AlbumMetadata{}, fmt.Errorf("resolve catalog pagination: %w", err)
		}
		if !sameOrigin(base, pageURL) {
			return AlbumMetadata{}, fmt.Errorf("catalog pagination origin %q is not the configured API origin", pageURL)
		}
		var page catalogTrackPage
		if err := c.getJSON(ctx, pageURL, token, &page); err != nil {
			return AlbumMetadata{}, fmt.Errorf("fetch catalog pagination: %w", err)
		}
		metadata.Tracks = append(metadata.Tracks, tracksFrom(page.Data)...)
		next = page.Next
	}
	return metadata, nil
}

// FetchAlbumByURL combines parsing, public-token discovery, and catalog
// retrieval for callers that start with pasted text.
func (c *CatalogClient) FetchAlbumByURL(ctx context.Context, rawURL string) (AlbumMetadata, error) {
	link, err := ParseAppleMusicURL(rawURL)
	if err != nil {
		return AlbumMetadata{}, err
	}
	return c.FetchAlbum(ctx, link, "")
}

// FetchAlbumCatalog is a convenience wrapper using Apple's production
// endpoints and the default HTTP client.
func FetchAlbumCatalog(ctx context.Context, rawURL string) (AlbumMetadata, error) {
	return NewCatalogClient(nil).FetchAlbumByURL(ctx, rawURL)
}

func (c *CatalogClient) withDefaults() *CatalogClient {
	copy := *c
	if copy.HTTPClient == nil {
		copy.HTTPClient = http.DefaultClient
	}
	if strings.TrimSpace(copy.MusicBaseURL) == "" {
		copy.MusicBaseURL = defaultAppleMusicWebBase
	}
	if strings.TrimSpace(copy.APIBaseURL) == "" {
		copy.APIBaseURL = defaultAppleMusicAPIBase
	}
	if strings.TrimSpace(copy.UserAgent) == "" {
		copy.UserAgent = defaultCatalogUserAgent
	}
	return &copy
}

func (c *CatalogClient) getText(ctx context.Context, endpoint string) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("User-Agent", c.UserAgent)
	req.Header.Set("Accept", "text/html,application/javascript;q=0.9,*/*;q=0.8")
	resp, err := c.HTTPClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		return "", fmt.Errorf("unexpected HTTP status %s", resp.Status)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if err != nil {
		return "", err
	}
	return string(body), nil
}

func (c *CatalogClient) getJSON(ctx context.Context, endpoint, token string, target any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return err
	}
	req.Header.Set("User-Agent", c.UserAgent)
	req.Header.Set("Origin", defaultAppleMusicWebBase)
	req.Header.Set("Accept", "application/json")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := c.HTTPClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		return fmt.Errorf("unexpected HTTP status %s", resp.Status)
	}
	decoder := json.NewDecoder(io.LimitReader(resp.Body, 8<<20))
	if err := decoder.Decode(target); err != nil {
		return err
	}
	return nil
}

func resolveBase(base, path string) (string, error) {
	baseURL, err := url.Parse(strings.TrimRight(strings.TrimSpace(base), "/") + "/")
	if err != nil || !baseURL.IsAbs() || baseURL.Host == "" {
		if err == nil {
			err = errors.New("base URL must be absolute")
		}
		return "", err
	}
	return resolveURL(baseURL.String(), path)
}

func resolveURL(base, reference string) (string, error) {
	baseURL, err := url.Parse(base)
	if err != nil {
		return "", err
	}
	refURL, err := url.Parse(reference)
	if err != nil {
		return "", err
	}
	if refURL.IsAbs() {
		return refURL.String(), nil
	}
	return baseURL.ResolveReference(refURL).String(), nil
}

func sameOrigin(left, right string) bool {
	a, err := url.Parse(left)
	if err != nil {
		return false
	}
	b, err := url.Parse(right)
	if err != nil || !a.IsAbs() || !b.IsAbs() {
		return false
	}
	if !strings.EqualFold(a.Scheme, b.Scheme) || !strings.EqualFold(a.Hostname(), b.Hostname()) {
		return false
	}
	return effectivePort(a) == effectivePort(b)
}

func effectivePort(value *url.URL) string {
	if port := value.Port(); port != "" {
		return port
	}
	switch strings.ToLower(value.Scheme) {
	case "http":
		return "80"
	case "https":
		return "443"
	default:
		return ""
	}
}

func appendQuery(endpoint string, query url.Values) string {
	u, err := url.Parse(endpoint)
	if err != nil {
		return endpoint
	}
	u.RawQuery = query.Encode()
	return u.String()
}

type catalogAlbumEnvelope struct {
	Data []catalogAlbumResource `json:"data"`
}

type catalogAlbumResource struct {
	ID            string                 `json:"id"`
	Attributes    catalogAlbumAttributes `json:"attributes"`
	Relationships struct {
		Tracks catalogTrackPage `json:"tracks"`
	} `json:"relationships"`
}

type catalogAlbumAttributes struct {
	Name        string   `json:"name"`
	ArtistName  string   `json:"artistName"`
	TrackCount  int      `json:"trackCount"`
	AudioTraits []string `json:"audioTraits"`
}

type catalogTrackPage struct {
	Data []catalogTrackResource `json:"data"`
	Next string                 `json:"next"`
}

type catalogTrackResource struct {
	ID         string                 `json:"id"`
	Attributes catalogTrackAttributes `json:"attributes"`
}

type catalogTrackAttributes struct {
	Name        string   `json:"name"`
	ArtistName  string   `json:"artistName"`
	AlbumName   string   `json:"albumName"`
	TrackNumber int      `json:"trackNumber"`
	DiscNumber  int      `json:"discNumber"`
	AudioTraits []string `json:"audioTraits"`
}

func tracksFrom(resources []catalogTrackResource) []TrackMetadata {
	tracks := make([]TrackMetadata, 0, len(resources))
	for _, resource := range resources {
		tracks = append(tracks, TrackMetadata{
			ID:          resource.ID,
			Name:        resource.Attributes.Name,
			ArtistName:  resource.Attributes.ArtistName,
			AlbumName:   resource.Attributes.AlbumName,
			TrackNumber: resource.Attributes.TrackNumber,
			DiscNumber:  resource.Attributes.DiscNumber,
			AudioTraits: append([]string(nil), resource.Attributes.AudioTraits...),
		})
	}
	return tracks
}
