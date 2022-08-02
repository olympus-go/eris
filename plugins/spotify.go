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

type authoredTrack struct {
	track      spotify.Track
	authorId   string
	authorName string
}

type SpotifyPlugin struct {
	player           *spotify.Playerr
	trackQueue       []authoredTrack
	playChan         chan spotify.Track
	queueChan        chan spotify.Track
	skipChan         chan bool
	playInteractions map[string][]spotify.Track
	isInVoice        bool
	isPlaying        bool
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
		queueChan:        make(chan spotify.Track, 100),
		playInteractions: make(map[string][]spotify.Track),
		logger:           logger.With().Str("plugin", "spotify").Logger(),
	}

	//go plugin.queueManager()

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

				s.playChan = make(chan spotify.Track)
				s.skipChan = make(chan bool)
				go s.trackPlayer()

				s.logger.Debug().Str("user_id", utils.GetInteractionUserId(i.Interaction)).Msg("user invoked spotify join")
				_ = utils.SendEphemeralInteractionResponse(session, i.Interaction, ":tada:")
			}
		default:
		}
	}

	handlers["spotify_leave_handler"] = func(session *discordgo.Session, i *discordgo.InteractionCreate) {
		switch i.Type {
		case discordgo.InteractionApplicationCommand:
			applicationCommandData := i.ApplicationCommandData()
			if applicationCommandData.Name != "spotify" || applicationCommandData.Options[0].Name != "leave" {
				return
			}

			if !s.isInVoice {
				_ = utils.SendEphemeralInteractionResponse(session, i.Interaction, "I'm not in a voice channel.")
				return
			} else {
				if s.voiceConnection == nil {
					s.logger.Error().Msg("expected to be in voice channel but connection is nil")
				}

				if err := s.voiceConnection.Disconnect(); err != nil {
					s.logger.Error().Err(err).Msg("failed to disconnect from voice channel")
					return
				}

				s.trackQueue = nil
				close(s.queueChan)
				close(s.skipChan)
				s.isInVoice = false
				s.logger.Debug().Str("user_id", utils.GetInteractionUserId(i.Interaction)).Msg("user invoked spotify leave")
				_ = utils.SendEphemeralInteractionResponse(session, i.Interaction, ":wave:")

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

			if !s.isInVoice {
				s.logger.Error().Msg("play invoked before join")
				_ = utils.SendEphemeralInteractionResponse(session, i.Interaction, "Summon me first before playing.")
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
				s.logger.Debug().Msg("failed to send interaction response")
				return
			}
			s.playInteractions[uid] = tracks
			s.logger.Debug().Str("user_id", utils.GetInteractionUserId(i.Interaction)).Msg("user invoked spotify play")

			go func() {
				time.Sleep(15 * time.Second)
				if _, ok := s.playInteractions[uid]; ok {
					delete(s.playInteractions, uid)
					s.logger.Debug().Str("uid", uid).Msg("play interaction timed out")

				}
			}()
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
			userId := utils.GetInteractionUserId(i.Interaction)

			// The interaction was already closed out
			if _, ok := s.playInteractions[uid]; !ok {
				_ = utils.SendEphemeralInteractionResponse(session, i.Interaction, "This song list is no longer available. Try searching again.")
				return
			}

			switch action {
			case "yes":
				track := s.playInteractions[uid][0]
				s.enqueueTrack(authoredTrack{track, utils.GetInteractionUserId(i.Interaction), utils.GetInteractionUserName(i.Interaction)})
				message := fmt.Sprintf("%s by %s added to queue.", track.Name(), track.Artist())
				_ = utils.SendEphemeralInteractionResponse(session, i.Interaction, message)
				delete(s.playInteractions, uid)
				s.logger.Debug().Str("user_id", userId).Str("track", track.Name()).Msg("user enqueued track")
			case "no":
				s.playInteractions[uid] = s.playInteractions[uid][1:]
				if len(s.playInteractions[uid]) == 0 {
					_ = utils.SendEphemeralInteractionResponse(session, i.Interaction, "That's all of them! Try searching again.")
					return
				}
				track := s.playInteractions[uid][0]
				message := fmt.Sprintf("Is this your song?\n```Name: %s\nArtist: %s\n```%s", track.Name(), track.Artist(), track.Image())
				_ = utils.SendEphemeralInteractionResponse(session, i.Interaction, message, s.yesNoButtons(uid, true)...)
				s.logger.Debug().Str("user_id", userId).Str("track", track.Name()).Msg("user responded no to track query")
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
			for index, aTrack := range s.trackQueue {
				if index == 0 {
					message += "Currently playing:\n"
					message += fmt.Sprintf("  %s - %s (@%s)\n", aTrack.track.Name(), aTrack.track.Artist(), aTrack.authorName)
					if len(s.trackQueue) > 1 {
						message += "Up next:\n"
					}
				} else {
					message += fmt.Sprintf("  %s - %s (@%s)\n", aTrack.track.Name(), aTrack.track.Artist(), aTrack.authorName)
				}
			}

			if message == "" {
				message = "No songs in queue"
			} else {
				message = fmt.Sprintf("```%s```", message)
			}

			s.logger.Debug().Str("user_id", utils.GetInteractionUserId(i.Interaction)).Msg("user invoked spotify queue")
			_ = utils.SendEphemeralInteractionResponse(session, i.Interaction, message)
		}
	}

	handlers["spotify_skip_handler"] = func(session *discordgo.Session, i *discordgo.InteractionCreate) {
		switch i.Type {
		case discordgo.InteractionApplicationCommand:
			applicationCommandData := i.ApplicationCommandData()
			if applicationCommandData.Name != "spotify" || applicationCommandData.Options[0].Name != "skip" {
				return
			}

			if !s.isInVoice || !s.isPlaying {
				_ = utils.SendEphemeralInteractionResponse(session, i.Interaction, "Nothing to skip.")
				return
			} else {
				userId := utils.GetInteractionUserId(i.Interaction)
				if s.trackQueue[0].authorId != userId {
					_ = utils.SendEphemeralInteractionResponse(session, i.Interaction, "You cannot skip a track you didn't queue.")
					s.logger.Debug().Str("user_id", userId).Str("author_id", s.trackQueue[0].authorId).Str("track", s.trackQueue[0].track.Name()).Msg("user attempted to skip track")
					return
				}
				s.logger.Debug().Str("user_id", userId).Str("track", s.trackQueue[0].track.Name()).Msg("user skipped track")
				s.skipChan <- true
				_ = utils.SendEphemeralInteractionResponse(session, i.Interaction, ":gun:")
			}
		default:
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
			{
				Name:        "leave",
				Description: "Requests the bot to leave the voice channel",
				Type:        discordgo.ApplicationCommandOptionSubCommand,
			},
			{
				Name:        "skip",
				Description: "Skip the currently playing song",
				Type:        discordgo.ApplicationCommandOptionSubCommand,
			},
		},
	}

	return commands
}

func (s *SpotifyPlugin) Intents() []discordgo.Intent {
	return nil
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

func (s *SpotifyPlugin) enqueueTrack(track authoredTrack) {
	s.queueLock.Lock()
	s.queueChan <- track.track
	s.trackQueue = append(s.trackQueue, track)
	s.queueLock.Unlock()
}

//func (s *SpotifyPlugin) dequeueTrack() spotify.Track {
//	if len(s.trackQueue) == 0 {
//		return spotify.Track{}
//	}
//
//	s.queueLock.Lock()
//
//	track := s.trackQueue[0]
//	s.trackQueue = s.trackQueue[1:]
//	s.queueLock.Unlock()
//
//	return track
//}

func (s *SpotifyPlugin) trackPlayer() {
	for track := range s.queueChan {
		r, err := s.player.DownloadTrack(track)
		if err != nil {
			s.logger.Error().Err(err).Str("track", track.Name()).Msg("failed to download track")
			s.queueLock.Lock()
			s.trackQueue = s.trackQueue[1:]
			s.queueLock.Unlock()
			continue
		}
		encodeSession, _ := dca.EncodeMem(r, dca.StdEncodeOptions)
		defer encodeSession.Cleanup()
		var buf bytes.Buffer
		io.Copy(&buf, encodeSession)
		decoder := dca.NewDecoder(&buf)
		s.isPlaying = true
		s.voiceConnection.Speaking(true)
	playLoop:
		for {
			select {
			case <-s.skipChan:
				break playLoop
			default:
				frame, err := decoder.OpusFrame()
				if err != nil {
					break playLoop
				}

				select {
				case s.voiceConnection.OpusSend <- frame:
				case <-time.After(time.Second):
					break playLoop
				}
			}
		}
		s.isPlaying = false
		s.voiceConnection.Speaking(false)
	}
}
