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
	"math/rand"
	"sort"
	"strconv"
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

type spotifyQuizGame struct {
	playlist  []string
	questions int

	questionNumber      int
	questionAnswer      int
	questionAnswerTrack spotify.Track

	questionStartTime     time.Time
	questionResponseTimes *threadsafe.Map[string, float64]

	// scoreboard contains the players as keys, and their points as values
	scoreboard *threadsafe.Map[string, struct {
		score     int
		totalTime float64
	}]

	startInteraction *discordgo.Interaction
	startMessage     string

	rng        *rand.Rand
	cancelFunc context.CancelFunc
}

type spotifySession struct {
	player           *spotify.Player
	trackQueue       *threadsafe.Slice[authoredTrack]
	playInteractions *threadsafe.Map[string, []spotify.Track]
	quizGame         *spotifyQuizGame
	framesProcessed  int
	queueChan        chan spotify.Track
	commandChan      chan int
	state            int
	playPause        chan bool
	skipChan         chan bool // TODO this is dumb
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
					Message("I don't think I'm in a voice chat here. ¯\\_(ツ)_/¯").SendWithLog(s.logger)
				return
			}

			spotSession.trackQueue.Empty()
			spotSession.workerCancel()
			spotSession.commandChan = nil
			spotSession.queueChan = nil
			if spotSession.quizGame != nil {
				spotSession.quizGame.cancelFunc()
				spotSession.quizGame = nil
			}

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
					Message("I don't think I'm in a voice chat here. ¯\\_(ツ)_/¯").SendWithLog(s.logger)
				return
			}

			if !spotSession.player.LoggedIn() {
				utils.InteractionResponse(discordSession, i.Interaction).Flags(discordgo.MessageFlagsEphemeral).
					Message("Login first before playing.\n`/spotify login`").SendWithLog(s.logger)
				return
			}

			// TODO I don't think order is guaranteed. We should check names.
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
					Message("I don't think I'm in a voice chat here. ¯\\_(ツ)_/¯").SendWithLog(s.logger)
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
					Message("I don't think I'm in a voice chat here. ¯\\_(ツ)_/¯").SendWithLog(s.logger)
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
					Message("I don't think I'm in a voice chat here. ¯\\_(ツ)_/¯").SendWithLog(s.logger)
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
					Message("I don't think I'm in a voice chat here. ¯\\_(ツ)_/¯").SendWithLog(s.logger)
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
					Message("I don't think I'm in a voice chat here. ¯\\_(ツ)_/¯").SendWithLog(s.logger)
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

	handlers["spotify_quiz_handler"] = func(discordSession *discordgo.Session, i *discordgo.InteractionCreate) {
		switch i.Type {
		case discordgo.InteractionApplicationCommand:
			applicationCommandData := i.ApplicationCommandData()
			if _, ok := utils.GetApplicationCommandOption(applicationCommandData, "spotify", "quiz"); !ok {
				return
			}

			userId := utils.GetInteractionUserId(i.Interaction)
			s.logger.Debug().Str("user_id", userId).Interface("command", applicationCommandData).
				Msg("user invoked slash command")

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

			if !spotSession.player.LoggedIn() {
				utils.InteractionResponse(discordSession, i.Interaction).Ephemeral().
					Message("Login first before playing.\n`/spotify login`").SendWithLog(s.logger)
				return
			}

			if spotSession.quizGame != nil {
				utils.InteractionResponse(discordSession, i.Interaction).Ephemeral().
					Message("Game is already running!").SendWithLog(s.logger)
				return
			}

			if spotSession.voiceConnection == nil {
				utils.InteractionResponse(discordSession, i.Interaction).Ephemeral().
					Message("Summon me into a voice channel before starting.").SendWithLog(s.logger)
				return
			}

			options, ok := utils.GetApplicationCommandOption(applicationCommandData, "spotify", "quiz")
			if !ok {
				return
			}

			// Set defaults and try and fetch options
			playlist := ""
			questions := 10
			for _, option := range options.Options {
				switch option.Name {
				case "playlist":
					playlist, _ = option.Value.(string)
				case "questions":
					v, _ := option.Value.(float64)
					questions = int(v)
				default:
					s.logger.Warn().Str("option_name", option.Name).Msg("interaction received unknown option")
				}
			}

			utils.InteractionResponse(discordSession, i.Interaction).
				Type(discordgo.InteractionResponseDeferredChannelMessageWithSource).SendWithLog(s.logger)

			results, err := spotSession.player.SearchPlaylist(playlist, 1)
			if err != nil || len(results) == 0 {
				s.logger.Error().Err(err).Msg("failed to search playlist")
				utils.InteractionResponse(discordSession, i.Interaction).Ephemeral().
					Message("I had trouble finding that playlist :/").EditWithLog(s.logger)
				return
			}

			ctx, cancelFunc := context.WithCancel(context.Background())
			quizGame := &spotifyQuizGame{
				playlist:       results[0].Tracks(),
				questionNumber: 1,

				questions: questions,
				scoreboard: threadsafe.NewMap[string, struct {
					score     int
					totalTime float64
				}](),
				rng:        rand.New(rand.NewSource(time.Now().UnixNano())),
				cancelFunc: cancelFunc,
			}

			spotSession.quizGame = quizGame

			quizGame.startMessage = fmt.Sprintf("<@%s> started a spotify quiz game! Click the button to join.", userId)
			buttonBuilder := utils.Button().Id("spotify_quiz_join").Label("Join game")
			components := utils.ActionsRow().Button(buttonBuilder.Build()).Build()

			utils.InteractionResponse(discordSession, i.Interaction).Message(quizGame.startMessage).Components(components).
				EditWithLog(s.logger)

			quizGame.startInteraction = i.Interaction

			go func() {
				select {
				case <-ctx.Done():
					return
				case <-time.After(15 * time.Second):
					break
				}

				// Close out the join option
				components = utils.ActionsRow().Button(buttonBuilder.Enabled(false).Build()).Build()
				utils.InteractionResponse(discordSession, i.Interaction).Components(components).EditWithLog(s.logger)

				if len(quizGame.scoreboard.Data) == 0 {
					utils.InteractionResponse(discordSession, i.Interaction).
						Message("No one joined :disappointed:").FollowUpCreate()
					spotSession.quizGame = nil
					return
				}

				for index := 0; index < quizGame.questions; index++ {
					if spotSession.quizGame == nil {
						return
					}
					quizGame.questionAnswer = quizGame.rng.Intn(5)
					tracks := quizGame.getRandomTracks(spotSession.player, 5)
					quizGame.questionAnswerTrack = tracks[quizGame.questionAnswer]
					quizGame.questionResponseTimes = threadsafe.NewMap[string, float64]()
					for _, username := range quizGame.scoreboard.Keys() {
						quizGame.questionResponseTimes.Set(username, 16.0)
					}

					spotSession.enqueueTrack(authoredTrack{tracks[quizGame.questionAnswer], "george", "george"})
					quizGame.questionStartTime = time.Now()
					questionMessage, _ := utils.InteractionResponse(discordSession, i.Interaction).
						Response(quizGame.generateQuestion(tracks)).FollowUpCreate()

					go func() {
						select {
						case <-ctx.Done():
							return
						case <-time.After(15 * time.Second):
							break
						}

						spotSession.commandChan <- spotifyNextState

						utils.InteractionResponse(discordSession, i.Interaction).Components().
							FollowUpEdit(questionMessage.ID)
						utils.InteractionResponse(discordSession, i.Interaction).
							Message(quizGame.generateQuestionWinner()).FollowUpCreate()
					}()

					select {
					case <-ctx.Done():
						return
					case <-time.After(18 * time.Second):
						break
					}

					quizGame.questionNumber++
				}

				utils.InteractionResponse(discordSession, i.Interaction).Message(quizGame.generateGameWinner()).
					FollowUpCreate()

				spotSession.quizGame = nil
			}()
		case discordgo.InteractionMessageComponent:
			messageComponentData := i.MessageComponentData()
			if strings.HasPrefix(messageComponentData.CustomID, "spotify_quiz_join") {
				s.logger.Debug().Str("user_id", utils.GetInteractionUserId(i.Interaction)).
					Interface("component", messageComponentData).Msg("user interacted with component")

				spotSession, ok := s.sessions.Get(i.Interaction.GuildID)
				if !ok || spotSession.quizGame == nil {
					utils.InteractionResponse(discordSession, i.Interaction).Ephemeral().
						Message("Game no longer exists.").SendWithLog(s.logger)
					return
				}

				username := utils.GetInteractionUserName(i.Interaction)

				if _, ok = spotSession.quizGame.scoreboard.Get(username); ok {
					utils.InteractionResponse(discordSession, i.Interaction).Ephemeral().
						Message("gl;hf").SendWithLog(s.logger)
					return
				}

				spotSession.quizGame.scoreboard.Set("@"+username, struct {
					score     int
					totalTime float64
				}{score: 0, totalTime: 0.0})

				message := fmt.Sprintf("%s\n```", spotSession.quizGame.startMessage)
				for _, user := range spotSession.quizGame.scoreboard.Keys() {
					message += fmt.Sprintf("%s joined.\n", user)
				}
				message += "```"

				utils.InteractionResponse(discordSession, spotSession.quizGame.startInteraction).
					Message(message).EditWithLog(s.logger)

				utils.InteractionResponse(discordSession, i.Interaction).Ephemeral().
					Message("gl;hf").SendWithLog(s.logger)

			} else if strings.HasPrefix(messageComponentData.CustomID, "spotify_quiz_answer") {
				username := fmt.Sprintf("@%s", utils.GetInteractionUserName(i.Interaction))

				s.logger.Debug().Str("user_id", utils.GetInteractionUserId(i.Interaction)).
					Interface("component", messageComponentData).Msg("user interacted with component")

				spotSession, ok := s.sessions.Get(i.Interaction.GuildID)
				if !ok || spotSession.quizGame == nil {
					utils.InteractionResponse(discordSession, i.Interaction).Ephemeral().
						Message("Game no longer exists.").SendWithLog(s.logger)
					return
				}

				if _, ok = spotSession.quizGame.scoreboard.Get(username); !ok {
					utils.InteractionResponse(discordSession, i.Interaction).Ephemeral().
						Message("You aren't a part of this round.").SendWithLog(s.logger)
					return
				}

				if responseTime, _ := spotSession.quizGame.questionResponseTimes.Get(username); responseTime != 16.0 {
					utils.InteractionResponse(discordSession, i.Interaction).Ephemeral().
						Message("You already selected an answer for this question.").SendWithLog(s.logger)
					return
				}

				idSplit := strings.Split(messageComponentData.CustomID, "_")
				if len(idSplit) != 4 {
					s.logger.Error().Str("custom_id", messageComponentData.CustomID).
						Msg("message interaction response had an unknown custom Id")
					utils.InteractionResponse(discordSession, i.Interaction).Ephemeral().
						Message("Something went wrong.").SendWithLog(s.logger)
					return
				}

				answer, err := strconv.Atoi(idSplit[3])
				if err != nil {
					s.logger.Error().Err(err).Str("id", idSplit[3]).Msg("failed to convert id to int")
					utils.InteractionResponse(discordSession, i.Interaction).Ephemeral().
						Message("Something went wrong.").SendWithLog(s.logger)
					return

				}

				timeElapsed := time.Since(spotSession.quizGame.questionStartTime).Round(time.Millisecond).Seconds()
				if answer-1 == spotSession.quizGame.questionAnswer {
					spotSession.quizGame.questionResponseTimes.Set(username, timeElapsed)

					score, _ := spotSession.quizGame.scoreboard.Get(username)
					score.totalTime += timeElapsed
					spotSession.quizGame.scoreboard.Set(username, score)
				} else {
					spotSession.quizGame.questionResponseTimes.Set(username, 15.0)

					score, _ := spotSession.quizGame.scoreboard.Get(username)
					score.totalTime += 15.0
					spotSession.quizGame.scoreboard.Set(username, score)
				}

				message := fmt.Sprintf("You answered in: %.3fs :stopwatch:", timeElapsed)
				utils.InteractionResponse(discordSession, i.Interaction).Ephemeral().
					Message(message).SendWithLog(s.logger)
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
			{
				Name:        "quiz",
				Description: "Start a spotify quiz game",
				Type:        discordgo.ApplicationCommandOptionSubCommand,
				Options: []*discordgo.ApplicationCommandOption{
					{
						Name:        "playlist",
						Description: "Link to public playlist to use",
						Type:        discordgo.ApplicationCommandOptionString,
						Required:    true,
					},
					{
						Name:        "questions",
						Description: "Number of questions to play (default = 10)",
						Type:        discordgo.ApplicationCommandOptionInteger,
						Required:    false,
					},
				},
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
		skipChan:         make(chan bool),
		voiceConnection:  nil,
		logger:           s.logger.With().Logger(),
	}

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
	s.commandChan = make(chan int)
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
			case spotifyNextState:
				if s.state == spotifyPlayState {
					s.state = spotifyNextState
					s.skipChan <- true
				}
			}
		}
	}
}

func (s *spotifySession) listenForTracks(ctx context.Context) {
	s.queueChan = make(chan spotify.Track, 100)
	for {
		select {
		case <-ctx.Done():
			return
		case track := <-s.queueChan:
			trackReader, err := s.player.DownloadTrack(track)
			if err != nil {
				s.state = spotifyStopState
				s.dequeueTrack()
				s.logger.Error().Err(err).Str("track", track.Name()).Msg("failed to download track")
				continue
			}

			encodeCtx, encodeCancel := context.WithCancel(ctx)
			encodedFrames := s.loadTrack(encodeCtx, trackReader, s.buildTrackObject(track))

			s.framesProcessed = 0
			s.state = spotifyPlayState
			s.playTrack(ctx, encodedFrames)
			s.state = spotifyStopState
			encodeCancel()
			_, _ = <-encodedFrames

			s.dequeueTrack()
			if s.voiceConnection != nil {
				_ = s.voiceConnection.Speaking(false)
			}
		}
	}
}

// loadTrack will begin asynchronously reading and encoding a track from data supplied in trackReader. Returns the
// []byte channel that it encodes to.
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
				close(encodedFrames)
				return
			default:
				frame, err := encodeSession.OpusFrame()
				if err != nil {
					s.logger.Debug().Interface("track", trackInfo).Msg("finished encoding track")
					close(encodedFrames)
					return
				}
				encodedFrames <- frame
			}
		}
	}(ctx, encodedFrames)

	return encodedFrames
}

// playTrack sends data on the sessions voiceConnection until the channel is closed or the voice connection is found
// to be closed. Supports interrupting sends through the playPause channel.
func (s *spotifySession) playTrack(ctx context.Context, data <-chan []byte) {
	for {
		select {
		case <-ctx.Done():
			return
		case <-s.playPause:
			s.playPause <- true
		case <-s.skipChan:
			return
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

func (s *spotifyQuizGame) getRandomTracks(player *spotify.Player, n int) []spotify.Track {
	if s.rng == nil {
		return nil
	}

	randomIndexes := make(map[int]bool)
	for len(randomIndexes) < n {
		randomIndexes[s.rng.Intn(len(s.playlist))] = true
	}

	var tracks []spotify.Track
	for key, _ := range randomIndexes {
		track, err := player.GetTrackById(s.playlist[key])
		if err != nil {
			return nil
		}

		tracks = append(tracks, track)
	}

	return tracks
}

func (s *spotifyQuizGame) generateQuestion(tracks []spotify.Track) *discordgo.InteractionResponse {
	message := fmt.Sprintf("Question %d:\n```\n", s.questionNumber)
	var actionsRow utils.ActionsRowBuilder
	for index, track := range tracks {
		rowNum := fmt.Sprintf("%d", index+1)
		message += fmt.Sprintf("%s) %s || %s\n", rowNum, track.Name(), track.Artist())
		button := utils.Button().Id(fmt.Sprintf("spotify_quiz_answer_%s", rowNum)).Label(rowNum).Build()
		actionsRow.Button(button)
	}
	message += "```"

	return &discordgo.InteractionResponse{
		Type: discordgo.InteractionResponseChannelMessageWithSource,
		Data: &discordgo.InteractionResponseData{
			Content:    message,
			Components: []discordgo.MessageComponent{actionsRow.Build()},
		},
	}
}

func (s *spotifyQuizGame) generateQuestionWinner() string {
	message := fmt.Sprintf("The correct answer was: `%s || %s`\n", s.questionAnswerTrack.Name(),
		s.questionAnswerTrack.Artist())

	keys, values := s.questionResponseTimes.Items()
	if len(keys) == 0 || len(values) == 0 {
		return message
	}

	sort.SliceStable(keys, func(i, j int) bool {
		return values[i] < values[j]
	})

	sort.SliceStable(values, func(i, j int) bool {
		return values[i] < values[j]
	})

	// Update the winner's score
	noWinner := true
	if responseTime, _ := s.questionResponseTimes.Get(keys[0]); responseTime < 15.0 {
		currentScore, _ := s.scoreboard.Get(keys[0])
		currentScore.score++
		s.scoreboard.Set(keys[0], currentScore)
		noWinner = false
	}

	if noWinner {
		message += fmt.Sprintf("Y'all are dumb :unamused:\n```")
	} else {
		message += fmt.Sprintf("%s answered the fastest <:gottagofast:1013862480478474280>\n```", keys[0])
	}

	for i := 0; i < len(keys); i++ {
		if values[i] < 15.0 {
			message += fmt.Sprintf("%s - %.3fs\n", keys[i], values[i])
		} else {
			message += fmt.Sprintf("%s - WRONG\n", keys[i])
		}
	}
	message += "```"

	return message
}

func (s *spotifyQuizGame) generateGameWinner() string {
	players, scores := s.scoreboard.Items()
	if len(players) == 0 || len(scores) == 0 {
		return "No one was playing."
	}

	sort.SliceStable(players, func(i, j int) bool {
		return scores[i].score > scores[j].score
	})

	sort.SliceStable(scores, func(i, j int) bool {
		return scores[i].score > scores[j].score
	})

	scoreboardMessage := "```"
	for index, _ := range players {
		scoreboardMessage += fmt.Sprintf("%s - %dpts (Total: %.3fs, Avg: %.3fs)\n",
			players[index], scores[index].score, scores[index].totalTime, scores[index].totalTime/float64(s.questions))
	}
	scoreboardMessage += "```"

	winners := []string{players[0]}
	for index := 1; index < len(players); index++ {
		if scores[index].score == scores[0].score {
			if scores[index].totalTime < scores[0].totalTime {
				winners = []string{players[index]}
				scores[0].totalTime = scores[index].totalTime
			} else if scores[index].totalTime == scores[0].totalTime {
				winners = append(winners, players[index])
			}
		}
	}

	var message string
	if len(winners) > 1 {
		message = fmt.Sprintf("%s are the winners!\n", strings.Join(winners, " and "))
	} else {
		message = fmt.Sprintf("%s is the winner!\n", winners[0])
	}

	return message + scoreboardMessage
}
