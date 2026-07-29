package backend

import (
	"testing"

	"github.com/dweymouth/supersonic/backend/mediaprovider"
)

func TestFetchLyricsAsyncWithNilTrack(t *testing.T) {
	lm := &LyricsManager{}
	called := false

	lm.FetchLyricsAsync(nil, func(id string, lyrics *mediaprovider.Lyrics) {
		called = true
		if id != "" || lyrics != nil {
			t.Fatalf("got callback values (%q, %v), want empty ID and nil lyrics", id, lyrics)
		}
	})
	if !called {
		t.Fatal("callback was not called")
	}
}
