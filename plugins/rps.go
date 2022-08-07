package plugins

import (
	"fmt"
	"github.com/bwmarrin/discordgo"
	"github.com/olympus-go/eris/utils"
	"github.com/rs/zerolog"
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
	challengeChannelId  string
	startTime           time.Time
}

type RpsPlugin struct {
	activeGames   map[string]*rpsGame
	rockValue     string
	paperValue    string
	scissorsValue string
	winChan       chan string
	logger        zerolog.Logger
}

func RPS(logger zerolog.Logger) RpsPlugin {
	rand.Seed(time.Now().UnixNano())

	plugin := RpsPlugin{
		activeGames:   make(map[string]*rpsGame),
		rockValue:     "rock",
		paperValue:    "newspaper",
		scissorsValue: "scissors",
		winChan:       make(chan string),
		logger:        logger.With().Str("plugin", "rps").Logger(),
	}

	return plugin
}

func (r RpsPlugin) Name() string {
	return "Rock Paper Scissors"
}

func (r RpsPlugin) Description() string {
	return "Enables challenging your friends to rock paper scissors matches"
}

func (r RpsPlugin) Handlers() map[string]any {
	handlers := make(map[string]any)

	handlers["rps_handler"] = func(session *discordgo.Session, i *discordgo.InteractionCreate) {
		switch i.Type {
		case discordgo.InteractionApplicationCommand:
			applicationCommandData := i.ApplicationCommandData()
			if applicationCommandData.Name != "rps" {
				return
			}

			r.logger.Debug().Str("user_id", utils.GetInteractionUserId(i.Interaction)).
				Interface("command", applicationCommandData).Msg("user invoked slash command")

			go r.winCheckWorker(session)

			challenger := utils.GetInteractionUserId(i.Interaction)
			challengee, ok := applicationCommandData.Options[0].Value.(string)
			if !ok {
				r.logger.Error().Msgf("expected value to be string, instead got %T", applicationCommandData.Options[0].Value)
				utils.InteractionResponse(session, i.Interaction).Flags(discordgo.MessageFlagsEphemeral).
					Message("Something went wrong.").SendWithLog(r.logger)
				return
			}

			// Make sure the challenger didn't challenge themselves
			if challengee == challenger {
				utils.InteractionResponse(session, i.Interaction).Flags(discordgo.MessageFlagsEphemeral).
					Message("You can't challenge yourself.").SendWithLog(r.logger)
				return
			}

			gameId := r.generateGameId(challenger, challengee)
			if _, ok = r.activeGames[gameId]; ok {
				utils.InteractionResponse(session, i.Interaction).Flags(discordgo.MessageFlagsEphemeral).
					Message("Finish your current match first!").SendWithLog(r.logger)
				return
			}

			r.activeGames[gameId] = &rpsGame{
				challenger:          challenger,
				challengee:          challengee,
				challengerSelection: "",
				challengeeSelection: "",
				challengeChannelId:  "",
				startTime:           time.Now(),
			}

			// Set the channelId if the interaction was created in a guild
			if i.Interaction.GuildID != "" {
				r.activeGames[gameId].challengeChannelId = i.Interaction.ChannelID
			}

			r.logger.Debug().Str("user_id", challenger).Str("target_user_id", challengee).
				Str("channel_id", r.activeGames[gameId].challengeChannelId).Msg("user invoked rock paper scissors")

			if challengee == session.State.User.ID {
				r.logger.Debug().Str("user_id", challenger).Msg("bot accepted the users challenge")

				utils.InteractionResponse(session, i.Interaction).Flags(discordgo.MessageFlagsEphemeral).
					Message("I'll DM you.").SendWithLog(r.logger)

				challengerUserChannel, err := session.UserChannelCreate(r.activeGames[gameId].challenger)
				if err != nil {
					r.logger.Error().Err(err).Str("user_id", r.activeGames[gameId].challenger).
						Msg("could not create DM with user")
					return
				}

				r.activeGames[gameId].challengerPrompt, err = session.ChannelMessageSendComplex(challengerUserChannel.ID, r.generatePrompt(gameId, true))
				if err != nil {
					r.logger.Error().Err(err).Str("user_id", r.activeGames[gameId].challenger).Msg("could not send DM to user")
					return
				}
				r.logger.Debug().Str("game_id", gameId).Str("user_id", r.activeGames[gameId].challenger).Msg("prompt sent to user")
				move := r.generateMove()
				r.activeGames[gameId].challengeeSelection = move
				r.logger.Debug().Str("game_id", gameId).Str("selection", move).Msg("bot made a selection")
			} else {
				challengeeUserChannel, err := session.UserChannelCreate(challengee)
				if err != nil {
					r.logger.Error().Err(err).Str("user_id", challengee).Msg("could not create DM with user")
					utils.InteractionResponse(session, i.Interaction).Flags(discordgo.MessageFlagsEphemeral).
						Message("Something went wrong.").SendWithLog(r.logger)
					return
				}

				message := ""
				if len(applicationCommandData.Options) > 1 {
					message, _ = applicationCommandData.Options[1].Value.(string)
				}

				r.activeGames[gameId].challengeeChallenge, err = session.ChannelMessageSendComplex(challengeeUserChannel.ID, r.generateChallenge(gameId, challenger, message, true))
				utils.InteractionResponse(session, i.Interaction).Flags(discordgo.MessageFlagsEphemeral).
					Message("Challenge issued.").SendWithLog(r.logger)
				r.logger.Debug().Str("game_id", gameId).Str("user_id", challengee).Msg("challenge sent to user")

				go func() {
					time.Sleep(30 * time.Second)
					if _, ok := r.activeGames[gameId]; ok {
						_ = session.ChannelMessageDelete(r.activeGames[gameId].challengeeChallenge.ChannelID, r.activeGames[gameId].challengeeChallenge.ID)
						_, _ = session.FollowupMessageCreate(i.Interaction, true, &discordgo.WebhookParams{
							Content: "Challenge timed out.",
							Flags:   uint64(discordgo.MessageFlagsEphemeral),
						})
						r.logger.Debug().Str("game_id", gameId).Msg("challenge timed out")
						delete(r.activeGames, gameId)
					}
				}()
			}
		case discordgo.InteractionMessageComponent:
			messageComponentData := i.MessageComponentData()

			if strings.HasPrefix(messageComponentData.CustomID, "rps_challenge") {
				idSlice := strings.Split(messageComponentData.CustomID, "_")
				if len(idSlice) != 4 {
					r.logger.Error().Str("id", messageComponentData.CustomID).Msg("button id failed to split as expected")
					utils.InteractionResponse(session, i.Interaction).Flags(discordgo.MessageFlagsEphemeral).
						Message("Something went wrong.").SendWithLog(r.logger)
					return
				}

				// Gather necessary info from interaction
				gameId := idSlice[3]
				responseSelection := idSlice[2]

				// Check if the game still exists
				if _, ok := r.activeGames[gameId]; !ok {
					utils.InteractionResponse(session, i.Interaction).Flags(discordgo.MessageFlagsEphemeral).
						Message("Game no longer exists.").SendWithLog(r.logger)
					return
				}

				switch responseSelection {
				case "accept":
					r.logger.Debug().Str("game_id", gameId).Str("user_id", r.activeGames[gameId].challengee).
						Msg("challenge accepted by user")

					// Generate the prompt for the challengee
					challengeeUserChannel, err := session.UserChannelCreate(r.activeGames[gameId].challengee)
					if err != nil {
						r.logger.Error().Err(err).Str("userId", r.activeGames[gameId].challengee).Msg("could not create DM with user")
						return
					}
					if r.activeGames[gameId].challengeePrompt, err = session.ChannelMessageSendComplex(challengeeUserChannel.ID, r.generatePrompt(gameId, true)); err != nil {
						r.logger.Error().Err(err).Str("channelId", challengeeUserChannel.ID).Msg("failed to send message")
						return
					}

					r.logger.Debug().Str("game_id", gameId).Str("user_id", r.activeGames[gameId].challengee).
						Msg("prompt sent to user")

					// Send the prompt to the challenger
					challengerUserChannel, err := session.UserChannelCreate(r.activeGames[gameId].challenger)
					if err != nil {
						r.logger.Error().Err(err).Str("userId", r.activeGames[gameId].challenger).Msg("could not create DM with user")
						return
					}

					r.logger.Debug().Str("game_id", gameId).Str("user_id", r.activeGames[gameId].challenger).
						Msg("prompt sent to user")

					_ = session.ChannelMessageDelete(r.activeGames[gameId].challengeeChallenge.ChannelID, r.activeGames[gameId].challengeeChallenge.ID)
					r.activeGames[gameId].challengerPrompt, err = session.ChannelMessageSendComplex(challengerUserChannel.ID, r.generatePrompt(gameId, true))
				case "decline":
					r.logger.Debug().Str("game_id", gameId).Str("user_id", r.activeGames[gameId].challengee).
						Msg("challenge declined by user")

					challengerUserChannel, err := session.UserChannelCreate(r.activeGames[gameId].challenger)
					if err != nil {
						r.logger.Error().Err(err).Str("userId", r.activeGames[gameId].challenger).Msg("could not create DM with user")
						return
					}

					_ = session.ChannelMessageDelete(r.activeGames[gameId].challengeeChallenge.ChannelID, r.activeGames[gameId].challengeeChallenge.ID)
					_, err = session.ChannelMessageSend(challengerUserChannel.ID, r.generateDecline(r.activeGames[gameId].challengee))

					r.logger.Debug().Str("game_id", gameId).Str("user_id", r.activeGames[gameId].challenger).
						Msg("decline notice sent to user")

					delete(r.activeGames, gameId)
				default:
					r.logger.Error().Str("gameId", gameId).Str("value", responseSelection).Msg("challenge response receive unexpected value")
					utils.InteractionResponse(session, i.Interaction).Flags(discordgo.MessageFlagsEphemeral).
						Message("Something went wrong.").SendWithLog(r.logger)
					_ = session.ChannelMessageDelete(r.activeGames[gameId].challengeeChallenge.ChannelID, r.activeGames[gameId].challengeeChallenge.ID)
					delete(r.activeGames, gameId)
					return
				}
			} else if strings.HasPrefix(messageComponentData.CustomID, "rps_move") {
				idSlice := strings.Split(messageComponentData.CustomID, "_")
				if len(idSlice) != 4 {
					r.logger.Error().Str("id", messageComponentData.CustomID).Msg("button id failed to split as expected")
					utils.InteractionResponse(session, i.Interaction).Flags(discordgo.MessageFlagsEphemeral).
						Message("Something went wrong.").SendWithLog(r.logger)
					return
				}

				// Gather necessary info from interaction
				gameId := idSlice[3]
				moveSelection := idSlice[2]
				userId := utils.GetInteractionUserId(i.Interaction)

				// Check if the game still exists
				if _, ok := r.activeGames[gameId]; !ok {
					utils.InteractionResponse(session, i.Interaction).Flags(discordgo.MessageFlagsEphemeral).
						Message("Game no longer exists.").SendWithLog(r.logger)
					return
				}

				// Store interaction input in active game
				if userId == r.activeGames[gameId].challenger {
					r.activeGames[gameId].challengerSelection = moveSelection

					// Update the prompt and remove the buttons
					messageEdit := discordgo.NewMessageEdit(r.activeGames[gameId].challengerPrompt.ChannelID, r.activeGames[gameId].challengerPrompt.ID)
					content := fmt.Sprintf("You selected :%s:.", moveSelection)
					messageEdit.Content = &content
					messageEdit.Components = []discordgo.MessageComponent{}
					_, _ = session.ChannelMessageEditComplex(messageEdit)

					r.logger.Debug().Str("game_id", gameId).Str("user_id", r.activeGames[gameId].challenger).
						Str("selection", moveSelection).Msg("user made a selection")

				} else if userId == r.activeGames[gameId].challengee {
					r.activeGames[gameId].challengeeSelection = moveSelection

					// Update the prompt and remove the buttons
					messageEdit := discordgo.NewMessageEdit(r.activeGames[gameId].challengeePrompt.ChannelID, r.activeGames[gameId].challengeePrompt.ID)
					content := fmt.Sprintf("You selected :%s:.", moveSelection)
					messageEdit.Content = &content
					messageEdit.Components = []discordgo.MessageComponent{}
					_, _ = session.ChannelMessageEditComplex(messageEdit)

					r.logger.Debug().Str("game_id", gameId).Str("user_id", r.activeGames[gameId].challengee).
						Str("selection", moveSelection).Msg("user made a selection")
				} else {
					log.Error().Str("gameId", gameId).Str("userId", userId).Msg("user interacted with button not associated with their game")
					utils.InteractionResponse(session, i.Interaction).Flags(discordgo.MessageFlagsEphemeral).
						Message("Something went wrong. This isn't your game.").SendWithLog(r.logger)
					return
				}

				// Send a successful response to the interaction
				utils.InteractionResponse(session, i.Interaction).Type(discordgo.InteractionResponseDeferredMessageUpdate).
					SendWithLog(r.logger)

				r.winChan <- gameId
			}
		}
	}

	return handlers
}

func (r RpsPlugin) Commands() map[string]*discordgo.ApplicationCommand {
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

func (r RpsPlugin) Intents() []discordgo.Intent {
	return nil
}

func (r RpsPlugin) challengeUser() {

}

func (r RpsPlugin) generatePrompt(gameId string, enabled bool) *discordgo.MessageSend {
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
		"Whatever you do don't choose rock:",
		"Whatever you do don't choose paper:",
		"Whatever you do don't choose scissors:",
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

func (r RpsPlugin) generateChallenge(gameId string, challenger string, message string, enabled bool) *discordgo.MessageSend {
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

func (r RpsPlugin) generateDecline(userId string) string {
	options := []string{
		fmt.Sprintf("<@%s> declined.", userId),
		fmt.Sprintf("<@%s> said fuck off.", userId),
		fmt.Sprintf("<@%s> passed.", userId),
		fmt.Sprintf("<@%s> ain't about it.", userId),
		fmt.Sprintf("<@%s> ain't got time fo dat.", userId),
		fmt.Sprintf("Miss <@%s> with that shit.", userId),
		fmt.Sprintf("<@%s> says to suck an egg.", userId),
		fmt.Sprintf("<@%s> thinks you smell.", userId),
	}

	return options[rand.Int()%len(options)]
}

func (r RpsPlugin) generateMove() string {
	return []string{r.rockValue, r.paperValue, r.scissorsValue}[rand.Int()%3]
}

func (r RpsPlugin) generateGameId(challenger, challengee string) string {
	return fmt.Sprintf("%svs%s", challenger, challengee)
}

func (r RpsPlugin) moveCmp(m1, m2 string) int {
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

func (r RpsPlugin) winCheckWorker(session *discordgo.Session) {
	for gameId := range r.winChan {
		if _, ok := r.activeGames[gameId]; !ok {
			log.Debug().Str("game_id", gameId).Msg("game already processed")
			continue
		}

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

			// If the game was launched in a channel, report the results back in the channel. Otherwise DM both
			// users.
			if r.activeGames[gameId].challengeChannelId != "" {
				_, _ = session.ChannelMessageSend(r.activeGames[gameId].challengeChannelId, winnerMessage)
			} else {
				// Send results to challenger
				challengerUserChannel, _ := session.UserChannelCreate(r.activeGames[gameId].challenger)
				_, _ = session.ChannelMessageSend(challengerUserChannel.ID, winnerMessage)

				// Send results to challengee (if not the bot itself)
				if r.activeGames[gameId].challengee != session.State.User.ID {
					challengeeUserChannel, _ := session.UserChannelCreate(r.activeGames[gameId].challengee)
					_, _ = session.ChannelMessageSend(challengeeUserChannel.ID, winnerMessage)
				}
			}

			// Cleanup the old messages
			_ = session.ChannelMessageDelete(r.activeGames[gameId].challengerPrompt.ChannelID, r.activeGames[gameId].challengerPrompt.ID)
			if r.activeGames[gameId].challengee != session.State.User.ID {
				_ = session.ChannelMessageDelete(r.activeGames[gameId].challengeePrompt.ChannelID, r.activeGames[gameId].challengeePrompt.ID)
			}

			// Close out the game
			delete(r.activeGames, gameId)
		}
	}
}
