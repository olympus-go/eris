package plugins

import (
	"bytes"
	"encoding/base64"
	"fmt"
	"github.com/bwmarrin/discordgo"
	"github.com/eolso/apollo/spotify"
	"github.com/eolso/eris"
	"github.com/eolso/eris/utils"
	"github.com/jonas747/dca"
	"github.com/rs/zerolog"
	"io"
	"strings"
	"sync"
	"time"
)

type SpotifyPlugin struct {
	player           *spotify.Playerr
	trackQueue       []spotify.Track
	playChan         chan spotify.Track
	playInteractions map[string][]spotify.Track
	isInVoice        bool
	voiceConnection  *discordgo.VoiceConnection
	logger           zerolog.Logger

	queueLock sync.RWMutex
}

func Spotify(logger zerolog.Logger) *SpotifyPlugin {
	player := spotify.NewPlayerr(spotify.DefaultPlayerConfig())

	if err := player.Login(); err != nil {
		return nil
	}

	plugin := SpotifyPlugin{
		player:           player,
		playChan:         make(chan spotify.Track),
		playInteractions: make(map[string][]spotify.Track),
		logger:           logger.With().Str("plugin", "spotify").Logger(),
	}

	go plugin.queueManager()
	go plugin.trackPlayer()

	return &plugin
}

func (s *SpotifyPlugin) Name() string {
	return "Spotify"
}

func (s *SpotifyPlugin) Description() string {
	return "Play spotify songs in voice chats"
}

func (s *SpotifyPlugin) Handlers() map[string]any {
	handlers := make(map[string]any)

	handlers["spotify_join_handler"] = func(session *discordgo.Session, i *discordgo.InteractionCreate) {
		switch i.Type {
		case discordgo.InteractionApplicationCommand:
			applicationCommandData := i.ApplicationCommandData()
			if applicationCommandData.Name != "spotify" || applicationCommandData.Options[0].Name != "join" {
				return
			}

			if s.isInVoice {
				_ = utils.SendEphemeralInteractionResponse(session, i.Interaction, "I'm already here!")
				return
			} else {
				guild, err := session.State.Guild(i.GuildID)
				if err != nil {
					s.logger.Error().Err(err).Msg("failed to fetch guild from id")
					_ = utils.SendEphemeralInteractionResponse(session, i.Interaction, "Something went wrong")
					return
				}

				for _, voiceState := range guild.VoiceStates {
					if i.Member != nil && i.Member.User.ID == voiceState.UserID {
						s.voiceConnection, err = session.ChannelVoiceJoin(guild.ID, voiceState.ChannelID, false, true)
						if err != nil {
							s.logger.Error().Err(err).Msg("failed to join voice channel")
							_ = utils.SendEphemeralInteractionResponse(session, i.Interaction, "Something went wrong")
							return
						}
						s.isInVoice = true
					}
				}

				// The invoking user could not be found in a voice channel
				if !s.isInVoice {
					_ = utils.SendEphemeralInteractionResponse(session, i.Interaction, "You're not in a voice channel")
					return
				}

				_ = utils.SendEphemeralInteractionResponse(session, i.Interaction, ":tada:")
			}
		default:
		}
	}

	handlers["spotify_play_handler"] = func(session *discordgo.Session, i *discordgo.InteractionCreate) {
		switch i.Type {
		case discordgo.InteractionApplicationCommand:
			applicationCommandData := i.ApplicationCommandData()
			if !eris.CheckApplicationCommandData(applicationCommandData, "spotify", "play") {
				return
			}

			// This shouldn't ever happen, but we don't want to risk going out of bounds on that assumption
			if len(applicationCommandData.Options) == 0 || len(applicationCommandData.Options[0].Options) == 0 {
				s.logger.Error().Str("command", "spotify play").Msg("unexpected empty options for command")
				_ = utils.SendEphemeralInteractionResponse(session, i.Interaction, "Something went wrong")
				return
			}

			query, _ := applicationCommandData.Options[0].Options[0].Value.(string)

			tracks, err := s.player.Search(query, 5)
			if err != nil {
				_ = utils.SendEphemeralInteractionResponse(session, i.Interaction, "Something went wrong")
				s.logger.Error().Err(err).Msg("spotify search failed")
				return
			}

			// No tracks found
			if len(tracks) == 0 {
				_ = utils.SendEphemeralInteractionResponse(session, i.Interaction, "No tracks found")
				return
			}

			// Send the initial track (with the possibility of more to come)
			message := fmt.Sprintf("Is this your song?\n```Name: %s\nArtist: %s\n```%s", tracks[0].Name(), tracks[0].Artist(), tracks[0].Image())
			uid := base64.StdEncoding.EncodeToString([]byte(fmt.Sprintf("%s%d", query, time.Now().UnixNano())))
			if len(uid) > 64 {
				uid = uid[:64]
			}
			if err = utils.SendEphemeralInteractionResponse(session, i.Interaction, message, s.yesNoButtons(uid, true)...); err != nil {
				s.logger.Error().Err(err).Msg("failed to send interaction response")
				return
			}
			s.playInteractions[uid] = tracks
		case discordgo.InteractionMessageComponent:
			messageComponentData := i.MessageComponentData()
			if !strings.HasPrefix(messageComponentData.CustomID, "spotify_play") {
				return
			}

			idSplit := strings.Split(messageComponentData.CustomID, "_")
			if len(idSplit) != 4 {
				s.logger.Error().Str("custom_id", messageComponentData.CustomID).Msg("message interaction response had an unknown custom id")
				return
			}

			action := idSplit[2]
			uid := idSplit[3]

			// The interaction was already closed out
			if _, ok := s.playInteractions[uid]; !ok {
				_ = utils.SendEphemeralInteractionResponse(session, i.Interaction, "This song list is no longer available.")
				return
			}

			switch action {
			case "yes":
				track := s.playInteractions[uid][0]
				s.enqueueTrack(track)
				message := fmt.Sprintf("%s by %s added to queue.", track.Name(), track.Artist())
				_ = utils.SendEphemeralInteractionResponse(session, i.Interaction, message)
				//s.songQueue <- track
				delete(s.playInteractions, uid)
			case "no":
				s.playInteractions[uid] = s.playInteractions[uid][1:]
				if len(s.playInteractions[uid]) == 0 {
					_ = utils.SendEphemeralInteractionResponse(session, i.Interaction, "That's all of them! Try searching again.")
					return
				}
				track := s.playInteractions[uid][0]
				message := fmt.Sprintf("Is this your song?\n```Name: %s\nArtist: %s\n```%s", track.Name(), track.Artist(), track.Image())
				_ = utils.SendEphemeralInteractionResponse(session, i.Interaction, message, s.yesNoButtons(uid, true)...)
			}
		}
	}

	handlers["spotify_queue_handler"] = func(session *discordgo.Session, i *discordgo.InteractionCreate) {
		switch i.Type {
		case discordgo.InteractionApplicationCommand:
			applicationCommandData := i.ApplicationCommandData()
			if !eris.CheckApplicationCommandData(applicationCommandData, "spotify", "queue") {
				return
			}

			message := ""
			for _, track := range s.trackQueue {
				message += fmt.Sprintf("%s - %s\n", track.Name(), track.Artist())
			}

			if message == "" {
				message = "No songs in queue"
			} else {
				message = fmt.Sprintf("```%s```", message)
			}

			_ = utils.SendEphemeralInteractionResponse(session, i.Interaction, message)
		}
	}

	return handlers
}

func (s *SpotifyPlugin) Commands() map[string]*discordgo.ApplicationCommand {
	commands := make(map[string]*discordgo.ApplicationCommand)

	commands["spotify_cmd"] = &discordgo.ApplicationCommand{
		Name:        "spotify",
		Description: "Spotify discord connector",
		Options: []*discordgo.ApplicationCommandOption{
			{
				Name:        "play",
				Description: "Plays a specified song",
				Options: []*discordgo.ApplicationCommandOption{
					{
						Name:        "query",
						Description: "Search query or spotify url",
						Type:        discordgo.ApplicationCommandOptionString,
						Required:    true,
					},
					{
						Name:        "limit",
						Description: "Limit the number of search results prompted",
						Type:        discordgo.ApplicationCommandOptionInteger,
						Required:    false,
					},
				},
				Type: discordgo.ApplicationCommandOptionSubCommand,
			},
			{
				Name:        "queue",
				Description: "Shows the current song queue",
				Type:        discordgo.ApplicationCommandOptionSubCommand,
			},
			{
				Name:        "join",
				Description: "Requests the bot to join your voice channel",
				Type:        discordgo.ApplicationCommandOptionSubCommand,
			},
		},
	}

	return commands
}

func (s *SpotifyPlugin) Intents() []discordgo.Intent {
	return nil
}

func (s *SpotifyPlugin) parsePlayOptions(options ...*discordgo.ApplicationCommandInteractionDataOption) (song string, artist string, album string) {
	for _, option := range options {
		switch option.Name {
		case "name":
			song, _ = option.Value.(string)
		case "artist":
			artist, _ = option.Value.(string)
		case "album":
			album, _ = option.Value.(string)
		}
	}

	return song, artist, album
}

func (s *SpotifyPlugin) yesNoButtons(uid string, enabled bool) []discordgo.MessageComponent {
	return []discordgo.MessageComponent{
		discordgo.ActionsRow{
			Components: []discordgo.MessageComponent{
				discordgo.Button{
					Label:    "Yes",
					Style:    discordgo.PrimaryButton,
					CustomID: "spotify_play_yes_" + uid,
					Disabled: !enabled,
				},
				discordgo.Button{
					Label:    "No",
					Style:    discordgo.SecondaryButton,
					CustomID: "spotify_play_no_" + uid,
					Disabled: !enabled,
				},
			},
		},
	}
}

func (s *SpotifyPlugin) enqueueTrack(track spotify.Track) {
	s.queueLock.Lock()
	s.trackQueue = append(s.trackQueue, track)
	s.queueLock.Unlock()
}

func (s *SpotifyPlugin) dequeueTrack() spotify.Track {
	if len(s.trackQueue) == 0 {
		return spotify.Track{}
	}

	s.queueLock.Lock()

	track := s.trackQueue[0]
	s.trackQueue = s.trackQueue[1:]
	s.queueLock.Unlock()

	return track
}

func (s *SpotifyPlugin) queueManager() {
	for {
		if len(s.trackQueue) == 0 {
			time.Sleep(1 * time.Second)
			continue
		}
		s.playChan <- s.dequeueTrack()
	}
}

func (s *SpotifyPlugin) trackPlayer() {
	for song := range s.playChan {
		r, _ := s.player.DownloadTrack(song)
		encodeSession, _ := dca.EncodeMem(r, dca.StdEncodeOptions)
		defer encodeSession.Cleanup()
		var buf bytes.Buffer
		io.Copy(&buf, encodeSession)
		decoder := dca.NewDecoder(&buf)
		s.voiceConnection.Speaking(true)
		for {
			frame, err := decoder.OpusFrame()
			if err != nil {
				if err != io.EOF {
				}
				break
			}

			select {
			case s.voiceConnection.OpusSend <- frame:
			case <-time.After(time.Second):
				return
			}
		}
		s.voiceConnection.Speaking(false)
	}
}
