package browsing

import "testing"

func TestPodcastDescriptionText(t *testing.T) {
	got := podcastDescriptionText(`<![CDATA[<p>Hello <em>podcast</em> &amp; listeners.</p>]]>`)
	if want := "Hello podcast & listeners."; got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}
