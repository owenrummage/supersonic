package subsonic

import (
	"encoding/json"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"time"

	"github.com/dweymouth/supersonic/backend/mediaprovider"
)

type podcastChannelDTO struct {
	ID               string              `xml:"id,attr" json:"id"`
	URL              string              `xml:"url,attr" json:"url"`
	Title            string              `xml:"title,attr" json:"title"`
	Description      string              `xml:"description,attr" json:"description"`
	CoverArt         string              `xml:"coverArt,attr" json:"coverArt"`
	OriginalImageURL string              `xml:"originalImageUrl,attr" json:"originalImageUrl"`
	Status           string              `xml:"status,attr" json:"status"`
	ErrorMessage     string              `xml:"errorMessage,attr" json:"errorMessage"`
	Episodes         []podcastEpisodeDTO `xml:"episode" json:"episode"`
}

type podcastEpisodeDTO struct {
	ID          string    `xml:"id,attr" json:"id"`
	StreamID    string    `xml:"streamId,attr" json:"streamId"`
	ChannelID   string    `xml:"channelId,attr" json:"channelId"`
	Title       string    `xml:"title,attr" json:"title"`
	Description string    `xml:"description,attr" json:"description"`
	Status      string    `xml:"status,attr" json:"status"`
	CoverArt    string    `xml:"coverArt,attr" json:"coverArt"`
	PublishDate time.Time `xml:"publishDate,attr" json:"publishDate"`
	Duration    int       `xml:"duration,attr" json:"duration"`
	Size        int64     `xml:"size,attr" json:"size"`
	BitRate     int       `xml:"bitRate,attr" json:"bitRate"`
	ContentType string    `xml:"contentType,attr" json:"contentType"`
}

type podcastAPIResponse struct {
	XMLName xml.Name `xml:"subsonic-response" json:"-"`
	Status  string   `xml:"status,attr" json:"status"`
	Error   *struct {
		Code    int    `xml:"code,attr" json:"code"`
		Message string `xml:"message,attr" json:"message"`
	} `xml:"error" json:"error"`
	Podcasts struct {
		Channels []podcastChannelDTO `xml:"channel" json:"channel"`
	} `xml:"podcasts" json:"podcasts"`
	Newest struct {
		Episodes []podcastEpisodeDTO `xml:"episode" json:"episode"`
	} `xml:"newestPodcasts" json:"newestPodcasts"`
	Episode *podcastEpisodeDTO `xml:"podcastEpisode" json:"podcastEpisode"`
}

func (s *subsonicMediaProvider) podcastRequest(endpoint string, params url.Values) (*podcastAPIResponse, error) {
	endpoint += ".view"
	resp, err := s.client.Request(http.MethodGet, endpoint, params)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("podcast API: HTTP %s", resp.Status)
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	var out podcastAPIResponse
	if s.client.UseJSON {
		var wrapper struct {
			Response podcastAPIResponse `json:"subsonic-response"`
		}
		if err = json.Unmarshal(body, &wrapper); err != nil {
			return nil, err
		}
		out = wrapper.Response
	} else if err = xml.Unmarshal(body, &out); err != nil {
		return nil, err
	}
	if out.Status != "ok" {
		if out.Error != nil {
			return nil, fmt.Errorf("podcast API error %d: %s", out.Error.Code, out.Error.Message)
		}
		return nil, fmt.Errorf("podcast API returned status %q", out.Status)
	}
	return &out, nil
}

func episodeFromDTO(e podcastEpisodeDTO, ch *mediaprovider.PodcastChannel) *mediaprovider.PodcastEpisode {
	p := &mediaprovider.PodcastEpisode{ID: e.ID, StreamID: e.StreamID, ChannelID: e.ChannelID, Title: e.Title,
		Description: e.Description, Status: e.Status, CoverArtID: e.CoverArt, PublishDate: e.PublishDate,
		Duration: time.Duration(e.Duration) * time.Second, Size: e.Size, BitRate: e.BitRate, ContentType: e.ContentType}
	if ch != nil {
		p.ChannelTitle = ch.Title
		if p.ChannelID == "" {
			p.ChannelID = ch.ID
		}
		if p.CoverArtID == "" {
			p.CoverArtID = ch.CoverArtID
		}
		p.OriginalImageURL = ch.OriginalImageURL
	}
	return p
}

func channelFromDTO(c podcastChannelDTO) *mediaprovider.PodcastChannel {
	ch := &mediaprovider.PodcastChannel{ID: c.ID, URL: c.URL, Title: c.Title, Description: c.Description,
		CoverArtID: c.CoverArt, OriginalImageURL: c.OriginalImageURL, Status: c.Status, ErrorMessage: c.ErrorMessage}
	for _, e := range c.Episodes {
		ch.Episodes = append(ch.Episodes, episodeFromDTO(e, ch))
	}
	return ch
}

func (s *subsonicMediaProvider) GetPodcastChannels() ([]*mediaprovider.PodcastChannel, error) {
	r, err := s.podcastRequest("getPodcasts", url.Values{"includeEpisodes": {"false"}})
	if err != nil {
		return nil, err
	}
	result := make([]*mediaprovider.PodcastChannel, 0, len(r.Podcasts.Channels))
	for _, c := range r.Podcasts.Channels {
		result = append(result, channelFromDTO(c))
	}
	return result, nil
}
func (s *subsonicMediaProvider) GetPodcastChannel(id string) (*mediaprovider.PodcastChannel, error) {
	r, err := s.podcastRequest("getPodcasts", url.Values{"id": {id}, "includeEpisodes": {"true"}})
	if err != nil {
		return nil, err
	}
	if len(r.Podcasts.Channels) == 0 {
		return nil, errors.New("podcast channel not found")
	}
	return channelFromDTO(r.Podcasts.Channels[0]), nil
}
func (s *subsonicMediaProvider) GetNewestPodcastEpisodes(count int) ([]*mediaprovider.PodcastEpisode, error) {
	r, err := s.podcastRequest("getNewestPodcasts", url.Values{"count": {strconv.Itoa(count)}})
	if err != nil {
		return nil, err
	}
	result := make([]*mediaprovider.PodcastEpisode, 0, len(r.Newest.Episodes))
	for _, e := range r.Newest.Episodes {
		result = append(result, episodeFromDTO(e, nil))
	}
	return result, nil
}
func (s *subsonicMediaProvider) GetPodcastEpisode(id string) (*mediaprovider.PodcastEpisode, error) {
	r, err := s.podcastRequest("getPodcastEpisode", url.Values{"id": {id}})
	if err != nil {
		return nil, err
	}
	if r.Episode == nil {
		return nil, errors.New("podcast episode not found")
	}
	return episodeFromDTO(*r.Episode, nil), nil
}
