package plugins

import (
	"context"
	"encoding/base64"
	"fmt"
	"github.com/bwmarrin/discordgo"
	"github.com/jonas747/dca"
	"github.com/olympus-go/apollo/spotify"
	"github.com/olympus-go/eris/utils"
	"github.com/rs/zerolog"
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
	player            *spotify.Player
	trackQueue        []authoredTrack
	framesProcessed   int
	playChan          chan spotify.Track
	queueChan         chan spotify.Track
	skipChan          chan bool
	trackPlayerCancel func()
	playInteractions  map[string][]spotify.Track
	isInVoice         bool
	isPlaying         bool
	voiceConnection   *discordgo.VoiceConnection
	logger            zerolog.Logger

	queueLock sync.RWMutex
}

func Spotify(logger zerolog.Logger) *SpotifyPlugin {
	player := spotify.NewPlayer(spotify.DefaultPlayerConfig())

	if err := player.Login(); err != nil {
		return nil
	}

	plugin := SpotifyPlugin{
		player:           player,
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

			if _, ok := utils.GetApplicationCommandOption(applicationCommandData, "spotify", "join"); !ok {
				return
			}

			s.logger.Debug().Str("user_id", utils.GetInteractionUserId(i.Interaction)).
				Interface("command", applicationCommandData).Msg("user invoked slash command")

			if s.isInVoice {
				utils.InteractionResponse(session, i.Interaction).Flags(discordgo.MessageFlagsEphemeral).
					Message("I'm already here!").SendWithLog(s.logger)
				return
			} else {
				guild, err := session.State.Guild(i.GuildID)
				if err != nil {
					s.logger.Error().Err(err).Msg("failed to fetch guild from id")
					utils.InteractionResponse(session, i.Interaction).Flags(discordgo.MessageFlagsEphemeral).
						Message("Something went wrong.").SendWithLog(s.logger)
					return
				}

				for _, voiceState := range guild.VoiceStates {
					if i.Member != nil && i.Member.User.ID == voiceState.UserID {
						s.voiceConnection, err = session.ChannelVoiceJoin(guild.ID, voiceState.ChannelID, false, true)
						if err != nil {
							s.logger.Error().Err(err).Msg("failed to join voice channel")
							utils.InteractionResponse(session, i.Interaction).Flags(discordgo.MessageFlagsEphemeral).
								Message("Something went wrong.").SendWithLog(s.logger)
							return
						}
						s.isInVoice = true
					}
				}

				// The invoking user could not be found in a voice channel
				if !s.isInVoice {
					utils.InteractionResponse(session, i.Interaction).Flags(discordgo.MessageFlagsEphemeral).
						Message("You're not in a voice channel.").SendWithLog(s.logger)
					return
				}

				s.queueChan = make(chan spotify.Track, 100)
				s.playChan = make(chan spotify.Track)
				s.skipChan = make(chan bool)
				var ctx context.Context
				ctx, s.trackPlayerCancel = context.WithCancel(context.Background())
				go s.trackPlayer(ctx)

				utils.InteractionResponse(session, i.Interaction).Flags(discordgo.MessageFlagsEphemeral).
					Message(":tada:").SendWithLog(s.logger)
			}
		}
	}

	handlers["spotify_leave_handler"] = func(session *discordgo.Session, i *discordgo.InteractionCreate) {
		switch i.Type {
		case discordgo.InteractionApplicationCommand:
			applicationCommandData := i.ApplicationCommandData()
			if _, ok := utils.GetApplicationCommandOption(applicationCommandData, "spotify", "leave"); !ok {
				return
			}

			s.logger.Debug().Str("user_id", utils.GetInteractionUserId(i.Interaction)).
				Interface("command", applicationCommandData).Msg("user invoked slash command")

			if !s.isInVoice {
				utils.InteractionResponse(session, i.Interaction).Flags(discordgo.MessageFlagsEphemeral).
					Message("I'm not in a voice channel").SendWithLog(s.logger)
				return
			} else {
				if s.voiceConnection == nil {
					s.logger.Error().Msg("expected to be in voice channel but connection is nil")
					utils.InteractionResponse(session, i.Interaction).Flags(discordgo.MessageFlagsEphemeral).
						Message("Something went wrong.").SendWithLog(s.logger)
					return
				}

				s.trackPlayerCancel()
				s.trackQueue = nil
				close(s.queueChan)
				close(s.skipChan)

				if err := s.voiceConnection.Disconnect(); err != nil {
					s.logger.Error().Err(err).Msg("failed to disconnect from voice channel")
					return
				}

				s.isInVoice = false
				utils.InteractionResponse(session, i.Interaction).Flags(discordgo.MessageFlagsEphemeral).
					Message(":wave:").SendWithLog(s.logger)
			}
		}
	}

	handlers["spotify_play_handler"] = func(session *discordgo.Session, i *discordgo.InteractionCreate) {
		switch i.Type {
		case discordgo.InteractionApplicationCommand:
			applicationCommandData := i.ApplicationCommandData()
			option, ok := utils.GetApplicationCommandOption(applicationCommandData, "spotify", "play")
			if !ok {
				return
			}

			s.logger.Debug().Str("user_id", utils.GetInteractionUserId(i.Interaction)).
				Interface("command", applicationCommandData).Msg("user invoked slash command")

			if len(option.Options) == 0 {
				s.logger.Error().Interface("command", applicationCommandData).Msg("unexpected empty options for command")
				utils.InteractionResponse(session, i.Interaction).Flags(discordgo.MessageFlagsEphemeral).
					Message("Something went wrong.").SendWithLog(s.logger)
				return
			}

			if !s.isInVoice {
				utils.InteractionResponse(session, i.Interaction).Flags(discordgo.MessageFlagsEphemeral).
					Message("Summon me first before playing.").SendWithLog(s.logger)
				return
			}

			query, _ := option.Options[0].Value.(string)

			tracks, err := s.player.Search(query, 5)
			if err != nil {
				s.logger.Error().Err(err).Msg("spotify search failed")
				utils.InteractionResponse(session, i.Interaction).Flags(discordgo.MessageFlagsEphemeral).
					Message("Something went wrong.").SendWithLog(s.logger)
				return
			}

			// No tracks found
			if len(tracks) == 0 {
				utils.InteractionResponse(session, i.Interaction).Flags(discordgo.MessageFlagsEphemeral).
					Message("No tracks found.").SendWithLog(s.logger)
				return
			}

			// Send the initial track (with the possibility of more to come)
			message := fmt.Sprintf("Is this your song?\n```Name: %s\nArtist: %s\n```%s", tracks[0].Name(), tracks[0].Artist(), tracks[0].Image())
			uid := base64.StdEncoding.EncodeToString([]byte(fmt.Sprintf("%s%d", query, time.Now().UnixNano())))
			if len(uid) > 64 {
				uid = uid[:64]
			}
			utils.InteractionResponse(session, i.Interaction).Flags(discordgo.MessageFlagsEphemeral).
				Message(message).Components(s.yesNoButtons(uid, true)...).SendWithLog(s.logger)

			s.playInteractions[uid] = tracks

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

			s.logger.Debug().Str("user_id", utils.GetInteractionUserId(i.Interaction)).
				Interface("component", messageComponentData).Msg("user interacted with component")

			idSplit := strings.Split(messageComponentData.CustomID, "_")
			if len(idSplit) != 4 {
				s.logger.Error().Str("custom_id", messageComponentData.CustomID).
					Msg("message interaction response had an unknown custom id")
				utils.InteractionResponse(session, i.Interaction).Flags(discordgo.MessageFlagsEphemeral).
					Message("Something went wrong.").SendWithLog(s.logger)
				return
			}

			action := idSplit[2]
			uid := idSplit[3]
			userId := utils.GetInteractionUserId(i.Interaction)

			// The interaction was already closed out
			if _, ok := s.playInteractions[uid]; !ok {
				utils.InteractionResponse(session, i.Interaction).Flags(discordgo.MessageFlagsEphemeral).
					Message("This song list is no longer available. Try searching again.").SendWithLog(s.logger)
				return
			}

			switch action {
			case "yes":
				aTrack := authoredTrack{
					track:      s.playInteractions[uid][0],
					authorId:   utils.GetInteractionUserId(i.Interaction),
					authorName: utils.GetInteractionUserName(i.Interaction),
				}
				s.enqueueTrack(aTrack)
				s.logger.Debug().Str("user_id", userId).Interface("track", s.buildTrackObject(aTrack.track)).Msg("user enqueued track")

				message := fmt.Sprintf("%s by %s added to queue.", aTrack.track.Name(), aTrack.track.Artist())
				utils.InteractionResponse(session, i.Interaction).Flags(discordgo.MessageFlagsEphemeral).
					Message(message).SendWithLog(s.logger)

				delete(s.playInteractions, uid)
			case "no":
				track := s.playInteractions[uid][0]
				s.logger.Debug().Str("user_id", userId).Str("track", track.Name()).Msg("user responded no to track query")

				s.playInteractions[uid] = s.playInteractions[uid][1:]
				if len(s.playInteractions[uid]) == 0 {
					utils.InteractionResponse(session, i.Interaction).Flags(discordgo.MessageFlagsEphemeral).
						Message("That's all of them! Try searching again.").SendWithLog(s.logger)
					return
				}
				track = s.playInteractions[uid][0]
				message := fmt.Sprintf("Is this your song?\n```Name: %s\nArtist: %s\n```%s", track.Name(), track.Artist(), track.Image())
				utils.InteractionResponse(session, i.Interaction).Flags(discordgo.MessageFlagsEphemeral).
					Message(message).Components(s.yesNoButtons(uid, true)...).SendWithLog(s.logger)
			}
		}
	}

	handlers["spotify_queue_handler"] = func(session *discordgo.Session, i *discordgo.InteractionCreate) {
		switch i.Type {
		case discordgo.InteractionApplicationCommand:
			applicationCommandData := i.ApplicationCommandData()

			if _, ok := utils.GetApplicationCommandOption(applicationCommandData, "spotify", "queue"); !ok {
				return
			}

			s.logger.Debug().Str("user_id", utils.GetInteractionUserId(i.Interaction)).
				Interface("command", applicationCommandData).Msg("user invoked slash command")

			message := ""
			for index, aTrack := range s.trackQueue {
				if index == 0 {
					message += "Currently playing:\n"
					timeElapsed := (time.Duration(s.framesProcessed*20) * time.Millisecond).Round(time.Second).String()
					totalTime := (time.Duration(aTrack.track.Duration()) * time.Millisecond).Round(time.Second).String()
					message += fmt.Sprintf("  %s - %s [%s/%s] (@%s)\n", aTrack.track.Name(), aTrack.track.Artist(),
						timeElapsed, totalTime, aTrack.authorName)
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

			utils.InteractionResponse(session, i.Interaction).Flags(discordgo.MessageFlagsEphemeral).
				Message(message).SendWithLog(s.logger)
		}
	}

	handlers["spotify_skip_handler"] = func(session *discordgo.Session, i *discordgo.InteractionCreate) {
		switch i.Type {
		case discordgo.InteractionApplicationCommand:
			applicationCommandData := i.ApplicationCommandData()
			if _, ok := utils.GetApplicationCommandOption(applicationCommandData, "spotify", "skip"); !ok {
				return
			}

			s.logger.Debug().Str("user_id", utils.GetInteractionUserId(i.Interaction)).
				Interface("command", applicationCommandData).Msg("user invoked slash command")

			if !s.isInVoice || !s.isPlaying {
				utils.InteractionResponse(session, i.Interaction).Flags(discordgo.MessageFlagsEphemeral).
					Message("Nothing to skip.").SendWithLog(s.logger)
				return
			} else {
				userId := utils.GetInteractionUserId(i.Interaction)
				if s.trackQueue[0].authorId != userId {
					s.logger.Debug().Str("user_id", userId).Str("author_id", s.trackQueue[0].authorId).
						Interface("track", s.buildTrackObject(s.trackQueue[0].track)).Msg("user attempted to skip track")
					utils.InteractionResponse(session, i.Interaction).Flags(discordgo.MessageFlagsEphemeral).
						Message("You cannot skip a track you didn't queue.").SendWithLog(s.logger)
					return
				}
				s.logger.Debug().Str("user_id", userId).Str("track", s.trackQueue[0].track.Name()).Msg("user skipped track")
				s.skipChan <- true
				utils.InteractionResponse(session, i.Interaction).Flags(discordgo.MessageFlagsEphemeral).
					Message(":gun:").SendWithLog(s.logger)
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

func (s *SpotifyPlugin) dequeueTrack() {
	if len(s.trackQueue) == 0 {
		return
	}
	s.queueLock.Lock()
	s.trackQueue = s.trackQueue[1:]
	s.queueLock.Unlock()
}

func (s *SpotifyPlugin) trackPlayer(ctx context.Context) {
	for track := range s.queueChan {
		select {
		case <-ctx.Done():
			return
		default:
		}
		r, err := s.player.DownloadTrack(track)
		if err != nil {
			s.logger.Error().Err(err).Str("track", track.Name()).Msg("failed to download track")
			s.dequeueTrack()
			continue
		}
		s.framesProcessed = 0
		encodeSession, _ := dca.EncodeMem(r, dca.StdEncodeOptions)
		// Create a channel that gives us about 1 minutes of buffer room
		encodedFrames := make(chan []byte, 3000)
		go func(encodeSession *dca.EncodeSession, encodedFrames chan<- []byte) {
			for {
				frame, err := encodeSession.OpusFrame()
				if err != nil {
					close(encodedFrames)
					s.logger.Debug().Interface("track", s.buildTrackObject(track)).Msg("finished encoding track")
					return
				}
				encodedFrames <- frame
			}
		}(encodeSession, encodedFrames)

		s.isPlaying = true
		_ = s.voiceConnection.Speaking(true)
	playLoop:
		for {
			select {
			case <-s.skipChan:
				break playLoop
			case frame, ok := <-encodedFrames:
				if !ok {
					break playLoop
				}

				select {
				case s.voiceConnection.OpusSend <- frame:
					s.framesProcessed++
				case <-time.After(time.Second):
					break playLoop
				}
			}
		}
		s.isPlaying = false
		_ = s.voiceConnection.Speaking(false)
		s.dequeueTrack()
		encodeSession.Cleanup()
	}
}

func (s *SpotifyPlugin) buildTrackObject(track spotify.Track) any {
	return struct {
		Name   string
		Artist string
		Album  string
	}{
		track.Name(),
		track.Artist(),
		track.Album(),
	}
}
