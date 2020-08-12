package listeners

import (
	"encoding/base64"
	"fmt"
	"image/color"
	"net/url"
	"regexp"
	"strings"
	"time"

	"github.com/bwmarrin/discordgo"
	"github.com/zekroTJA/shinpuru/internal/core/database"
	"github.com/zekroTJA/shinpuru/internal/util"
	"github.com/zekroTJA/shinpuru/pkg/colors"
	"github.com/zekroTJA/timedmap"
)

var (
	rxColorHex = regexp.MustCompile(`^#?[\dA-Fa-f]{6,8}$`)
)

type ColorListener struct {
	db         database.Database
	publicAddr string

	emojiCahce *timedmap.TimedMap
}

func NewColorListener(db database.Database, publicAddr string) *ColorListener {
	return &ColorListener{db, publicAddr, timedmap.New(1 * time.Minute)}
}

func (l *ColorListener) HandlerMessageCreate(s *discordgo.Session, e *discordgo.MessageCreate) {
	l.process(s, e.Message)
}

func (l *ColorListener) HandlerMessageEdit(s *discordgo.Session, e *discordgo.MessageUpdate) {
	l.process(s, e.Message)
}

func (l *ColorListener) HandlerMessageReaction(s *discordgo.Session, e *discordgo.MessageReactionAdd) {
	if e.MessageReaction.UserID == s.State.User.ID {
		return
	}

	if !l.emojiCahce.Contains(e.MessageID) {
		return
	}

	clr, ok := l.emojiCahce.GetValue(e.MessageID).(*color.RGBA)
	if !ok {
		return
	}

	hexClr := strings.ToUpper(colors.ToHex(clr))
	intClr := colors.ToInt(clr)
	cC, cM, cY, cK := color.RGBToCMYK(clr.R, clr.G, clr.B)
	yY, yCb, yCr := color.RGBToYCbCr(clr.R, clr.G, clr.B)

	desc := fmt.Sprintf(
		"```\n"+
			"Hex:    #%s\n"+
			"Int:    %d\n"+
			"RGBA:   %03d, %03d, %03d, %03d\n"+
			"CMYK:   %03d, %03d, %03d, %03d\n"+
			"YCbCr:  %03d, %03d, %03d\n"+
			"```",
		hexClr,
		intClr,
		clr.R, clr.G, clr.B, clr.A,
		cC, cM, cY, cK,
		yY, yCb, yCr,
	)

	emb := &discordgo.MessageEmbed{
		Color:       intClr,
		Title:       "#" + hexClr,
		Description: desc,
		Thumbnail: &discordgo.MessageEmbedThumbnail{
			URL: fmt.Sprintf("%s/api/util/color/%s?size=64", l.publicAddr, hexClr),
		},
	}

	_, err := s.ChannelMessageSendEmbed(e.ChannelID, emb)
	if err != nil {
		util.Log.Error("[ColorListener] could not send embed message:", err)
	}

	l.emojiCahce.Remove(e.MessageID)
}

func (l *ColorListener) process(s *discordgo.Session, m *discordgo.Message) {
	if len(m.Content) < 6 {
		return
	}

	matches := make([]string, 0)

	m.Content = strings.ReplaceAll(m.Content, "\n", " ")

	// Find color hex in message content using
	// predefined regex.
	for _, v := range strings.Split(m.Content, " ") {
		if rxColorHex.MatchString(v) {
			matches = append(matches, v)
		}
	}

	// Get color reaction enabled guild setting
	// and return when disabled
	active, err := l.db.GetGuildColorReaction(m.GuildID)
	if err != nil {
		util.Log.Error("[ColorListener] could not get setting from database:", err)
		return
	}
	if !active {
		return
	}

	// Execute reaction for each match
	for _, hexClr := range matches {
		l.createReaction(s, m, hexClr)
	}
}

func (l *ColorListener) createReaction(s *discordgo.Session, m *discordgo.Message, hexClr string) {
	if strings.HasPrefix(hexClr, "#") {
		hexClr = hexClr[1:]
	}

	clr, err := colors.FromHex(hexClr)
	if err != nil {
		util.Log.Error("[ColorListener] failed parsing color code:", err)
		return
	}

	buff, err := colors.CreateImage(clr, 24, 24)
	if err != nil {
		util.Log.Error("[ColorListener] failed generating image data:", err)
		return
	}

	// Encode the raw image data to a base64 string
	b64Data := base64.StdEncoding.EncodeToString(buff.Bytes())

	// Envelope the base64 data into data uri format
	dataUri := fmt.Sprintf("data:image/png;base64,%s", b64Data)

	// Upload guild emote
	emoji, err := s.GuildEmojiCreate(m.GuildID, hexClr, dataUri, nil)
	if err != nil {
		util.Log.Error("[ColorListener] failed uploading emoji:", err)
		return
	}

	// Add reaction of the uploaded emote to the message
	err = s.MessageReactionAdd(m.ChannelID, m.ID, url.QueryEscape(":"+emoji.Name+":"+emoji.ID))
	if err != nil {
		util.Log.Error("[ColorListener] failed creating message reaction:", err)
		return
	}

	l.emojiCahce.Set(m.ID, clr, 24*time.Hour)

	// Delete the uploaded emote after 5 seconds
	// to give discords caching or whatever some
	// time to save the emoji.
	time.AfterFunc(5*time.Second, func() {
		if err = s.GuildEmojiDelete(m.GuildID, emoji.ID); err != nil {
			util.Log.Error("[ColorListener] failed deleting emoji:", err)
		}
	})
}
