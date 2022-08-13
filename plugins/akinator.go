package plugins

import (
	"fmt"
	"github.com/bwmarrin/discordgo"
	"github.com/eolso/threadsafe"
	"github.com/olympus-go/athena"
	"github.com/olympus-go/eris/utils"
	"github.com/rs/zerolog"
	"strconv"
	"strings"
	"unicode"
)

const (
	akiStateNil             = 0
	akiStateThemeSelection  = 1
	akiStateAnswerSelection = 2
	akiStateGuessSelection  = 3
	akiStateProcessing      = 4
)

type akinatorGuess struct {
	name     string
	imageUrl string
}

type akinatorSession struct {
	client          *athena.Client
	interaction     *discordgo.Interaction
	ownerId         string
	state           int
	questionLimit   int
	guessThreshold  float64
	currentGuesses  int
	maxGuesses      int
	guessCooldown   int
	guessMessageId  string
	previousGuesses []akinatorGuess
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
			options, ok := utils.GetApplicationCommandOption(applicationCommandData, "21q", "start")
			if !ok {
				return
			}

			// Set defaults and try and fetch options
			questionLimit := 21
			guessThreshold := 85.0
			maxGuesses := 3
			for _, option := range options.Options {
				if option.Name == "questions" {
					v, _ := option.Value.(float64)
					questionLimit = int(v)
				}
				if option.Name == "confidence" {
					v, _ := option.Value.(float64)
					guessThreshold = v
				}
				if option.Name == "guesses" {
					v, _ := option.Value.(float64)
					maxGuesses = int(v)
				}
			}

			userId := utils.GetInteractionUserId(i.Interaction)

			a.logger.Debug().Str("user_id", userId).
				Interface("command", applicationCommandData).Msg("user invoked slash command")

			// Check if the user already has a game running
			if _, ok := a.sessions.Get(userId); ok {
				utils.InteractionResponse(s, i.Interaction).Ephemeral().
					Message("Finish you current game first!").SendWithLog(a.logger)
				return
			}

			utils.InteractionResponse(s, i.Interaction).
				Type(discordgo.InteractionResponseDeferredChannelMessageWithSource).SendWithLog(a.logger)

			// Spin up the client
			client, err := athena.NewClient()
			if err != nil {
				a.logger.Error().Err(err).Msg("failed to create akinator client")
				utils.InteractionResponse(s, i.Interaction).Message("Something went wrong.").
					Flags(discordgo.MessageFlagsEphemeral).EditWithLog(a.logger)
				return
			}

			a.sessions.Set(userId, &akinatorSession{
				client:          client,
				interaction:     i.Interaction,
				ownerId:         userId,
				state:           akiStateThemeSelection,
				questionLimit:   questionLimit,
				guessThreshold:  guessThreshold,
				currentGuesses:  0,
				maxGuesses:      maxGuesses,
				guessCooldown:   0,
				previousGuesses: []akinatorGuess{{name: "Ashley Wsfd"}},
			})

			// Update the interaction with the initial theme selection
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
				case akiStateThemeSelection:
					break
				case akiStateProcessing:
					utils.InteractionResponse(s, i.Interaction).Ephemeral().Message("Please wait, I'm thinking...").
						SendWithLog(a.logger)
					return
				default:
					utils.InteractionResponse(s, i.Interaction).Ephemeral().Message("Invalid button for game state.").
						SendWithLog(a.logger)
					return
				}

				// If we got this far without returning then let the user know we're thinking
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
						FollowUpCreate()
					return
				}

				// Start the game with the theme of choice
				if _, err = gameSession.client.NewGame(a.themes[themeIndex]); err != nil {
					a.logger.Error().Err(err).Msg("could not start game")
					utils.InteractionResponse(s, i.Interaction).Ephemeral().Message("Something went wrong.").
						FollowUpCreate()
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
				case akiStateAnswerSelection:
					break
				case akiStateProcessing:
					utils.InteractionResponse(s, i.Interaction).Ephemeral().Message("Please wait, I'm thinking...").
						SendWithLog(a.logger)
					return
				default:
					utils.InteractionResponse(s, i.Interaction).Ephemeral().Message("Invalid button for game state.").
						SendWithLog(a.logger)
					return
				}

				utils.InteractionResponse(s, i.Interaction).Type(discordgo.InteractionResponseDeferredMessageUpdate).
					SendWithLog(a.logger)

				gameSession.state = akiStateProcessing

				// Update response to show thinking and disable the buttons
				utils.InteractionResponse(s, i.Interaction).
					Message("<a:loading:1005279530438623272> George Tuney is thinking...").
					Components(gameSession.questionButtons(false)).EditWithLog(a.logger)

				answer, err := strconv.Atoi(selection)
				if err != nil {
					a.logger.Error().Err(err).Str("answer_index", selection).Msg("unexpected answer index received")
					utils.InteractionResponse(s, i.Interaction).Message(":x: Something went wrong.").
						FollowUpCreate()
					return
				}

				// Submit the answer to the client and fetch the new question
				if _, err = gameSession.client.Answer(answer); err != nil {
					a.logger.Error().Err(err).Msg("failed to fetch answer")
					utils.InteractionResponse(s, i.Interaction).Ephemeral().Message("Something went wrong.").
						FollowUpCreate()
					return
				}

				// End state check
				if (gameSession.client.Step()+1 > gameSession.questionLimit ||
					gameSession.client.Progress() >= gameSession.guessThreshold) && gameSession.guessCooldown <= 0 {

					utils.InteractionResponse(s, i.Interaction).Components(gameSession.questionButtons(false)).
						EditWithLog(a.logger)

					gameSession.state = akiStateProcessing
					// Get the first guess available to the client that hasn't been guessed before
					guess, ok := gameSession.getGuess()
					if !ok {
						// If there are no guesses, and we're already at our wits end, just give up
						if gameSession.currentGuesses >= gameSession.maxGuesses || gameSession.client.Step()+1 > 99 {
							utils.InteractionResponse(s, gameSession.interaction).DeleteWithLog(a.logger)
							utils.InteractionResponse(s, i.Interaction).Components().FollowUpEdit(gameSession.guessMessageId)
							utils.InteractionResponse(s, i.Interaction).Message("I give up. You win :disappointed:").FollowUpCreate()
							a.sessions.Delete(ownerId)
						} else {
							// Otherwise let's just roll it back and pretend like nothing happened hehe
							_ = gameSession.client.Undo()
							gameSession.guessCooldown = 3

							utils.InteractionResponse(s, gameSession.interaction).Message(gameSession.questionStr()).
								Components(gameSession.questionButtons(true)).EditWithLog(a.logger)

							gameSession.state = akiStateAnswerSelection
						}
						return
					}

					// Send the user our guess
					embed := utils.MessageEmbed().Title(guess.name).Image(guess.imageUrl).Build()
					message, err := utils.InteractionResponse(s, i.Interaction).Message("You're thinking of...").
						Embeds(embed).Components(gameSession.guessButtons(true)).FollowUpCreate()
					if err != nil {
						a.logger.Error().Err(err).Msg("failed to send guess as followup message")
						utils.InteractionResponse(s, i.Interaction).Components().
							Message("Something went wrong.").FollowUpCreate()
						a.cleanupSession(s, ownerId)
						return
					}

					// Update internal state to await for the user response to guess
					gameSession.interaction = i.Interaction
					gameSession.guessMessageId = message.ID
					gameSession.currentGuesses += 1
					gameSession.state = akiStateGuessSelection
					gameSession.previousGuesses = append(gameSession.previousGuesses, guess)

					return
				}

				// We're not in an end state, so let's update the message with the new question and continue
				utils.InteractionResponse(s, i.Interaction).Message(gameSession.questionStr()).
					Components(gameSession.questionButtons(true)).EditWithLog(a.logger)

				gameSession.guessCooldown--
				gameSession.state = akiStateAnswerSelection

				return
			} else if strings.HasPrefix(messageComponentData.CustomID, "21q_guess_") {
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
				case akiStateGuessSelection:
					break
				case akiStateProcessing:
					utils.InteractionResponse(s, i.Interaction).Ephemeral().Message("Please wait, I'm thinking...").
						SendWithLog(a.logger)
					return
				default:
					utils.InteractionResponse(s, i.Interaction).Ephemeral().Message("Invalid button for game state.").
						SendWithLog(a.logger)
					return
				}

				utils.InteractionResponse(s, i.Interaction).
					Type(discordgo.InteractionResponseDeferredMessageUpdate).SendWithLog(a.logger)

				if selection == "yes" {
					// Woo the guess was marked as correct. Time to celebrate and clean up.
					utils.InteractionResponse(s, gameSession.interaction).DeleteWithLog(a.logger)
					_, _ = utils.InteractionResponse(s, gameSession.interaction).Components().
						FollowUpEdit(gameSession.guessMessageId)
					utils.InteractionResponse(s, i.Interaction).Message(":tada:").FollowUpCreate()
					a.sessions.Delete(ownerId)
				} else if selection == "no" {
					// The guess was wrong, so let's check our current status and determine if we should give up.
					if gameSession.currentGuesses >= gameSession.maxGuesses || gameSession.client.Step()+1 > 99 {
						utils.InteractionResponse(s, gameSession.interaction).DeleteWithLog(a.logger)
						utils.InteractionResponse(s, i.Interaction).Components().FollowUpEdit(gameSession.guessMessageId)
						utils.InteractionResponse(s, i.Interaction).Message("I give up. You win :disappointed:").FollowUpCreate()
						a.sessions.Delete(ownerId)
					} else {
						err := utils.InteractionResponse(s, gameSession.interaction).FollowUpDelete(gameSession.guessMessageId)
						if err != nil {
							a.logger.Error().Err(err).Str("message_id", gameSession.guessMessageId).
								Msg("failed to delete follow up message")
						}

						_ = gameSession.client.Undo()
						gameSession.guessMessageId = ""
						gameSession.guessCooldown = 3
						gameSession.questionLimit += 21

						utils.InteractionResponse(s, gameSession.interaction).Message(gameSession.questionStr()).
							Components(gameSession.questionButtons(true)).EditWithLog(a.logger)

						gameSession.state = akiStateAnswerSelection
					}
				}
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

			a.cleanupSession(s, userId)
			utils.InteractionResponse(s, i.Interaction).Flags(discordgo.MessageFlagsEphemeral).
				Message(":wave:").SendWithLog(a.logger)

			return
		}
	}

	return handlers
}

func (a *AkinatorPlugin) Commands() map[string]*discordgo.ApplicationCommand {
	commands := make(map[string]*discordgo.ApplicationCommand)

	minQuestions := float64(1)
	maxQuestions := float64(99)
	minConfidence := float64(1)
	maxConfidence := 99.0
	minGuess := float64(1)
	maxGuess := float64(5)

	commands["aki_cmd"] = &discordgo.ApplicationCommand{
		Name:        "21q",
		Description: "21 questions like game",
		Options: []*discordgo.ApplicationCommandOption{
			{
				Name:        "start",
				Description: "Starts a new game of 21* questions",
				Type:        discordgo.ApplicationCommandOptionSubCommand,
				Options: []*discordgo.ApplicationCommandOption{
					{
						Name:        "questions",
						Description: "Limit the number of questions asked before guessing (default = 21)",
						Type:        discordgo.ApplicationCommandOptionInteger,
						Required:    false,
						MinValue:    &minQuestions,
						MaxValue:    maxQuestions,
					},
					{
						Name:        "confidence",
						Description: "Set the confidence threshold needed before guessing (default = 85.0)",
						Type:        discordgo.ApplicationCommandOptionNumber,
						Required:    false,
						MinValue:    &minConfidence,
						MaxValue:    maxConfidence,
					},
					{
						Name:        "guesses",
						Description: "Set the number of guess attempts before giving up (default = 3)",
						Type:        discordgo.ApplicationCommandOptionInteger,
						Required:    false,
						MinValue:    &minGuess,
						MaxValue:    maxGuess,
					},
				},
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

func (a *AkinatorPlugin) cleanupSession(session *discordgo.Session, id string) {
	if gameSession, ok := a.sessions.Get(id); ok {
		if gameSession.interaction != nil {
			utils.InteractionResponse(session, gameSession.interaction).DeleteWithLog(a.logger)
			if gameSession.guessMessageId != "" {
				_ = utils.InteractionResponse(session, gameSession.interaction).FollowUpDelete(gameSession.guessMessageId)
			}
		}
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

func (a *akinatorSession) guessButtons(enabled bool) discordgo.ActionsRow {
	var actionRowBuilder utils.ActionsRowBuilder
	for _, selection := range []string{"Yes", "No"} {
		id := fmt.Sprintf("21q_guess_%s_%s", strings.ToLower(selection), a.ownerId)
		button := utils.Button().Label(selection).Id(id).Enabled(enabled).Build()
		actionRowBuilder.Button(button)
	}
	return actionRowBuilder.Build()
}

func (a *akinatorSession) getGuess() (akinatorGuess, bool) {
	response, err := a.client.ListGuesses()
	if err != nil {
		return akinatorGuess{}, false
	}

	if len(response.Parameters.Elements) == 0 {
		return akinatorGuess{}, false
	}

	var newGuess akinatorGuess
	for _, guess := range response.Parameters.Elements {
		previouslyGuessed := false
		for _, previousGuess := range a.previousGuesses {
			if guess.Element.Name == previousGuess.name {
				previouslyGuessed = true
				break
			}
		}
		if !previouslyGuessed {
			newGuess.name = guess.Element.Name
			newGuess.imageUrl = guess.Element.AbsolutePicturePath
			break
		}
	}

	return newGuess, newGuess.name != ""
}
