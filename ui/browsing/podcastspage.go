package browsing

import (
	"fmt"
	"html"
	"log"
	"strings"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/lang"
	"fyne.io/fyne/v2/layout"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"
	"github.com/deluan/sanitize"
	"github.com/dweymouth/supersonic/backend"
	"github.com/dweymouth/supersonic/backend/mediaprovider"
	"github.com/dweymouth/supersonic/ui/controller"
	myTheme "github.com/dweymouth/supersonic/ui/theme"
	"github.com/dweymouth/supersonic/ui/util"
	appWidgets "github.com/dweymouth/supersonic/ui/widgets"
)

type PodcastsPage struct {
	widget.BaseWidget
	contr                    *controller.Controller
	provider                 mediaprovider.PodcastProvider
	pm                       *backend.PlaybackManager
	channels                 []*mediaprovider.PodcastChannel
	episodes                 []*mediaprovider.PodcastEpisode
	channelList, episodeList *widget.List
	channelTitle             *widget.Label
	content                  *fyne.Container
}

func NewPodcastsPage(c *controller.Controller, p mediaprovider.PodcastProvider, pm *backend.PlaybackManager) *PodcastsPage {
	x := &PodcastsPage{contr: c, provider: p, pm: pm, channelTitle: widget.NewLabel(lang.L("Newest episodes"))}
	x.ExtendBaseWidget(x)
	x.channelTitle.TextStyle.Bold = true
	x.channelList = widget.NewList(func() int { return len(x.channels) }, func() fyne.CanvasObject {
		title := widget.NewButton("", nil)
		more := appWidgets.NewIconButton(theme.MoreVerticalIcon(), nil)
		more.IconSize = appWidgets.IconButtonSizeSmaller
		more.SetToolTip(lang.L("More"))
		return container.NewBorder(nil, nil, nil, more, title)
	}, func(id widget.ListItemID, o fyne.CanvasObject) {
		ch := x.channels[id]
		row := o.(*fyne.Container)
		b := row.Objects[0].(*widget.Button)
		more := row.Objects[1].(*appWidgets.IconButton)
		b.SetText(ch.Title)
		b.OnTapped = func() { go x.loadChannel(ch.ID) }
		more.OnTapped = func() {
			pos := fyne.CurrentApp().Driver().AbsolutePositionForObject(more)
			x.showChannelMenu(ch, pos)
		}
	})
	x.episodeList = widget.NewList(func() int { return len(x.episodes) }, x.newEpisodeRow, x.updateEpisodeRow)
	header := container.NewHBox(widget.NewLabelWithStyle(lang.L("Podcasts"), fyne.TextAlignLeading, fyne.TextStyle{Bold: true}), layout.NewSpacer())
	left := container.NewBorder(widget.NewLabel(lang.L("Channels")), nil, nil, nil, x.channelList)
	right := container.NewBorder(x.channelTitle, nil, nil, nil, x.episodeList)
	split := container.NewHSplit(left, right)
	split.Offset = .27
	x.content = container.NewBorder(header, nil, nil, nil, split)
	go x.load()
	return x
}

func (p *PodcastsPage) showChannelMenu(ch *mediaprovider.PodcastChannel, pos fyne.Position) {
	share := fyne.NewMenuItem(lang.L("Share")+"...", func() {
		p.contr.ShowShareDialog(ch.ID)
	})
	share.Icon = myTheme.ShareIcon
	_, canShare := p.provider.(mediaprovider.SupportsSharing)
	share.Disabled = ch.ID == "" || !canShare
	menu := fyne.NewMenu("", share)
	widget.ShowPopUpMenuAtPosition(menu, fyne.CurrentApp().Driver().CanvasForObject(p), pos)
}

func (p *PodcastsPage) newEpisodeRow() fyne.CanvasObject {
	title := widget.NewLabel("")
	title.TextStyle.Bold = true
	desc := widget.NewLabel("")
	// List rows are virtualized and have a fixed template height. Letting an
	// arbitrary feed description wrap makes it paint over following rows.
	desc.Wrapping = fyne.TextWrapOff
	desc.Truncation = fyne.TextTruncateEllipsis
	play := appWidgets.NewIconButton(theme.MediaPlayIcon(), nil)
	play.IconSize = appWidgets.IconButtonSizeSlightlyBigger
	play.SetToolTip(lang.L("Play"))
	playArea := container.NewPadded(container.NewCenter(play))
	more := appWidgets.NewIconButton(theme.MoreVerticalIcon(), nil)
	more.IconSize = appWidgets.IconButtonSizeSmaller
	more.SetToolTip(lang.L("More"))
	moreArea := container.NewPadded(container.NewCenter(more))
	texts := container.New(layout.NewCustomPaddedVBoxLayout(0), title, desc)
	return container.NewBorder(nil, nil, playArea, moreArea, texts)
}

func (p *PodcastsPage) updateEpisodeRow(id widget.ListItemID, obj fyne.CanvasObject) {
	e := p.episodes[id]
	border := obj.(*fyne.Container)
	texts := border.Objects[0].(*fyne.Container)
	playArea := border.Objects[1].(*fyne.Container)
	playCenter := playArea.Objects[0].(*fyne.Container)
	play := playCenter.Objects[0].(*appWidgets.IconButton)
	moreArea := border.Objects[2].(*fyne.Container)
	moreCenter := moreArea.Objects[0].(*fyne.Container)
	more := moreCenter.Objects[0].(*appWidgets.IconButton)
	date := ""
	if !e.PublishDate.IsZero() {
		date = e.PublishDate.Local().Format("Jan 2, 2006")
	}
	title := e.Title
	if date != "" {
		title = fmt.Sprintf("%s (%s)", title, date)
	}
	texts.Objects[0].(*widget.Label).SetText(title)
	texts.Objects[1].(*widget.Label).SetText(podcastDescriptionText(e.Description))
	play.OnTapped = func() { p.pm.LoadItems([]mediaprovider.MediaItem{e}, backend.Replace, false); p.pm.PlayTrackAt(0) }
	more.OnTapped = func() {
		pos := fyne.CurrentApp().Driver().AbsolutePositionForObject(more)
		p.showEpisodeMenu(e, pos)
	}
	if e.Playable() {
		play.Enable()
	} else {
		play.Disable()
	}
}

func (p *PodcastsPage) showEpisodeMenu(e *mediaprovider.PodcastEpisode, pos fyne.Position) {
	play := fyne.NewMenuItem(lang.L("Play"), func() {
		p.pm.LoadItems([]mediaprovider.MediaItem{e}, backend.Replace, false)
		p.pm.PlayTrackAt(0)
	})
	play.Icon = theme.MediaPlayIcon()
	playNext := fyne.NewMenuItem(lang.L("Play next"), func() {
		p.pm.LoadItems([]mediaprovider.MediaItem{e}, backend.InsertNext, false)
	})
	playNext.Icon = myTheme.PlayNextIcon
	addToQueue := fyne.NewMenuItem(lang.L("Add to queue"), func() {
		p.pm.LoadItems([]mediaprovider.MediaItem{e}, backend.Append, false)
	})
	addToQueue.Icon = theme.ContentAddIcon()
	addToPlaylist := fyne.NewMenuItem(lang.L("Add to playlist")+"...", func() {
		p.contr.DoAddTracksToPlaylistWorkflow([]string{e.StreamID})
	})
	addToPlaylist.Icon = myTheme.PlaylistIcon
	share := fyne.NewMenuItem(lang.L("Share")+"...", func() {
		p.contr.ShowShareDialog(e.StreamID)
	})
	share.Icon = myTheme.ShareIcon
	favorite := fyne.NewMenuItem(lang.L("Set favorite"), func() {
		p.contr.SetTrackFavorites([]string{e.StreamID}, true)
	})
	favorite.Icon = myTheme.FavoriteIcon
	unfavorite := fyne.NewMenuItem(lang.L("Unset favorite"), func() {
		p.contr.SetTrackFavorites([]string{e.StreamID}, false)
	})
	unfavorite.Icon = myTheme.NotFavoriteIcon
	rating := util.NewRatingSubmenu(func(value int) {
		p.contr.SetTrackRatings([]string{e.StreamID}, value)
	})

	play.Disabled = !e.Playable()
	playNext.Disabled = !e.Playable()
	addToQueue.Disabled = !e.Playable()
	mediaActionsDisabled := e.StreamID == ""
	addToPlaylist.Disabled = mediaActionsDisabled
	favorite.Disabled = mediaActionsDisabled
	unfavorite.Disabled = mediaActionsDisabled
	_, canRate := p.provider.(mediaprovider.SupportsRating)
	rating.Disabled = mediaActionsDisabled || !canRate
	_, canShare := p.provider.(mediaprovider.SupportsSharing)
	share.Disabled = mediaActionsDisabled || !canShare

	menu := fyne.NewMenu("", play, playNext, addToQueue, fyne.NewMenuItemSeparator(),
		addToPlaylist, share, fyne.NewMenuItemSeparator(), favorite, unfavorite, rating)
	widget.ShowPopUpMenuAtPosition(menu, fyne.CurrentApp().Driver().CanvasForObject(p), pos)
}

func podcastDescriptionText(description string) string {
	// Some servers return RSS descriptions still wrapped in CDATA and HTML.
	// Labels display plain text, so normalize it before rendering the preview.
	description = strings.TrimSpace(description)
	description = strings.TrimPrefix(description, "<![CDATA[")
	description = strings.TrimSuffix(description, "]]>")
	description = html.UnescapeString(sanitize.HTML(description))
	return strings.Join(strings.Fields(description), " ")
}

func (p *PodcastsPage) load() {
	if p.provider == nil {
		return
	}
	channels, err := p.provider.GetPodcastChannels()
	if err != nil {
		log.Printf("load podcast channels: %v", err)
		return
	}
	episodes, err := p.provider.GetNewestPodcastEpisodes(20)
	if err != nil {
		log.Printf("load newest podcasts: %v", err)
	}
	byID := make(map[string]*mediaprovider.PodcastChannel, len(channels))
	for _, ch := range channels {
		byID[ch.ID] = ch
	}
	for _, ep := range episodes {
		if ch := byID[ep.ChannelID]; ch != nil {
			ep.ChannelTitle = ch.Title
			if ep.CoverArtID == "" {
				ep.CoverArtID = ch.CoverArtID
			}
			ep.OriginalImageURL = ch.OriginalImageURL
		}
	}
	fyne.Do(func() {
		p.channels = channels
		p.episodes = episodes
		p.channelTitle.SetText(lang.L("Newest episodes"))
		p.channelList.Refresh()
		p.episodeList.Refresh()
	})
}
func (p *PodcastsPage) loadChannel(id string) {
	ch, err := p.provider.GetPodcastChannel(id)
	if err != nil {
		log.Printf("load podcast channel: %v", err)
		return
	}
	fyne.Do(func() { p.episodes = ch.Episodes; p.channelTitle.SetText(ch.Title); p.episodeList.Refresh() })
}
func (p *PodcastsPage) Reload()                 { go p.load() }
func (p *PodcastsPage) Route() controller.Route { return controller.PodcastsRoute() }
func (p *PodcastsPage) Save() SavedPage         { return &savedPodcastsPage{p.contr, p.provider, p.pm} }
func (p *PodcastsPage) CreateRenderer() fyne.WidgetRenderer {
	return widget.NewSimpleRenderer(p.content)
}

type savedPodcastsPage struct {
	c  *controller.Controller
	p  mediaprovider.PodcastProvider
	pm *backend.PlaybackManager
}

func (s *savedPodcastsPage) Restore() Page { return NewPodcastsPage(s.c, s.p, s.pm) }
