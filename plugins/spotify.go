package plugins

import (
	"context"
	"encoding/base64"
	"fmt"
	"github.com/bwmarrin/discordgo"
	"github.com/eolso/threadsafe"
	"github.com/jonas747/dca"
	"github.com/olympus-go/apollo/spotify"
	"github.com/olympus-go/eris/utils"
	"github.com/rs/zerolog"
	"io"
	"strings"
	"time"
)

const (
	spotifyPlayState     = 0
	spotifyPauseState    = 1
	spotifyNextState     = 2
	spotifyPreviousState = 3
	spotifyStopState     = 4
)

type authoredTrack struct {
	track      spotify.Track
	authorId   string
	authorName string
}

type spotifySession struct {
	player           *spotify.Player
	trackQueue       *threadsafe.Slice[authoredTrack]
	playInteractions *threadsafe.Map[string, []spotify.Track]
	framesProcessed  int
	queueChan        chan spotify.Track
	commandChan      chan int
	state            int
	playPause        chan bool
	workerCancel     context.CancelFunc
	voiceConnection  *discordgo.VoiceConnection
	logger           zerolog.Logger
}

type SpotifyPlugin struct {
	sessions     *threadsafe.Map[string, *spotifySession]
	callback     string
	clientId     string
	clientSecret string
	logger       zerolog.Logger
}

func Spotify(logger zerolog.Logger, callback string, clientId string, clientSecret string) *SpotifyPlugin {
	plugin := SpotifyPlugin{
		sessions:     threadsafe.NewMap[string, *spotifySession](),
		callback:     callback,
		clientId:     clientId,
		clientSecret: clientSecret,
		logger:       logger.With().Str("plugin", "spotify").Logger(),
	}

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

	handlers["spotify_join_handler"] = func(discordSession *discordgo.Session, i *discordgo.InteractionCreate) {
		switch i.Type {
		case discordgo.InteractionApplicationCommand:
			applicationCommandData := i.ApplicationCommandData()

			if _, ok := utils.GetApplicationCommandOption(applicationCommandData, "spotify", "join"); !ok {
				return
			}

			s.logger.Debug().Str("user_id", utils.GetInteractionUserId(i.Interaction)).
				Interface("command", applicationCommandData).Msg("user invoked slash command")

			if i.Interaction.GuildID == "" {
				utils.InteractionResponse(discordSession, i.Interaction).Ephemeral().
					Message("I can't do that in a DM, sry.").SendWithLog(s.logger)
				return
			}

			// If the session for the guild doesn't already exist, create it
			spotSession, ok := s.sessions.Get(i.Interaction.GuildID)
			if !ok {
				spotSession = s.newSession()
				s.sessions.Set(i.Interaction.GuildID, spotSession)
			}

			voiceId := utils.GetInteractionUserVoiceStateId(discordSession, i.Interaction)

			// If the user isn't in a voice channel
			if voiceId == "" {
				utils.InteractionResponse(discordSession, i.Interaction).Ephemeral().
					Message("You're not in a voice channel bub.").SendWithLog(s.logger)
				return
			}

			if spotSession.voiceConnection != nil && spotSession.voiceConnection.ChannelID == voiceId {
				utils.InteractionResponse(discordSession, i.Interaction).Ephemeral().
					Message("I'm already here!").SendWithLog(s.logger)
				return
			}

			utils.InteractionResponse(discordSession, i.Interaction).Ephemeral().
				Type(discordgo.InteractionResponseDeferredChannelMessageWithSource).SendWithLog(s.logger)

			if spotSession.voiceConnection != nil {
				spotSession.commandChan <- spotifyPauseState
				// A pause command should return almost instantaneously, but we should still wait for the player to stop
				time.Sleep(30 * time.Millisecond)
				if err := spotSession.voiceConnection.Disconnect(); err != nil {
					s.logger.Error().Err(err).Msg("failed to disconnect from voice channel")
					utils.InteractionResponse(discordSession, i.Interaction).Ephemeral().
						Message("Something went wrong.").EditWithLog(s.logger)
					return
				}
			} else {
				var ctx context.Context
				ctx, spotSession.workerCancel = context.WithCancel(context.Background())
				go spotSession.listenForCommands(ctx)
				go spotSession.listenForTracks(ctx)
			}

			var err error
			spotSession.voiceConnection, err = discordSession.ChannelVoiceJoin(i.GuildID, voiceId, false, true)
			if err != nil {
				s.logger.Error().Err(err).Msg("failed to join voice channel")
				utils.InteractionResponse(discordSession, i.Interaction).Ephemeral().
					Message("Something went wrong.").EditWithLog(s.logger)
				return
			}

			spotSession.commandChan <- spotifyPlayState

			utils.InteractionResponse(discordSession, i.Interaction).Flags(discordgo.MessageFlagsEphemeral).
				Message(":tada:").EditWithLog(s.logger)
		}
	}

	handlers["spotify_leave_handler"] = func(discordSession *discordgo.Session, i *discordgo.InteractionCreate) {
		switch i.Type {
		case discordgo.InteractionApplicationCommand:
			applicationCommandData := i.ApplicationCommandData()
			if _, ok := utils.GetApplicationCommandOption(applicationCommandData, "spotify", "leave"); !ok {
				return
			}

			s.logger.Debug().Str("user_id", utils.GetInteractionUserId(i.Interaction)).
				Interface("command", applicationCommandData).Msg("user invoked slash command")

			if i.Interaction.GuildID == "" {
				utils.InteractionResponse(discordSession, i.Interaction).Ephemeral().
					Message("I can't do that in a DM, sry.").SendWithLog(s.logger)
				return
			}

			spotSession, ok := s.sessions.Get(i.Interaction.GuildID)
			if !ok || spotSession.voiceConnection == nil {
				utils.InteractionResponse(discordSession, i.Interaction).Ephemeral().
					Message("I don't think I'm in a voice chat here. ??\\_(???)_/??").SendWithLog(s.logger)
				return
			}

			spotSession.trackQueue.Empty()
			spotSession.workerCancel()
			//spotSession.queueChan = make(chan spotify.Track, 100)
			//spotSession.commandChan = make(chan int)

			if err := spotSession.voiceConnection.Disconnect(); err != nil {
				s.logger.Error().Err(err).Msg("failed to disconnect from voice channel")
				return
			}

			spotSession.voiceConnection = nil

			utils.InteractionResponse(discordSession, i.Interaction).Flags(discordgo.MessageFlagsEphemeral).
				Message(":wave:").SendWithLog(s.logger)
		}
	}

	handlers["spotify_play_handler"] = func(discordSession *discordgo.Session, i *discordgo.InteractionCreate) {
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
				utils.InteractionResponse(discordSession, i.Interaction).Flags(discordgo.MessageFlagsEphemeral).
					Message("Something went wrong.").SendWithLog(s.logger)
				return
			}

			if i.Interaction.GuildID == "" {
				utils.InteractionResponse(discordSession, i.Interaction).Ephemeral().
					Message("I can't do that in a DM, sry.").SendWithLog(s.logger)
				return
			}

			spotSession, ok := s.sessions.Get(i.Interaction.GuildID)
			if !ok {
				utils.InteractionResponse(discordSession, i.Interaction).Ephemeral().
					Message("I don't think I'm in a voice chat here. ??\\_(???)_/??").SendWithLog(s.logger)
				return
			}

			if !spotSession.player.LoggedIn() {
				utils.InteractionResponse(discordSession, i.Interaction).Flags(discordgo.MessageFlagsEphemeral).
					Message("Login first before playing.\n`/spotify login`").SendWithLog(s.logger)
				return
			}

			query, _ := option.Options[0].Value.(string)

			tracks, err := spotSession.player.Search(query, 5)
			if err != nil {
				s.logger.Error().Err(err).Msg("spotify search failed")
				utils.InteractionResponse(discordSession, i.Interaction).Flags(discordgo.MessageFlagsEphemeral).
					Message("Something went wrong.").SendWithLog(s.logger)
				return
			}

			// No tracks found
			if len(tracks) == 0 {
				utils.InteractionResponse(discordSession, i.Interaction).Flags(discordgo.MessageFlagsEphemeral).
					Message("No tracks found.").SendWithLog(s.logger)
				return
			}

			// Send the initial track (with the possibility of more to come)
			message := fmt.Sprintf("Is this your song?\n```Name: %s\nArtist: %s\n```%s", tracks[0].Name(), tracks[0].Artist(), tracks[0].Image())
			// TODO this isn't truly truly unique
			uid := base64.StdEncoding.EncodeToString([]byte(fmt.Sprintf("%s%d", query, time.Now().UnixNano())))
			if len(uid) > 64 {
				uid = uid[:64]
			}
			utils.InteractionResponse(discordSession, i.Interaction).Flags(discordgo.MessageFlagsEphemeral).
				Message(message).Components(s.yesNoButtons(uid, true)...).SendWithLog(s.logger)

			spotSession.playInteractions.Set(uid, tracks)

			go func() {
				time.Sleep(15 * time.Second)
				if _, ok := spotSession.playInteractions.Get(uid); ok {
					spotSession.playInteractions.Delete(uid)
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

			if i.Interaction.GuildID == "" {
				utils.InteractionResponse(discordSession, i.Interaction).Ephemeral().
					Message("I can't do that in a DM, sry.").SendWithLog(s.logger)
				return
			}

			// If the session for the guild doesn't already exist, create it
			spotSession, ok := s.sessions.Get(i.Interaction.GuildID)
			if !ok {
				utils.InteractionResponse(discordSession, i.Interaction).Ephemeral().
					Message("I don't think I'm in a voice chat here. ??\\_(???)_/??").SendWithLog(s.logger)
				return
			}

			if !spotSession.player.LoggedIn() {
				utils.InteractionResponse(discordSession, i.Interaction).Flags(discordgo.MessageFlagsEphemeral).
					Message("Login first before playing.\n`/spotify login`").SendWithLog(s.logger)
				return
			}

			idSplit := strings.Split(messageComponentData.CustomID, "_")
			if len(idSplit) != 4 {
				s.logger.Error().Str("custom_id", messageComponentData.CustomID).
					Msg("message interaction response had an unknown custom Id")
				utils.InteractionResponse(discordSession, i.Interaction).Flags(discordgo.MessageFlagsEphemeral).
					Message("Something went wrong.").SendWithLog(s.logger)
				return
			}

			action := idSplit[2]
			uid := idSplit[3]
			userId := utils.GetInteractionUserId(i.Interaction)

			// The interaction was already closed out
			if _, ok := spotSession.playInteractions.Get(uid); !ok {
				utils.InteractionResponse(discordSession, i.Interaction).Flags(discordgo.MessageFlagsEphemeral).
					Message("This song list is no longer available. Try searching again.").SendWithLog(s.logger)
				return
			}

			switch action {
			case "yes":
				tracks, ok := spotSession.playInteractions.Get(uid)
				if !ok || len(tracks) == 0 {
					s.logger.Error().Msg("tracks no longer exist")
					utils.InteractionResponse(discordSession, i.Interaction).Ephemeral().
						Message("Something went wrong.").FollowUpCreate()
					return
				}

				aTrack := authoredTrack{
					track:      tracks[0],
					authorId:   utils.GetInteractionUserId(i.Interaction),
					authorName: utils.GetInteractionUserName(i.Interaction),
				}

				spotSession.enqueueTrack(aTrack)

				s.logger.Debug().Str("user_id", userId).
					Interface("track", spotSession.buildTrackObject(aTrack.track)).Msg("user enqueued track")

				message := fmt.Sprintf("%s by %s added to queue.", aTrack.track.Name(), aTrack.track.Artist())
				utils.InteractionResponse(discordSession, i.Interaction).Flags(discordgo.MessageFlagsEphemeral).
					Message(message).SendWithLog(s.logger)

				spotSession.playInteractions.Delete(uid)
			case "no":
				tracks, ok := spotSession.playInteractions.Get(uid)
				if !ok || len(tracks) == 0 {
					s.logger.Error().Msg("tracks no longer exist")
					utils.InteractionResponse(discordSession, i.Interaction).Ephemeral().
						Message("Something went wrong.").FollowUpCreate()
					return
				}

				s.logger.Debug().Str("user_id", userId).Str("track", tracks[0].Name()).Msg("user responded no to track query")

				spotSession.playInteractions.Set(uid, tracks[1:])
				if len(tracks[1:]) == 0 {
					utils.InteractionResponse(discordSession, i.Interaction).Flags(discordgo.MessageFlagsEphemeral).
						Message("That's all of them! Try searching again.").SendWithLog(s.logger)
					spotSession.playInteractions.Delete(uid)
					return
				}

				message := fmt.Sprintf("Is this your song?\n```Name: %s\nArtist: %s\n```%s",
					tracks[1].Name(), tracks[1].Artist(), tracks[1].Image())
				utils.InteractionResponse(discordSession, i.Interaction).Flags(discordgo.MessageFlagsEphemeral).
					Message(message).Components(s.yesNoButtons(uid, true)...).SendWithLog(s.logger)
			}
		}
	}

	handlers["spotify_queue_handler"] = func(discordSession *discordgo.Session, i *discordgo.InteractionCreate) {
		switch i.Type {
		case discordgo.InteractionApplicationCommand:
			applicationCommandData := i.ApplicationCommandData()

			if _, ok := utils.GetApplicationCommandOption(applicationCommandData, "spotify", "queue"); !ok {
				return
			}

			s.logger.Debug().Str("user_id", utils.GetInteractionUserId(i.Interaction)).
				Interface("command", applicationCommandData).Msg("user invoked slash command")

			if i.Interaction.GuildID == "" {
				utils.InteractionResponse(discordSession, i.Interaction).Ephemeral().
					Message("I can't do that in a DM, sry.").SendWithLog(s.logger)
				return
			}

			spotSession, ok := s.sessions.Get(i.Interaction.GuildID)
			if !ok {
				utils.InteractionResponse(discordSession, i.Interaction).Ephemeral().
					Message("I don't think I'm in a voice chat here. ??\\_(???)_/??").SendWithLog(s.logger)
				return
			}

			message := ""
			tracks := spotSession.trackQueue.GetAll()
			for index, aTrack := range tracks {
				if index == 0 {
					message += "Currently playing:\n"
					message += fmt.Sprintf("  %s - %s (@%s)\n", aTrack.track.Name(), aTrack.track.Artist(),
						aTrack.authorName)

					elapsedDuration := (time.Duration(spotSession.framesProcessed*20) * time.Millisecond).Round(time.Second)
					totalDuration := (time.Duration(aTrack.track.Duration()) * time.Millisecond).Round(time.Second)
					elapsedPercent := elapsedDuration.Seconds() / totalDuration.Seconds()
					message += fmt.Sprintf("  <%s%s> [%s/%s]\n", strings.Repeat("\u2588", int(elapsedPercent*30)),
						strings.Repeat("\u2591", int(30-(elapsedPercent*30))), elapsedDuration.String(),
						totalDuration.String())

					if len(tracks) > 1 {
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

			utils.InteractionResponse(discordSession, i.Interaction).Flags(discordgo.MessageFlagsEphemeral).
				Message(message).SendWithLog(s.logger)
		}
	}

	handlers["spotify_resume_handler"] = func(discordSession *discordgo.Session, i *discordgo.InteractionCreate) {
		switch i.Type {
		case discordgo.InteractionApplicationCommand:
			applicationCommandData := i.ApplicationCommandData()
			if _, ok := utils.GetApplicationCommandOption(applicationCommandData, "spotify", "resume"); !ok {
				return
			}

			s.logger.Debug().Str("user_id", utils.GetInteractionUserId(i.Interaction)).
				Interface("command", applicationCommandData).Msg("user invoked slash command")

			if i.Interaction.GuildID == "" {
				utils.InteractionResponse(discordSession, i.Interaction).Ephemeral().
					Message("I can't do that in a DM, sry.").SendWithLog(s.logger)
				return
			}

			// If the session for the guild doesn't already exist, create it
			spotSession, ok := s.sessions.Get(i.Interaction.GuildID)
			if !ok {
				utils.InteractionResponse(discordSession, i.Interaction).Ephemeral().
					Message("I don't think I'm in a voice chat here. ??\\_(???)_/??").SendWithLog(s.logger)
				return
			}

			if spotSession.trackQueue.Len() == 0 {
				utils.InteractionResponse(discordSession, i.Interaction).Flags(discordgo.MessageFlagsEphemeral).
					Message("Nothing in queue.").SendWithLog(s.logger)
				return
			}

			spotSession.commandChan <- spotifyPlayState

			utils.InteractionResponse(discordSession, i.Interaction).Flags(discordgo.MessageFlagsEphemeral).
				Message(":arrow_forward:").SendWithLog(s.logger)
		}
	}

	handlers["spotify_pause_handler"] = func(discordSession *discordgo.Session, i *discordgo.InteractionCreate) {
		switch i.Type {
		case discordgo.InteractionApplicationCommand:
			applicationCommandData := i.ApplicationCommandData()
			if _, ok := utils.GetApplicationCommandOption(applicationCommandData, "spotify", "pause"); !ok {
				return
			}

			s.logger.Debug().Str("user_id", utils.GetInteractionUserId(i.Interaction)).
				Interface("command", applicationCommandData).Msg("user invoked slash command")

			if i.Interaction.GuildID == "" {
				utils.InteractionResponse(discordSession, i.Interaction).Ephemeral().
					Message("I can't do that in a DM, sry.").SendWithLog(s.logger)
				return
			}

			// If the session for the guild doesn't already exist, create it
			spotSession, ok := s.sessions.Get(i.Interaction.GuildID)
			if !ok {
				utils.InteractionResponse(discordSession, i.Interaction).Ephemeral().
					Message("I don't think I'm in a voice chat here. ??\\_(???)_/??").SendWithLog(s.logger)
				return
			}

			if spotSession.trackQueue.Len() == 0 {
				utils.InteractionResponse(discordSession, i.Interaction).Flags(discordgo.MessageFlagsEphemeral).
					Message("Nothing is currently playing.").SendWithLog(s.logger)
				return
			}

			spotSession.commandChan <- spotifyPauseState

			utils.InteractionResponse(discordSession, i.Interaction).Flags(discordgo.MessageFlagsEphemeral).
				Message(":pause_button:").SendWithLog(s.logger)
		}
	}

	handlers["spotify_skip_handler"] = func(discordSession *discordgo.Session, i *discordgo.InteractionCreate) {
		switch i.Type {
		case discordgo.InteractionApplicationCommand:
			applicationCommandData := i.ApplicationCommandData()
			if _, ok := utils.GetApplicationCommandOption(applicationCommandData, "spotify", "skip"); !ok {
				return
			}

			s.logger.Debug().Str("user_id", utils.GetInteractionUserId(i.Interaction)).
				Interface("command", applicationCommandData).Msg("user invoked slash command")

			if i.Interaction.GuildID == "" {
				utils.InteractionResponse(discordSession, i.Interaction).Ephemeral().
					Message("I can't do that in a DM, sry.").SendWithLog(s.logger)
				return
			}

			// If the session for the guild doesn't already exist, create it
			spotSession, ok := s.sessions.Get(i.Interaction.GuildID)
			if !ok {
				utils.InteractionResponse(discordSession, i.Interaction).Ephemeral().
					Message("I don't think I'm in a voice chat here. ??\\_(???)_/??").SendWithLog(s.logger)
				return
			}

			if spotSession.trackQueue.Len() == 0 {
				utils.InteractionResponse(discordSession, i.Interaction).Flags(discordgo.MessageFlagsEphemeral).
					Message("Nothing to skip.").SendWithLog(s.logger)
				return
			}

			userId := utils.GetInteractionUserId(i.Interaction)
			track := spotSession.trackQueue.Get(0)
			if track.authorId != userId {
				s.logger.Debug().Str("user_id", userId).Str("author_id", track.authorId).
					Interface("track", spotSession.buildTrackObject(track.track)).Msg("user attempted to skip track")
				utils.InteractionResponse(discordSession, i.Interaction).Flags(discordgo.MessageFlagsEphemeral).
					Message("You cannot skip a track you didn't queue.").SendWithLog(s.logger)
				return
			}
			s.logger.Debug().Str("user_id", userId).Str("track", track.track.Name()).Msg("user skipped track")
			spotSession.commandChan <- spotifyNextState
			utils.InteractionResponse(discordSession, i.Interaction).Flags(discordgo.MessageFlagsEphemeral).
				Message(":gun:").SendWithLog(s.logger)
		}
	}

	handlers["spotify_login_handler"] = func(discordSession *discordgo.Session, i *discordgo.InteractionCreate) {
		switch i.Type {
		case discordgo.InteractionApplicationCommand:
			applicationCommandData := i.ApplicationCommandData()
			if _, ok := utils.GetApplicationCommandOption(applicationCommandData, "spotify", "login"); !ok {
				return
			}

			s.logger.Debug().Str("user_id", utils.GetInteractionUserId(i.Interaction)).
				Interface("command", applicationCommandData).Msg("user invoked slash command")

			if i.Interaction.GuildID == "" {
				utils.InteractionResponse(discordSession, i.Interaction).Ephemeral().
					Message("I can't do that in a DM, sry.").SendWithLog(s.logger)
				return
			}

			// If the session for the guild doesn't already exist, create it
			spotSession, ok := s.sessions.Get(i.Interaction.GuildID)
			if !ok {
				spotSession = s.newSession()
				s.sessions.Set(i.Interaction.GuildID, spotSession)
			}

			if spotSession.player.LoggedIn() {
				yesButton := utils.Button().Label("Yes").Id("spotify_login_yes").Build()
				noButton := utils.Button().Style(discordgo.SecondaryButton).Label("Yes").Id("spotify_login_no").Build()
				utils.InteractionResponse(discordSession, i.Interaction).Ephemeral().
					Components(utils.ActionsRow().Button(yesButton).Button(noButton).Build()).
					Message("Spotify player is already logged in. Log out now?").SendWithLog(s.logger)
				return
			}

			url := spotify.StartLocalOAuth(s.clientId, s.clientSecret, s.callback)

			linkButton := utils.Button().Style(discordgo.LinkButton).Label("Login").URL(url).Build()
			utils.InteractionResponse(discordSession, i.Interaction).Ephemeral().Message("Click here to login!").
				Components(utils.ActionsRow().Button(linkButton).Build()).SendWithLog(s.logger)

			go func() {
				token := spotify.GetOAuthToken()
				if err := spotSession.player.LoginWithToken("georgetuney", token); err != nil {
					utils.InteractionResponse(discordSession, i.Interaction).Ephemeral().
						Message("Login failed :(").FollowUpCreate()
				} else {
					utils.InteractionResponse(discordSession, i.Interaction).Ephemeral().
						Message("Login successful :tada:").FollowUpCreate()
					spotSession.logger = spotSession.logger.With().
						Str("spotify_user", spotSession.player.Username()).Logger()
				}
			}()
		case discordgo.InteractionMessageComponent:
			messageComponentData := i.MessageComponentData()
			if !strings.HasPrefix(messageComponentData.CustomID, "spotify_login") {
				return
			}

			if i.Interaction.GuildID == "" {
				utils.InteractionResponse(discordSession, i.Interaction).Ephemeral().
					Message("I can't do that in a DM, sry.").SendWithLog(s.logger)
				return
			}

			// If the session for the guild doesn't already exist, create it
			spotSession, ok := s.sessions.Get(i.Interaction.GuildID)
			if !ok {
				spotSession = s.newSession()
				s.sessions.Set(i.Interaction.GuildID, spotSession)
			}

			idSplit := strings.Split(messageComponentData.CustomID, "_")
			if len(idSplit) != 3 {
				s.logger.Error().Str("custom_id", messageComponentData.CustomID).
					Msg("message interaction response had an unknown custom Id")
				utils.InteractionResponse(discordSession, i.Interaction).Flags(discordgo.MessageFlagsEphemeral).
					Message("Something went wrong.").SendWithLog(s.logger)
				return
			}

			action := idSplit[2]
			userId := utils.GetInteractionUserId(i.Interaction)

			s.logger.Debug().Str("user_id", userId).
				Interface("component", messageComponentData).Msg("user interacted with component")

			if action == "yes" {
				url := spotify.StartLocalOAuth(s.clientId, s.clientSecret, s.callback)

				linkButton := utils.Button().Style(discordgo.LinkButton).Label("Login").URL(url).Build()
				utils.InteractionResponse(discordSession, i.Interaction).Ephemeral().Message("Click here to login!").
					Components(utils.ActionsRow().Button(linkButton).Build()).SendWithLog(s.logger)

				go func() {
					token := spotify.GetOAuthToken()
					if err := spotSession.player.LoginWithToken("georgetuney", token); err != nil {
						utils.InteractionResponse(discordSession, i.Interaction).Ephemeral().Message("Login failed :(").
							FollowUpCreate()
					} else {
						utils.InteractionResponse(discordSession, i.Interaction).Ephemeral().Message("Login successful :tada:").
							FollowUpCreate()
					}
				}()
			} else if action == "no" {
				utils.InteractionResponse(discordSession, i.Interaction).Ephemeral().Message(":+1:").
					SendWithLog(s.logger)
			}
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
				Name:        "resume",
				Description: "Resume playback",
				Type:        discordgo.ApplicationCommandOptionSubCommand,
			},
			{
				Name:        "pause",
				Description: "Pause the currently playing song",
				Type:        discordgo.ApplicationCommandOptionSubCommand,
			},
			{
				Name:        "skip",
				Description: "Skip the currently playing song",
				Type:        discordgo.ApplicationCommandOptionSubCommand,
			},
			{
				Name:        "login",
				Description: "Connect the bot to your spotify account",
				Type:        discordgo.ApplicationCommandOptionSubCommand,
			},
		},
	}

	return commands
}

func (s *SpotifyPlugin) Intents() []discordgo.Intent {
	return nil
}

func (s *SpotifyPlugin) newSession() *spotifySession {
	playerConfig := spotify.DefaultPlayerConfig()
	playerConfig.ConfigHomeDir = ""
	playerConfig.OAuthCallback = s.callback

	spotSession := &spotifySession{
		player:           spotify.NewPlayer(playerConfig),
		trackQueue:       &threadsafe.Slice[authoredTrack]{},
		playInteractions: threadsafe.NewMap[string, []spotify.Track](),
		framesProcessed:  0,
		queueChan:        make(chan spotify.Track, 100),
		commandChan:      make(chan int),
		state:            spotifyPlayState,
		playPause:        make(chan bool),
		voiceConnection:  nil,
		logger:           s.logger.With().Logger(),
	}

	//go spotSession.listenForTracks(context.Background())
	//go spotSession.trackPlayer(context.Background())

	return spotSession
}

func (s *SpotifyPlugin) clearSession(session *spotifySession) {

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

func (s *spotifySession) enqueueTrack(track authoredTrack) {
	if s.trackQueue != nil {
		s.queueChan <- track.track
		s.trackQueue.Append(track)
	}
}

func (s *spotifySession) dequeueTrack() {
	if s.trackQueue != nil && s.trackQueue.Len() > 0 {
		s.trackQueue.Delete(0)
	}
}

func (s *spotifySession) listenForCommands(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		case cmd := <-s.commandChan:
			switch cmd {
			case spotifyPlayState:
				if s.state != spotifyPlayState {
					<-s.playPause
					s.state = spotifyPlayState
				}
			case spotifyPauseState:
				if s.state != spotifyPauseState {
					s.playPause <- true
					s.state = spotifyPauseState
				}
			}
		}
	}
}

func (s *spotifySession) listenForTracks(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		case track := <-s.queueChan:
			trackReader, err := s.player.DownloadTrack(track)
			if err != nil {
				s.dequeueTrack()
				s.logger.Error().Err(err).Str("track", track.Name()).Msg("failed to download track")
				continue
			}
			encodedFrames := s.loadTrack(ctx, trackReader, s.buildTrackObject(track))

			s.framesProcessed = 0
			s.playTrack(ctx, encodedFrames)

			s.dequeueTrack()
			if s.voiceConnection != nil {
				_ = s.voiceConnection.Speaking(false)
			}
		}
	}

}

func (s *spotifySession) loadTrack(ctx context.Context, trackReader io.Reader, trackInfo any) <-chan []byte {
	// Create a channel that gives us about 1 minutes of buffer room
	encodedFrames := make(chan []byte, 3000)

	go func(ctx context.Context, encodedFrames chan<- []byte) {
		encodeSession, _ := dca.EncodeMem(trackReader, dca.StdEncodeOptions)
		defer func() {
			if encodeSession != nil {
				encodeSession.Cleanup()
			}
		}()

		for {
			select {
			case <-ctx.Done():
				return
			default:
				frame, err := encodeSession.OpusFrame()
				if err != nil {
					close(encodedFrames)
					s.logger.Debug().Interface("track", trackInfo).Msg("finished encoding track")
					return
				}
				encodedFrames <- frame
			}
		}
	}(ctx, encodedFrames)

	return encodedFrames
}

func (s *spotifySession) playTrack(ctx context.Context, data <-chan []byte) {
	for {
		select {
		case <-ctx.Done():
			return
		case <-s.playPause:
			s.playPause <- true
		case frame, ok := <-data:
			if !ok || s.voiceConnection == nil {
				return
			}

			select {
			case s.voiceConnection.OpusSend <- frame:
				s.framesProcessed++
			case <-time.After(time.Second * 10):
				return
			}
		}
	}
}

func (s *spotifySession) buildTrackObject(track spotify.Track) any {
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
