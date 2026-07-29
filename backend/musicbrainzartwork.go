package backend

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
)

const (
	musicBrainzAPIBase = "https://musicbrainz.org/ws/2"
	coverArtAPIBase    = "https://coverartarchive.org"
)

// musicBrainzArtworkResolver resolves public album artwork URLs while obeying
// MusicBrainz's one-request-per-second client limit.
type musicBrainzArtworkResolver struct {
	ctx       context.Context
	client    *http.Client
	userAgent string
	mu        sync.Mutex
	lastCall  time.Time
	cache     map[string]string
	inFlight  map[string][]chan string
}

func newMusicBrainzArtworkResolver(ctx context.Context, userAgent string) *musicBrainzArtworkResolver {
	return &musicBrainzArtworkResolver{
		ctx: ctx, client: &http.Client{Timeout: 10 * time.Second}, userAgent: userAgent,
		cache: make(map[string]string), inFlight: make(map[string][]chan string),
	}
}

func (r *musicBrainzArtworkResolver) Resolve(artist, album string) string {
	artist, album = strings.TrimSpace(artist), strings.TrimSpace(album)
	if artist == "" || album == "" {
		return ""
	}
	key := strings.ToLower(artist + "\x00" + album)
	r.mu.Lock()
	if result, ok := r.cache[key]; ok {
		r.mu.Unlock()
		return result
	}
	if waiters, ok := r.inFlight[key]; ok {
		wait := make(chan string, 1)
		r.inFlight[key] = append(waiters, wait)
		r.mu.Unlock()
		select {
		case result := <-wait:
			return result
		case <-r.ctx.Done():
			return ""
		}
	}
	r.inFlight[key] = nil
	r.mu.Unlock()

	result := r.lookup(artist, album)
	r.mu.Lock()
	r.cache[key] = result
	for _, waiter := range r.inFlight[key] {
		waiter <- result
		close(waiter)
	}
	delete(r.inFlight, key)
	r.mu.Unlock()
	return result
}

func (r *musicBrainzArtworkResolver) lookup(artist, album string) string {
	if !r.waitForRateLimit() {
		return ""
	}
	query := fmt.Sprintf(`releasegroup:"%s" AND artist:"%s"`, luceneEscape(album), luceneEscape(artist))
	endpoint := musicBrainzAPIBase + "/release-group/?fmt=json&limit=1&query=" + url.QueryEscape(query)
	req, err := http.NewRequestWithContext(r.ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return ""
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", r.userAgent)
	resp, err := r.client.Do(req)
	if err != nil {
		return ""
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return ""
	}
	var result struct {
		ReleaseGroups []struct {
			ID string `json:"id"`
		} `json:"release-groups"`
	}
	if json.NewDecoder(resp.Body).Decode(&result) != nil || len(result.ReleaseGroups) == 0 {
		return ""
	}

	artURL := coverArtAPIBase + "/release-group/" + result.ReleaseGroups[0].ID + "/front-500"
	artReq, err := http.NewRequestWithContext(r.ctx, http.MethodHead, artURL, nil)
	if err != nil {
		return ""
	}
	// Validate the Cover Art Archive endpoint itself without following the
	// redirect to an archive.org image host. Some archive mirrors reject HEAD
	// even though Discord can fetch the redirected image normally.
	artClient := *r.client
	artClient.CheckRedirect = func(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse }
	artResp, err := artClient.Do(artReq)
	if err != nil {
		return ""
	}
	artResp.Body.Close()
	validRedirect := artResp.StatusCode >= 300 && artResp.StatusCode < 400 && artResp.Header.Get("Location") != ""
	if (artResp.StatusCode < 200 || artResp.StatusCode >= 300) && !validRedirect {
		return ""
	}
	return artURL
}

func (r *musicBrainzArtworkResolver) waitForRateLimit() bool {
	r.mu.Lock()
	wait := time.Until(r.lastCall.Add(time.Second))
	if wait < 0 {
		wait = 0
	}
	r.lastCall = time.Now().Add(wait)
	r.mu.Unlock()
	timer := time.NewTimer(wait)
	defer timer.Stop()
	select {
	case <-timer.C:
		return true
	case <-r.ctx.Done():
		return false
	}
}

func luceneEscape(value string) string {
	value = strings.ReplaceAll(value, `\`, `\\`)
	return strings.ReplaceAll(value, `"`, `\"`)
}
