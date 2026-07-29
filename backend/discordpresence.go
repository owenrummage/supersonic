package backend

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"log"
	"os"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/dweymouth/supersonic/backend/mediaprovider"
	"github.com/dweymouth/supersonic/backend/player"
	"github.com/hugolgst/rich-go/ipc"
)

const discordApplicationID = "1531906238919475352"

// DiscordPresence publishes playback information to the local Discord desktop
// client. All RPC failures are intentionally non-fatal: Discord may not be
// installed or running, and that should never affect playback.
type DiscordPresence struct {
	pm       *PlaybackManager
	server   func() mediaprovider.MediaProvider
	mu       sync.Mutex
	loggedIn bool
	shareURL map[string]string
	artURL   map[string]string
	artwork  *musicBrainzArtworkResolver
}

type discordRPCActivity struct {
	Type              int                   `json:"type"`
	StatusDisplayType int                   `json:"status_display_type"`
	Details           string                `json:"details,omitempty"`
	State             string                `json:"state,omitempty"`
	Timestamps        *discordRPCTimestamps `json:"timestamps,omitempty"`
	Assets            *discordRPCAssets     `json:"assets,omitempty"`
	Buttons           []discordRPCButton    `json:"buttons,omitempty"`
}

type discordRPCTimestamps struct {
	Start int64 `json:"start,omitempty"`
	End   int64 `json:"end,omitempty"`
}

type discordRPCAssets struct {
	LargeImage string `json:"large_image,omitempty"`
	LargeText  string `json:"large_text,omitempty"`
}

type discordRPCButton struct {
	Label string `json:"label"`
	URL   string `json:"url"`
}

func NewDiscordPresence(pm *PlaybackManager, server func() mediaprovider.MediaProvider, artwork *musicBrainzArtworkResolver) *DiscordPresence {
	d := &DiscordPresence{pm: pm, server: server, shareURL: make(map[string]string), artURL: make(map[string]string), artwork: artwork}
	pm.OnSongChange(func(item mediaprovider.MediaItem, _ *mediaprovider.Track) {
		d.update()
		d.createShareURL(item)
		d.resolveArtwork(item)
	})
	pm.OnRadioMetadataChange(func(_, _, _ string) { d.update() })
	pm.OnPlaying(d.update)
	pm.OnPaused(d.update)
	pm.OnSeek(d.update)
	pm.OnStopped(d.clear)
	return d
}

func (d *DiscordPresence) loginLocked() bool {
	if d.loggedIn {
		return true
	}
	if err := ipc.OpenSocket(); err != nil {
		return false
	}
	payload, err := json.Marshal(map[string]string{"v": "1", "client_id": discordApplicationID})
	if err != nil {
		_ = ipc.CloseSocket()
		return false
	}
	ipc.Send(0, string(payload))
	d.loggedIn = true
	return true
}

func (d *DiscordPresence) update() {
	item := d.pm.NowPlaying()
	if item == nil || d.pm.PlaybackStatus().State == player.Stopped {
		d.clear()
		return
	}

	metadata := item.Metadata()
	status := d.pm.PlaybackStatus()
	activity := discordActivity(metadata, status)

	d.mu.Lock()
	defer d.mu.Unlock()
	if shareURL := d.shareURL[metadata.ID]; shareURL != "" && metadata.Type == mediaprovider.MediaItemTypeTrack {
		log.Printf("Discord share URL for %q: %s", metadata.Name, shareURL)
		activity.Buttons = []discordRPCButton{{Label: "View Song", URL: shareURL}}
	}
	if url := d.artURL[metadata.ID]; url != "" {
		activity.Assets.LargeImage = url
	}
	if d.loginLocked() {
		if err := setDiscordActivity(activity); err != nil {
			log.Printf("failed to update Discord Rich Presence: %v", err)
		}
	}
}

func (d *DiscordPresence) resolveArtwork(item mediaprovider.MediaItem) {
	if item == nil || d.artwork == nil {
		return
	}
	metadata := item.Metadata()
	if metadata.Album == "" || len(metadata.Artists) == 0 {
		return
	}
	id := metadata.ID
	d.mu.Lock()
	_, exists := d.artURL[id]
	d.mu.Unlock()
	if exists {
		return
	}
	go func() {
		artURL := d.artwork.Resolve(metadata.Artists[0], metadata.Album)
		d.mu.Lock()
		d.artURL[id] = artURL
		d.mu.Unlock()
		if artURL != "" {
			if current := d.pm.NowPlaying(); current != nil && current.Metadata().ID == id {
				d.update()
			}
		}
	}()
}

func discordActivity(metadata mediaprovider.MediaItemMetadata, status PlaybackStatus) discordRPCActivity {
	artist := strings.Join(metadata.Artists, ", ")
	state := artist
	if metadata.Album != "" {
		if state != "" {
			state += " — "
		}
		state += metadata.Album
	}
	if status.State == player.Paused {
		if state != "" {
			state += " • Paused"
		} else {
			state = "Paused"
		}
	}

	activity := discordRPCActivity{
		Type:              2, // Listening
		StatusDisplayType: 2, // Use Details (the song title) as the profile headline.
		Details:           discordText(metadata.Name),
		State:             discordText(state),
		Assets:            &discordRPCAssets{LargeText: "Listening with Supersonic"},
	}
	if status.State == player.Playing {
		started := time.Now().Add(-time.Duration(status.TimePos * float64(time.Second)))
		activity.Timestamps = &discordRPCTimestamps{Start: started.UnixMilli()}
		if metadata.Duration > 0 {
			activity.Timestamps.End = started.Add(metadata.Duration).UnixMilli()
		}
	}
	return activity
}

func setDiscordActivity(activity discordRPCActivity) error {
	nonceBytes := make([]byte, 16)
	if _, err := rand.Read(nonceBytes); err != nil {
		return err
	}
	payload, err := json.Marshal(struct {
		Command string `json:"cmd"`
		Args    struct {
			PID      int                `json:"pid"`
			Activity discordRPCActivity `json:"activity"`
		} `json:"args"`
		Nonce string `json:"nonce"`
	}{
		Command: "SET_ACTIVITY",
		Args: struct {
			PID      int                `json:"pid"`
			Activity discordRPCActivity `json:"activity"`
		}{PID: os.Getpid(), Activity: activity},
		Nonce: hex.EncodeToString(nonceBytes),
	})
	if err != nil {
		return err
	}
	ipc.Send(1, string(payload))
	return nil
}

// Discord limits details and state to 128 characters. It also rejects a
// non-empty field shorter than two characters.
func discordText(value string) string {
	value = strings.TrimSpace(value)
	if utf8.RuneCountInString(value) < 2 {
		return ""
	}
	runes := []rune(value)
	if len(runes) > 128 {
		value = string(runes[:127]) + "…"
	}
	return value
}

func (d *DiscordPresence) createShareURL(item mediaprovider.MediaItem) {
	if item == nil || item.Metadata().Type != mediaprovider.MediaItemTypeTrack {
		return
	}
	id := item.Metadata().ID
	d.mu.Lock()
	_, exists := d.shareURL[id]
	d.mu.Unlock()
	if exists {
		return
	}

	go func() {
		server := d.server()
		sharing, ok := server.(mediaprovider.SupportsSharing)
		if !ok {
			return
		}
		url, err := sharing.CreateShareURL(id)
		if err != nil {
			log.Printf("failed to create Discord download share for track: %v", err)
			return
		}
		d.mu.Lock()
		d.shareURL[id] = url.String()
		d.mu.Unlock()
		log.Printf("Created Discord share URL for %q: %s", item.Metadata().Name, url.String())
		if current := d.pm.NowPlaying(); current != nil && current.Metadata().ID == id {
			d.update()
		}
	}()
}

func (d *DiscordPresence) clear() {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.loggedIn {
		// Closing RPC clears this application's activity in Discord.
		_ = ipc.CloseSocket()
		d.loggedIn = false
	}
}

func (d *DiscordPresence) Shutdown() {
	d.clear()
}
