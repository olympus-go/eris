package plugins

import (
	"fmt"
	"github.com/bwmarrin/discordgo"
	"github.com/olympus-go/athena"
	"github.com/olympus-go/eris/utils"
	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
	"strconv"
	"strings"
	"unicode"
)

type AkinatorPlugin struct {
	client          *athena.Client
	themes          []athena.Theme
	lastInteraction *discordgo.Interaction
	gameOwnerId     string
	processing      bool
	questionLimit   int
	guessThreshold  float64
	logger          zerolog.Logger
}

func Akinator(logger zerolog.Logger) *AkinatorPlugin {
	themes, _ := athena.GetThemes()

	return &AkinatorPlugin{
		themes:         themes,
		questionLimit:  40,
		guessThreshold: 90.0,
		logger:         logger.With().Str("plugin", "akinator").Logger(),
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

			a.logger.Debug().Str("user_id", utils.GetInteractionUserId(i.Interaction)).
				Interface("command", applicationCommandData).Msg("user invoked slash command")

			if a.client != nil {
				utils.InteractionResponse(s, i.Interaction).Message("Game is already running.").
					Flags(discordgo.MessageFlagsEphemeral).SendWithLog(a.logger)
				return
			} else {
				utils.InteractionResponse(s, i.Interaction).
					Message("<a:loading:1005279530438623272> Loading...").SendWithLog(a.logger)

				var err error
				a.client, err = athena.NewClient()
				if err != nil {
					log.Error().Err(err).Msg("failed to create akinator client")
					utils.InteractionResponse(s, i.Interaction).Message("Something went wrong.").
						Flags(discordgo.MessageFlagsEphemeral).SendWithLog(a.logger)
					return
				}
				a.gameOwnerId = utils.GetInteractionUserId(i.Interaction)

				utils.InteractionResponse(s, i.Interaction).Message("Select a theme").
					Components(a.themeButtons(true)).EditWithLog(a.logger)

				return
			}
		case discordgo.InteractionMessageComponent:
			messageComponentData := i.MessageComponentData()
			if strings.HasPrefix(messageComponentData.CustomID, "21q_theme") {
				if utils.GetInteractionUserId(i.Interaction) != a.gameOwnerId {
					_ = utils.InteractionResponse(s, i.Interaction).Flags(discordgo.MessageFlagsEphemeral).
						Message("Please wait until this round is finished to start a new game.").Send()
					return
				} else {
					if a.processing {
						_ = utils.InteractionResponse(s, i.Interaction).Flags(discordgo.MessageFlagsEphemeral).
							Message("Please wait, I'm thinking...").Send()
						return
					}
				}

				a.logger.Debug().Str("user_id", utils.GetInteractionUserId(i.Interaction)).
					Interface("component", messageComponentData).Msg("user interacted with component")

				utils.InteractionResponse(s, i.Interaction).Type(discordgo.InteractionResponseDeferredMessageUpdate).
					SendWithLog(a.logger)

				a.processing = true
				utils.InteractionResponse(s, i.Interaction).
					Message("<a:loading:1005279530438623272> Starting game...").
					Components(a.themeButtons(false)).EditWithLog(a.logger)

				// TODO do all the bound checking goodness. this is unsafe
				idSplit := strings.Split(messageComponentData.CustomID, "_")
				themeIndex, _ := strconv.Atoi(idSplit[2])
				a.client.NewGame(a.themes[themeIndex])

				utils.InteractionResponse(s, i.Interaction).Message(a.questionStr()).
					Components(a.questionButtons(true)).EditWithLog(a.logger)
				a.processing = false

				return
			} else if strings.HasPrefix(messageComponentData.CustomID, "21q_answer_") {
				if a.processing {
					utils.InteractionResponse(s, i.Interaction).Flags(discordgo.MessageFlagsEphemeral).
						Message("Please wait, I'm thinking...").SendWithLog(a.logger)
					return
				}

				a.logger.Debug().Str("user_id", utils.GetInteractionUserId(i.Interaction)).
					Interface("component", messageComponentData).Msg("user interacted with component")

				utils.InteractionResponse(s, i.Interaction).Type(discordgo.InteractionResponseDeferredMessageUpdate).
					SendWithLog(a.logger)

				a.processing = true

				utils.InteractionResponse(s, i.Interaction).
					Message("<a:loading:1005279530438623272> George Tuney is thinking...").
					Components(a.questionButtons(false)).EditWithLog(a.logger)

				// TODO do all the bound checking goodness. this is unsafe
				idSplit := strings.Split(messageComponentData.CustomID, "_")
				answer, _ := strconv.Atoi(idSplit[2])
				_ = a.client.Answer(answer)

				if a.client.Progress() >= a.guessThreshold || a.client.Step()+1 > a.questionLimit {
					_ = utils.InteractionResponse(s, i.Interaction).Delete()

					guesses := a.client.ListGuesses().Parameters.Elements
					if len(guesses) == 0 {
						_, _ = s.ChannelMessageSend(i.ChannelID, "I give up. You win.")
					} else {
						response := fmt.Sprintf("You're thinking of: %s", guesses[0].Element.Name)
						_, _ = s.ChannelMessageSend(i.ChannelID, response)
						_, _ = s.ChannelMessageSend(i.ChannelID, guesses[0].Element.AbsolutePicturePath)
					}

					a.client = nil
					return
				}

				utils.InteractionResponse(s, i.Interaction).Message(a.questionStr()).
					Components(a.questionButtons(true)).EditWithLog(a.logger)

				a.processing = false

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

			a.logger.Debug().Str("user_id", utils.GetInteractionUserId(i.Interaction)).
				Interface("command", applicationCommandData).Msg("user invoked slash command")

			if a.client == nil {
				_ = utils.InteractionResponse(s, i.Interaction).Flags(discordgo.MessageFlagsEphemeral).
					Message("No game is currently running.").Send()
			} else {
				responseStr := ""
				if len(a.client.Selections) == 0 {
					responseStr = "No history yet"
				} else {
					for index, selection := range a.client.Selections {
						responseStr += fmt.Sprintf("%d) %s %s\n", index+1, selection.Question, selection.Answer)
					}
				}
				_ = utils.InteractionResponse(s, i.Interaction).Flags(discordgo.MessageFlagsEphemeral).
					Message("```" + responseStr + "```").Send()
			}

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

			a.logger.Debug().Str("user_id", utils.GetInteractionUserId(i.Interaction)).
				Interface("command", applicationCommandData).Msg("user invoked slash command")

			if a.client == nil {
				_ = utils.InteractionResponse(s, i.Interaction).Flags(discordgo.MessageFlagsEphemeral).
					Message("No game is currently running.").Send()
			} else {
				a.client = nil
				a.gameOwnerId = ""
				_ = utils.InteractionResponse(s, i.Interaction).Flags(discordgo.MessageFlagsEphemeral).
					Message(":wave:").Send()
			}

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
				Description: "Prints the current running game's selection history",
				Type:        discordgo.ApplicationCommandOptionSubCommand,
			},
		},
	}

	return commands
}

func (a *AkinatorPlugin) Intents() []discordgo.Intent {
	return nil
}

func (a *AkinatorPlugin) themeButtons(enabled bool) discordgo.ActionsRow {
	var actionRowBuilder utils.ActionsRowBuilder
	for index, theme := range a.themes {
		themeName := []rune(strings.ToLower(theme.Name))
		if len(themeName) > 0 {
			themeName[0] = unicode.ToUpper(themeName[0])
		}
		button := utils.Button().Label(string(themeName)).Id(fmt.Sprintf("21q_theme_%d", index)).Enabled(enabled).Build()
		actionRowBuilder.Button(button)
	}

	return actionRowBuilder.Build()
}

func (a *AkinatorPlugin) questionStr() string {
	return fmt.Sprintf("%d) %s [%.1f]", a.client.Step()+1, a.client.Question(), a.client.Progress())
}

func (a *AkinatorPlugin) questionButtons(enabled bool) discordgo.ActionsRow {
	var actionRowBuilder utils.ActionsRowBuilder
	for index, answer := range a.client.Answers() {
		button := utils.Button().Label(answer).Id(fmt.Sprintf("21q_answer_%d", index)).Enabled(enabled).Build()
		actionRowBuilder.Button(button)
	}

	return actionRowBuilder.Build()
}
