package plugins

import (
	"fmt"
	"github.com/bwmarrin/discordgo"
	"github.com/eolso/threadsafe"
	"github.com/olympus-go/athena"
	"github.com/olympus-go/eris/utils"
	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
	"strconv"
	"strings"
	"unicode"
)

const (
	akiStateNil             = 0
	akiStateThemeSelection  = 1
	akiStateAnswerSelection = 2
	akiStateProcessing      = 3
)

type akinatorSession struct {
	client         *athena.Client
	interaction    *discordgo.Interaction
	ownerId        string
	state          int
	questionLimit  int
	guessThreshold float64
}

type AkinatorPlugin struct {
	sessions *threadsafe.Map[string, *akinatorSession]
	themes   []athena.Theme
	logger   zerolog.Logger
}

func Akinator(logger zerolog.Logger) *AkinatorPlugin {
	themes, _ := athena.GetThemes()

	return &AkinatorPlugin{
		sessions: threadsafe.NewMap[string, *akinatorSession](),
		themes:   themes,
		logger:   logger.With().Str("plugin", "akinator").Logger(),
	}
}

func (a *AkinatorPlugin) Name() string {
	return "Akinator"
}

func (a *AkinatorPlugin) Description() string {
	return "Play a 21 question game"
}

func (a *AkinatorPlugin) Handlers() map[string]any {
	handlers := make(map[string]any)

	handlers["aki_start_handler"] = func(s *discordgo.Session, i *discordgo.InteractionCreate) {
		switch i.Type {
		case discordgo.InteractionApplicationCommand:
			applicationCommandData := i.ApplicationCommandData()
			if _, ok := utils.GetApplicationCommandOption(applicationCommandData, "21q", "start"); !ok {
				return
			}

			userId := utils.GetInteractionUserId(i.Interaction)

			a.logger.Debug().Str("user_id", userId).
				Interface("command", applicationCommandData).Msg("user invoked slash command")

			if _, ok := a.sessions.Get(userId); ok {
				utils.InteractionResponse(s, i.Interaction).Ephemeral().
					Message("Finish you current game first!").SendWithLog(a.logger)
				return
			}

			utils.InteractionResponse(s, i.Interaction).
				Message("<a:loading:1005279530438623272> Loading...").SendWithLog(a.logger)

			client, err := athena.NewClient()
			if err != nil {
				log.Error().Err(err).Msg("failed to create akinator client")
				utils.InteractionResponse(s, i.Interaction).Message("Something went wrong.").
					Flags(discordgo.MessageFlagsEphemeral).SendWithLog(a.logger)
				return
			}

			a.sessions.Set(userId, &akinatorSession{
				client:         client,
				ownerId:        userId,
				state:          akiStateThemeSelection,
				questionLimit:  40,
				guessThreshold: 90.0,
			})

			utils.InteractionResponse(s, i.Interaction).Message("Select a theme").
				Components(a.themeButtons(userId, true)).EditWithLog(a.logger)

			return
		case discordgo.InteractionMessageComponent:
			messageComponentData := i.MessageComponentData()
			if strings.HasPrefix(messageComponentData.CustomID, "21q_theme") {
				idSplit := strings.Split(messageComponentData.CustomID, "_")
				if len(idSplit) != 4 {
					utils.InteractionResponse(s, i.Interaction).Ephemeral().Message("Something went wrong.").
						SendWithLog(a.logger)
					return
				}

				selection := idSplit[2]
				ownerId := idSplit[3]
				userId := utils.GetInteractionUserId(i.Interaction)

				a.logger.Debug().Str("user_id", userId).
					Interface("component", messageComponentData).Msg("user interacted with component")

				if userId != ownerId {
					utils.InteractionResponse(s, i.Interaction).Ephemeral().Message("This isn't your game :bell:").
						SendWithLog(a.logger)
					return
				}

				gameSession, ok := a.sessions.Get(userId)
				if !ok {
					utils.InteractionResponse(s, i.Interaction).Ephemeral().Message("Game no longer exists.").
						SendWithLog(a.logger)
					return
				}

				switch gameSession.state {
				case akiStateAnswerSelection:
					utils.InteractionResponse(s, i.Interaction).Ephemeral().Message("Invalid button for game state.").
						SendWithLog(a.logger)
					return
				case akiStateProcessing:
					utils.InteractionResponse(s, i.Interaction).Ephemeral().Message("Please wait, I'm thinking...").
						SendWithLog(a.logger)
					return
				}

				utils.InteractionResponse(s, i.Interaction).Type(discordgo.InteractionResponseDeferredMessageUpdate).
					SendWithLog(a.logger)

				gameSession.state = akiStateProcessing
				utils.InteractionResponse(s, i.Interaction).
					Message("<a:loading:1005279530438623272> Starting game...").
					Components(a.themeButtons(gameSession.ownerId, false)).EditWithLog(a.logger)

				themeIndex, err := strconv.Atoi(selection)
				if err != nil {
					a.logger.Error().Err(err).Str("theme_index", selection).Msg("unexpected theme index received")
					utils.InteractionResponse(s, i.Interaction).Ephemeral().Message("Something went wrong.").
						SendWithLog(a.logger)
					return
				}

				if _, err = gameSession.client.NewGame(a.themes[themeIndex]); err != nil {
					a.logger.Error().Err(err).Msg("could not start game")
					utils.InteractionResponse(s, i.Interaction).Ephemeral().Message("Something went wrong.").
						SendWithLog(a.logger)
					return
				}

				utils.InteractionResponse(s, i.Interaction).Message(gameSession.questionStr()).
					Components(gameSession.questionButtons(true)).EditWithLog(a.logger)

				gameSession.state = akiStateAnswerSelection

				return
			} else if strings.HasPrefix(messageComponentData.CustomID, "21q_answer_") {
				idSplit := strings.Split(messageComponentData.CustomID, "_")
				if len(idSplit) != 4 {
					utils.InteractionResponse(s, i.Interaction).Ephemeral().Message("Something went wrong.").
						SendWithLog(a.logger)
					return
				}

				selection := idSplit[2]
				ownerId := idSplit[3]
				userId := utils.GetInteractionUserId(i.Interaction)

				a.logger.Debug().Str("user_id", userId).
					Interface("component", messageComponentData).Msg("user interacted with component")

				if userId != ownerId {
					utils.InteractionResponse(s, i.Interaction).Ephemeral().Message("This isn't your game :bell:").
						SendWithLog(a.logger)
					return
				}

				gameSession, ok := a.sessions.Get(userId)
				if !ok {
					utils.InteractionResponse(s, i.Interaction).Ephemeral().Message("Game no longer exists.").
						SendWithLog(a.logger)
					return
				}

				switch gameSession.state {
				case akiStateThemeSelection:
					utils.InteractionResponse(s, i.Interaction).Ephemeral().Message("Invalid button for game state.").
						SendWithLog(a.logger)
					return
				case akiStateProcessing:
					utils.InteractionResponse(s, i.Interaction).Ephemeral().Message("Please wait, I'm thinking...").
						SendWithLog(a.logger)
					return
				}

				utils.InteractionResponse(s, i.Interaction).Type(discordgo.InteractionResponseDeferredMessageUpdate).
					SendWithLog(a.logger)

				gameSession.state = akiStateProcessing

				utils.InteractionResponse(s, i.Interaction).
					Message("<a:loading:1005279530438623272> George Tuney is thinking...").
					Components(gameSession.questionButtons(false)).EditWithLog(a.logger)

				answer, err := strconv.Atoi(selection)
				if err != nil {
					a.logger.Error().Err(err).Str("answer_index", selection).Msg("unexpected answer index received")
					utils.InteractionResponse(s, i.Interaction).Message(":x: Something went wrong.").
						SendWithLog(a.logger)
					return
				}

				if _, err = gameSession.client.Answer(answer); err != nil {
					utils.InteractionResponse(s, i.Interaction).Ephemeral().Message("Something went wrong.").
						SendWithLog(a.logger)
					return
				}

				if gameSession.client.Progress() >= gameSession.guessThreshold ||
					gameSession.client.Step()+1 > gameSession.questionLimit {
					utils.InteractionResponse(s, i.Interaction).DeleteWithLog(a.logger)

					guessResponse, err := gameSession.client.ListGuesses()
					if err != nil {
						a.logger.Error().Err(err).Msg("failed to fetch guesses")
						utils.InteractionResponse(s, i.Interaction).
							Message("Something went wrong.").
							Components(utils.ActionsRow().Build()).EditWithLog(a.logger)
						return
					}
					guesses := guessResponse.Parameters.Elements

					if len(guesses) == 0 {
						_, _ = s.ChannelMessageSend(i.ChannelID, "I give up. You win.")
					} else {
						response := fmt.Sprintf("You're thinking of: %s", guesses[0].Element.Name)
						_, _ = s.ChannelMessageSend(i.ChannelID, response)
						_, _ = s.ChannelMessageSend(i.ChannelID, guesses[0].Element.AbsolutePicturePath)
					}

					gameSession.client = nil
					return
				}

				utils.InteractionResponse(s, i.Interaction).Message(gameSession.questionStr()).
					Components(gameSession.questionButtons(true)).EditWithLog(a.logger)

				gameSession.state = akiStateAnswerSelection

				return
			}
		}
	}

	handlers["aki_history_handler"] = func(s *discordgo.Session, i *discordgo.InteractionCreate) {
		switch i.Type {
		case discordgo.InteractionApplicationCommand:
			applicationCommandData := i.ApplicationCommandData()
			if _, ok := utils.GetApplicationCommandOption(applicationCommandData, "21q", "history"); !ok {
				return
			}

			userId := utils.GetInteractionUserId(i.Interaction)

			a.logger.Debug().Str("user_id", userId).
				Interface("command", applicationCommandData).Msg("user invoked slash command")

			gameSession, ok := a.sessions.Get(userId)
			if !ok {
				utils.InteractionResponse(s, i.Interaction).Flags(discordgo.MessageFlagsEphemeral).
					Message("No game is currently running.").SendWithLog(a.logger)
				return
			}

			responseStr := ""
			if len(gameSession.client.Selections) == 0 {
				responseStr = "No history yet"
			} else {
				for index, selection := range gameSession.client.Selections {
					responseStr += fmt.Sprintf("%d) %s %s\n", index+1, selection.Question, selection.Answer)
				}
			}

			utils.InteractionResponse(s, i.Interaction).Flags(discordgo.MessageFlagsEphemeral).
				Message("```" + responseStr + "```").SendWithLog(a.logger)

			return
		}

	}

	handlers["aki_stop_handler"] = func(s *discordgo.Session, i *discordgo.InteractionCreate) {
		switch i.Type {
		case discordgo.InteractionApplicationCommand:
			applicationCommandData := i.ApplicationCommandData()
			if _, ok := utils.GetApplicationCommandOption(applicationCommandData, "21q", "stop"); !ok {
				return
			}

			userId := utils.GetInteractionUserId(i.Interaction)

			a.logger.Debug().Str("user_id", userId).
				Interface("command", applicationCommandData).Msg("user invoked slash command")

			if _, ok := a.sessions.Get(userId); !ok {
				utils.InteractionResponse(s, i.Interaction).Ephemeral().
					Message("No game is currently running.").SendWithLog(a.logger)
				return
			}

			a.deleteSession(s, userId)
			utils.InteractionResponse(s, i.Interaction).Flags(discordgo.MessageFlagsEphemeral).
				Message(":wave:").SendWithLog(a.logger)

			return
		}
	}

	return handlers
}

func (a *AkinatorPlugin) Commands() map[string]*discordgo.ApplicationCommand {
	commands := make(map[string]*discordgo.ApplicationCommand)

	commands["aki_cmd"] = &discordgo.ApplicationCommand{
		Name:        "21q",
		Description: "21 questions like game",
		Options: []*discordgo.ApplicationCommandOption{
			{
				Name:        "start",
				Description: "Starts a new game of 21* questions",
				Type:        discordgo.ApplicationCommandOptionSubCommand,
			},
			{
				Name:        "stop",
				Description: "Stops the current running game of 21* questions",
				Type:        discordgo.ApplicationCommandOptionSubCommand,
			},
			{
				Name:        "history",
				Description: "Prints the current running game's Selection history",
				Type:        discordgo.ApplicationCommandOptionSubCommand,
			},
		},
	}

	return commands
}

func (a *AkinatorPlugin) Intents() []discordgo.Intent {
	return nil
}

func (a *AkinatorPlugin) themeButtons(userId string, enabled bool) discordgo.ActionsRow {
	var actionRowBuilder utils.ActionsRowBuilder
	for index, theme := range a.themes {
		themeName := []rune(strings.ToLower(theme.Name))
		if len(themeName) > 0 {
			themeName[0] = unicode.ToUpper(themeName[0])
		}
		button := utils.Button().Label(string(themeName)).Id(fmt.Sprintf("21q_theme_%d_%s", index, userId)).
			Enabled(enabled).Build()
		actionRowBuilder.Button(button)
	}

	return actionRowBuilder.Build()
}

func (a *AkinatorPlugin) deleteSession(session *discordgo.Session, id string) {
	if _, ok := a.sessions.Get(id); ok {
		//utils.InteractionResponse(session, gameSession.interaction).DeleteWithLog(a.logger)
		a.sessions.Delete(id)
	}
}

func (a *akinatorSession) questionStr() string {
	return fmt.Sprintf("%d) %s [%.1f]", a.client.Step()+1, a.client.Question(), a.client.Progress())
}

func (a *akinatorSession) questionButtons(enabled bool) discordgo.ActionsRow {
	var actionRowBuilder utils.ActionsRowBuilder
	for index, answer := range a.client.Answers() {
		button := utils.Button().Label(answer).Id(fmt.Sprintf("21q_answer_%d_%s", index, a.ownerId)).
			Enabled(enabled).Build()
		actionRowBuilder.Button(button)
	}

	return actionRowBuilder.Build()
}
