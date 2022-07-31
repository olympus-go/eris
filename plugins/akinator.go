package plugins

import (
	"fmt"
	"github.com/bwmarrin/discordgo"
	"github.com/eolso/athena"
	"github.com/eolso/eris"
	"github.com/rs/zerolog/log"
	"strings"
)

var themeIdMap = map[string]athena.Theme{
	"21q_theme_characters": athena.CharactersTheme,
	"21q_theme_animals":    athena.AnimalsTheme,
	"21q_theme_objects":    athena.ObjectsTheme,
}

var answerIdMap = map[string]int{
	"21q_answer_yes":          0,
	"21q_answer_no":           1,
	"21q_answer_dont_know":    2,
	"21q_answer_probably":     3,
	"21q_answer_probably_not": 4,
}

type akinator struct {
	client          *athena.Client
	lastInteraction *discordgo.Interaction
	gameOwnerId     string
	processing      bool
	questionLimit   int
	guessThreshold  float64
	//handlers   map[string]any
}

func Akinator() eris.Plugin {
	return &akinator{
		questionLimit:  40,
		guessThreshold: 90.0,
	}
}

func (a *akinator) Name() string {
	return "Akinator"
}

func (a *akinator) Description() string {
	return "Play a 21 question game"
}

func (a *akinator) Handlers() map[string]any {
	handlers := make(map[string]any)

	handlers["aki_start_handler"] = func(s *discordgo.Session, i *discordgo.InteractionCreate) {
		switch i.Type {
		case discordgo.InteractionApplicationCommand:
			applicationCommandData := i.ApplicationCommandData()
			if applicationCommandData.Name == "21q" {
				// This should never happen if we defined the commands correctly, but probably shouldn't OoB ourselves
				if len(i.ApplicationCommandData().Options) == 0 {
					return
				}

				switch applicationCommandData.Options[0].Name {
				case "start":
					if a.client != nil {
						_ = s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
							Type: discordgo.InteractionResponseChannelMessageWithSource,
							Data: &discordgo.InteractionResponseData{
								Content: "Game is already running",
							},
						})
					} else {
						a.client = athena.NewClient()
						if i.Member != nil {
							a.gameOwnerId = i.Member.User.ID
						}
						err := s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
							Type: discordgo.InteractionResponseChannelMessageWithSource,
							Data: a.generateThemeResponseData(),
						})
						if err != nil {
							log.Error().Err(err).Msg("failed to respond to interaction")
						}
					}
					return
				case "history":
					if a.client == nil {
						_ = s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
							Type: discordgo.InteractionResponseChannelMessageWithSource,
							Data: &discordgo.InteractionResponseData{
								Content: "No game is currently running",
							},
						})
					} else {
						responseStr := ""
						if len(a.client.Selections) == 0 {
							responseStr = "No history yet"
						} else {
							for index, selection := range a.client.Selections {
								responseStr += fmt.Sprintf("%d) %s %s\n", index+1, selection.Question, selection.Answer)
							}
						}
						_ = s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
							Type: discordgo.InteractionResponseChannelMessageWithSource,
							Data: &discordgo.InteractionResponseData{
								Content: fmt.Sprintf("```%s```", responseStr),
							},
						})
					}
					return
				case "stop":
					if a.client == nil {
						_ = s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
							Type: discordgo.InteractionResponseChannelMessageWithSource,
							Data: &discordgo.InteractionResponseData{
								Content: "No game is currently running",
							},
						})
					} else {
						a.client = nil
						a.gameOwnerId = ""
						_ = s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
							Type: discordgo.InteractionResponseChannelMessageWithSource,
							Data: &discordgo.InteractionResponseData{
								Content: "Bye bye!",
							},
						})
					}
					return
				default:
				}
			}
		case discordgo.InteractionMessageComponent:
			if strings.HasPrefix(i.MessageComponentData().CustomID, "21q_theme") {
				if i.Member != nil && i.Member.User.ID != a.gameOwnerId {
					_ = s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
						Type: discordgo.InteractionResponseChannelMessageWithSource,
						Data: &discordgo.InteractionResponseData{
							Content: "Please wait until this round is finished to start your own game.",
							Flags:   uint64(discordgo.MessageFlagsEphemeral),
						},
					})
					return
				} else {
					if a.processing {
						_ = s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
							Type: discordgo.InteractionResponseChannelMessageWithSource,
							Data: &discordgo.InteractionResponseData{
								Content: "Please wait, I'm thinking...",
								Flags:   uint64(discordgo.MessageFlagsEphemeral),
							},
						})
						return
					}
				}

				a.processing = true
				err := s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
					Type: discordgo.InteractionResponseDeferredMessageUpdate,
				})
				if err != nil {
					log.Error().Err(err).Msg("failed to respond to interaction")
				}

				a.client.NewGame(themeIdMap[i.MessageComponentData().CustomID])

				interactionResponse, _ := s.InteractionResponse(i.Interaction)
				questionResponse := a.generateQuestionResponseData(false)
				messageEdit := discordgo.NewMessageEdit(interactionResponse.ChannelID, interactionResponse.ID)
				messageEdit.Content = &questionResponse.Content
				messageEdit.Components = questionResponse.Components
				_, _ = s.ChannelMessageEditComplex(messageEdit)
				a.processing = false

				return
			} else if strings.HasPrefix(i.MessageComponentData().CustomID, "21q_answer_") {
				_ = s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
					Type: discordgo.InteractionResponseDeferredMessageUpdate,
				})

				interactionResponse, _ := s.InteractionResponse(i.Interaction)

				a.processing = true
				/////
				// sending:
				////// Type: discordgo.InteractionResponseDeferredChannelMessageWithSource,
				// as an interaction response can make the bot show "is thinking", but I can't seem to get it to work
				// with the messages with the action row in it.
				/////
				_ = a.client.Answer(answerIdMap[i.MessageComponentData().CustomID])

				if a.client.Progress() >= a.guessThreshold || a.client.Step()+1 > a.questionLimit {
					_ = s.ChannelMessageDelete(interactionResponse.ChannelID, interactionResponse.ID)

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

				questionResponse := a.generateQuestionResponseData(false)
				_, _ = s.ChannelMessageEdit(interactionResponse.ChannelID, interactionResponse.ID, questionResponse.Content)
				a.processing = false
			}

		default:
		}
	}

	return handlers
}

func (a *akinator) Commands() map[string]*discordgo.ApplicationCommand {
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

func (a *akinator) Intents() []discordgo.Intent {
	return nil
}

func (a *akinator) generateThemeResponseData() *discordgo.InteractionResponseData {
	responseData := discordgo.InteractionResponseData{
		Content: "Select a theme:",
		Components: []discordgo.MessageComponent{
			discordgo.ActionsRow{
				Components: []discordgo.MessageComponent{
					discordgo.Button{
						Label:    "Characters",
						Style:    discordgo.PrimaryButton,
						CustomID: "21q_theme_characters",
					},
					discordgo.Button{
						Label:    "Animals",
						Style:    discordgo.PrimaryButton,
						CustomID: "21q_theme_animals",
					},
					discordgo.Button{
						Label:    "Objects",
						Style:    discordgo.PrimaryButton,
						CustomID: "21q_theme_objects",
					},
				},
			},
		},
	}

	return &responseData
}

func (a *akinator) generateQuestionResponseData(buttonDisabled bool) *discordgo.MessageSend {
	var actionsRow discordgo.ActionsRow
	for _, answer := range a.client.Answers() {
		button := discordgo.Button{
			Label:    answer,
			Style:    discordgo.PrimaryButton,
			CustomID: "21q_answer_" + strings.ToLower(answer),
			Disabled: buttonDisabled,
		}
		actionsRow.Components = append(actionsRow.Components, button)
	}

	responseData := discordgo.MessageSend{
		Content: fmt.Sprintf("%d) %s [%.1f]", a.client.Step()+1, a.client.Question(), a.client.Progress()),
		Components: []discordgo.MessageComponent{
			actionsRow,
		},
	}

	return &responseData
}
