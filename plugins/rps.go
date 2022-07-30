package plugins

import (
	"fmt"
	"github.com/bwmarrin/discordgo"
	"github.com/eolso/eris"
	"github.com/rs/zerolog/log"
	"math/rand"
	"strings"
	"time"
)

type rpsGame struct {
	challenger          string
	challengee          string
	challengeeChallenge *discordgo.Message
	challengerPrompt    *discordgo.Message
	challengeePrompt    *discordgo.Message
	challengerSelection string
	challengeeSelection string
	startTime           time.Time
}

type rps struct {
	botId         string
	activeGames   map[string]*rpsGame
	rockValue     string
	paperValue    string
	scissorsValue string
}

func RPS(botId string) eris.Plugin {
	rand.Seed(time.Now().UnixNano())

	return rps{
		botId:         botId,
		activeGames:   make(map[string]*rpsGame),
		rockValue:     "rock",
		paperValue:    "newspaper",
		scissorsValue: "scissors",
	}
}

func (r rps) Name() string {
	return "RockPaperScissors"
}

func (r rps) Description() string {
	return "Enables challenging your friends to rock paper scissors matches"
}

func (r rps) Handlers() map[string]any {
	handlers := make(map[string]any)

	handlers["rps_handler"] = func(session *discordgo.Session, i *discordgo.InteractionCreate) {
		switch i.Type {
		case discordgo.InteractionApplicationCommand:
			applicationCommandData := i.ApplicationCommandData()
			if applicationCommandData.Name != "rps" {
				return
			}

			challenger := ""
			if i.Member != nil {
				challenger = i.Member.User.ID
			} else if i.User != nil {
				challenger = i.User.ID
			}

			challengee, ok := applicationCommandData.Options[0].Value.(string)
			if !ok {
				_ = session.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
					Type: discordgo.InteractionResponseChannelMessageWithSource,
					Data: &discordgo.InteractionResponseData{
						Content: "Something went wrong.",
						Flags:   uint64(discordgo.MessageFlagsEphemeral),
					},
				})
				log.Error().Msgf("expected value to be string, instead got %T", applicationCommandData.Options[0].Value)
				return
			}

			// Make sure the challenger didn't challenge themself
			if challengee == challenger {
				_ = session.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
					Type: discordgo.InteractionResponseChannelMessageWithSource,
					Data: &discordgo.InteractionResponseData{
						Content: "You can't challenge yourself.",
						Flags:   uint64(discordgo.MessageFlagsEphemeral),
					},
				})
				return
			}

			gameId := r.generateGameId(challenger, challengee)

			if _, ok = r.activeGames[gameId]; ok {
				_ = session.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
					Type: discordgo.InteractionResponseChannelMessageWithSource,
					Data: &discordgo.InteractionResponseData{
						Content: "Finish your current match first!",
						Flags:   uint64(discordgo.MessageFlagsEphemeral),
					},
				})
				return
			}

			r.activeGames[gameId] = &rpsGame{
				challenger:          challenger,
				challengee:          challengee,
				challengerSelection: "",
				challengeeSelection: "",
				startTime:           time.Now(),
			}

			//_, _ = session.ChannelMessageSend(i.ChannelID, fmt.Sprintf("<@%s> challenged <@%s> to rock paper scissors!", challenger, challengedUser))

			if challengee == r.botId {
				r.activeGames[gameId].challengeeSelection = r.generateMove()
				_ = session.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
					Type: discordgo.InteractionResponseChannelMessageWithSource,
					Data: &discordgo.InteractionResponseData{
						Content: "I'll DM you.",
						Flags:   uint64(discordgo.MessageFlagsEphemeral),
					},
				})

				challengerUserChannel, err := session.UserChannelCreate(r.activeGames[gameId].challenger)
				if err != nil {
					log.Error().Err(err).Str("userId", r.activeGames[gameId].challenger).Msg("could not create DM with user")
					return
				}

				r.activeGames[gameId].challengerPrompt, err = session.ChannelMessageSendComplex(challengerUserChannel.ID, r.generatePrompt(gameId, true))
			} else {
				challengeeUserChannel, err := session.UserChannelCreate(challengee)
				if err != nil {
					_ = session.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
						Type: discordgo.InteractionResponseChannelMessageWithSource,
						Data: &discordgo.InteractionResponseData{
							Content: "Something went wrong.",
							Flags:   uint64(discordgo.MessageFlagsEphemeral),
						},
					})
					log.Error().Err(err).Str("userId", challengee).Msg("could not create DM with user")
					return
				}

				message := ""
				if len(applicationCommandData.Options) > 1 {
					message, _ = applicationCommandData.Options[1].Value.(string)
				}

				r.activeGames[gameId].challengeeChallenge, err = session.ChannelMessageSendComplex(challengeeUserChannel.ID, r.generateChallenge(gameId, challenger, message, true))

				err = session.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
					Type: discordgo.InteractionResponseChannelMessageWithSource,
					Data: &discordgo.InteractionResponseData{
						Content: "Challenge issued.",
						Flags:   uint64(discordgo.MessageFlagsEphemeral),
					},
				})
				if err != nil {
					log.Error().Err(err).Msg("failed to respond to interaction")
				}
			}
		case discordgo.InteractionMessageComponent:
			messageComponentData := i.MessageComponentData()

			// TODO maybe this is bad to have here
			_ = session.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
				Type: discordgo.InteractionResponseDeferredMessageUpdate,
			})

			if strings.HasPrefix(messageComponentData.CustomID, "rps_challenge") {
				idSlice := strings.Split(messageComponentData.CustomID, "_")
				if len(idSlice) != 4 {
					log.Error().Str("id", messageComponentData.CustomID).Msg("button id failed to split as expected")
					// TODO respond to interaction probs
					return
				}

				// Gather necessary info from interaction
				gameId := idSlice[3]
				responseSelection := idSlice[2]

				// Check if the game still exists. No ephemeral flags needed since it's a DM.
				if _, ok := r.activeGames[gameId]; !ok {
					_ = session.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
						Type: discordgo.InteractionResponseChannelMessageWithSource,
						Data: &discordgo.InteractionResponseData{
							Content: "Game no longer exists",
						},
					})
					return
				}

				switch responseSelection {
				case "accept":
					// Generate the prompt for the challengee
					challengeeUserChannel, err := session.UserChannelCreate(r.activeGames[gameId].challengee)
					if err != nil {
						log.Error().Err(err).Str("userId", r.activeGames[gameId].challengee).Msg("could not create DM with user")
						return
					}
					if r.activeGames[gameId].challengeePrompt, err = session.ChannelMessageSendComplex(challengeeUserChannel.ID, r.generatePrompt(gameId, true)); err != nil {
						log.Error().Err(err).Str("channelId", challengeeUserChannel.ID).Msg("failed to send message")
					}

					// Send the prompt to the challenger
					challengerUserChannel, err := session.UserChannelCreate(r.activeGames[gameId].challenger)
					if err != nil {
						log.Error().Err(err).Str("userId", r.activeGames[gameId].challenger).Msg("could not create DM with user")
						return
					}

					_ = session.ChannelMessageDelete(r.activeGames[gameId].challengeeChallenge.ChannelID, r.activeGames[gameId].challengeeChallenge.ID)
					r.activeGames[gameId].challengerPrompt, err = session.ChannelMessageSendComplex(challengerUserChannel.ID, r.generatePrompt(gameId, true))

				case "decline":
					challengerUserChannel, err := session.UserChannelCreate(r.activeGames[gameId].challenger)
					if err != nil {
						log.Error().Err(err).Str("userId", r.activeGames[gameId].challenger).Msg("could not create DM with user")
						return
					}

					_ = session.ChannelMessageDelete(r.activeGames[gameId].challengeeChallenge.ChannelID, r.activeGames[gameId].challengeeChallenge.ID)
					_, err = session.ChannelMessageSend(challengerUserChannel.ID, r.generateDecline(r.activeGames[gameId].challengee))

					delete(r.activeGames, gameId)
				default:
					_ = session.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
						Type: discordgo.InteractionResponseChannelMessageWithSource,
						Data: &discordgo.InteractionResponseData{
							Content: "Something went wrong.",
							Flags:   uint64(discordgo.MessageFlagsEphemeral),
						},
					})
					log.Error().Str("gameId", gameId).Str("value", responseSelection).Msg("challenge response receive unexpected value")
					_ = session.ChannelMessageDelete(r.activeGames[gameId].challengeeChallenge.ChannelID, r.activeGames[gameId].challengeeChallenge.ID)
					delete(r.activeGames, gameId)
					return
				}
			} else if strings.HasPrefix(messageComponentData.CustomID, "rps_move") {
				idSlice := strings.Split(messageComponentData.CustomID, "_")
				if len(idSlice) != 4 {
					log.Error().Str("id", messageComponentData.CustomID).Msg("button id failed to split as expected")
					// TODO respond to interaction probs
					return
				}

				// Gather necessary info from interaction
				gameId := idSlice[3]
				moveSelection := idSlice[2]
				userId := ""
				if i.Member != nil {
					userId = i.Member.User.ID
				} else if i.User != nil {
					userId = i.User.ID
				}

				// Check if the game still exists
				if _, ok := r.activeGames[gameId]; !ok {
					_ = session.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
						Type: discordgo.InteractionResponseChannelMessageWithSource,
						Data: &discordgo.InteractionResponseData{
							Content: "Game no longer exists",
							Flags:   uint64(discordgo.MessageFlagsEphemeral),
						},
					})
					return
				}

				// Store interaction input in active game
				if userId == r.activeGames[gameId].challenger {
					r.activeGames[gameId].challengerSelection = moveSelection
				} else if userId == r.activeGames[gameId].challengee {
					r.activeGames[gameId].challengeeSelection = moveSelection
				} else {
					_ = session.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
						Type: discordgo.InteractionResponseChannelMessageWithSource,
						Data: &discordgo.InteractionResponseData{
							Content: "Something went wrong. This isn't your game.",
							Flags:   uint64(discordgo.MessageFlagsEphemeral),
						},
					})
					log.Error().Str("gameId", gameId).Str("userId", userId).Msg("user interacted with button not associated with their game")
					return
				}

				// Send a successful response to the interaction
				_ = session.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
					Type: discordgo.InteractionResponseDeferredMessageUpdate,
				})

				// Check if match is over
				if r.activeGames[gameId].challengerSelection != "" && r.activeGames[gameId].challengeeSelection != "" {
					winnerMessage := fmt.Sprintf("<@%s> :%s:  :vs:  :%s: <@%s>\n",
						r.activeGames[gameId].challenger, r.activeGames[gameId].challengerSelection,
						r.activeGames[gameId].challengeeSelection, r.activeGames[gameId].challengee)

					switch r.moveCmp(r.activeGames[gameId].challengerSelection, r.activeGames[gameId].challengeeSelection) {
					case -1:
						winnerMessage += fmt.Sprintf("<@%s> wins!", r.activeGames[gameId].challenger)
					case 0:
						winnerMessage += fmt.Sprintf("It's a tie!")
					case 1:
						winnerMessage += fmt.Sprintf("<@%s> wins!", r.activeGames[gameId].challengee)
					}

					// Send results to challenger and delete the old messages
					challengerUserChannel, _ := session.UserChannelCreate(r.activeGames[gameId].challenger)
					_, _ = session.ChannelMessageSend(challengerUserChannel.ID, winnerMessage)
					_ = session.ChannelMessageDelete(r.activeGames[gameId].challengerPrompt.ChannelID, r.activeGames[gameId].challengerPrompt.ID)

					// Send results to challengee (if not the bot itself)
					if r.activeGames[gameId].challengee != r.botId {
						challengeeUserChannel, _ := session.UserChannelCreate(r.activeGames[gameId].challengee)
						_, _ = session.ChannelMessageSend(challengeeUserChannel.ID, winnerMessage)
						_ = session.ChannelMessageDelete(r.activeGames[gameId].challengeePrompt.ChannelID, r.activeGames[gameId].challengeePrompt.ID)
					}

					delete(r.activeGames, gameId)
				}
			}
		}
	}

	return handlers
}

func (r rps) Commands() map[string]*discordgo.ApplicationCommand {
	commands := make(map[string]*discordgo.ApplicationCommand)

	commands["rps_cmd"] = &discordgo.ApplicationCommand{
		Name:        "rps",
		Description: "Challenge a user to rock paper scissors",
		Options: []*discordgo.ApplicationCommandOption{
			{
				Name:        "user",
				Description: "Challenges the specified user",
				Type:        discordgo.ApplicationCommandOptionUser,
				Required:    true,
			},
			{
				Name:        "message",
				Description: "Optional message to send with the challenge",
				Type:        discordgo.ApplicationCommandOptionString,
				Required:    false,
			},
		},
	}

	return commands
}

func (r rps) Intents() []discordgo.Intent {
	return nil
}

func (r rps) challengeUser() {

}

func (r rps) generatePrompt(gameId string, enabled bool) *discordgo.MessageSend {
	options := []string{
		"Choose wisely:",
		"Make your move:",
		"Ooooo get 'em:",
		"No pressure:",
		"Believe in the heart of your fingers:",
		"Look, if you had one shot or one opportunity\nTo seize everything you ever wanted in one moment\nWould you capture it, or just let it slip? Yo:",
		"Don't mess this up:",
		"おまえはもうしんでいる:",
		"I believe in you:",
	}

	response := discordgo.MessageSend{
		Content: options[rand.Int()%len(options)],
		Components: []discordgo.MessageComponent{
			discordgo.ActionsRow{
				Components: []discordgo.MessageComponent{
					discordgo.Button{
						Label:    "Rock",
						Style:    discordgo.PrimaryButton,
						CustomID: fmt.Sprintf("rps_move_%s_%s", r.rockValue, gameId),
						Disabled: !enabled,
					},
					discordgo.Button{
						Label:    "Paper",
						Style:    discordgo.PrimaryButton,
						CustomID: fmt.Sprintf("rps_move_%s_%s", r.paperValue, gameId),
						Disabled: !enabled,
					},
					discordgo.Button{
						Label:    "Scissors",
						Style:    discordgo.PrimaryButton,
						CustomID: fmt.Sprintf("rps_move_%s_%s", r.scissorsValue, gameId),
						Disabled: !enabled,
					},
				},
			},
		},
	}

	return &response
}

func (r rps) generateChallenge(gameId string, challenger string, message string, enabled bool) *discordgo.MessageSend {
	challengeString := fmt.Sprintf("<@%s> challenged you to rock paper scissors.", challenger)
	if message != "" {
		challengeString += fmt.Sprintf("\n\n They also wanted to say: %s", message)
	}

	responseData := discordgo.MessageSend{
		Content: challengeString,
		Components: []discordgo.MessageComponent{
			discordgo.ActionsRow{
				Components: []discordgo.MessageComponent{
					discordgo.Button{
						Label:    "Accept",
						Style:    discordgo.PrimaryButton,
						CustomID: "rps_challenge_accept_" + gameId,
						Disabled: !enabled,
					},
					discordgo.Button{
						Label:    "Decline",
						Style:    discordgo.PrimaryButton,
						CustomID: "rps_challenge_decline_" + gameId,
						Disabled: !enabled,
					},
				},
			},
		},
	}

	return &responseData
}

func (r rps) generateDecline(userId string) string {
	options := []string{
		fmt.Sprintf("<@%s> declined.", userId),
		fmt.Sprintf("<@%s> said fuck off.", userId),
		fmt.Sprintf("<@%s> passed.", userId),
		fmt.Sprintf("<@%s> ain't about it.", userId),
		fmt.Sprintf("<@%s> ain't got time fo dat.", userId),
		fmt.Sprintf("Miss <@%s> with that shit.", userId),
		fmt.Sprintf("<@%s> says to suck an egg.", userId),
	}

	return options[rand.Int()%len(options)]
}

func (r rps) generateMove() string {
	return []string{r.rockValue, r.paperValue, r.scissorsValue}[rand.Int()%3]
}

func (r rps) generateGameId(challenger, challengee string) string {
	return fmt.Sprintf("%svs%s", challenger, challengee)
}

func (r rps) moveCmp(m1, m2 string) int {
	switch m1 {
	case "":
		if m2 == "" {
			return 0
		} else {
			return 1
		}
	case r.rockValue:
		if m2 == r.rockValue {
			return 0
		} else if m2 == r.paperValue {
			return 1
		} else {
			return -1
		}
	case r.paperValue:
		if m2 == r.paperValue {
			return 0
		} else if m2 == r.scissorsValue {
			return 1
		} else {
			return -1
		}
	case r.scissorsValue:
		if m2 == r.scissorsValue {
			return 0
		} else if m2 == r.rockValue {
			return 1
		} else {
			return -1
		}
	}

	return 0
}
