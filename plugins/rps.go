package plugins

import (
	"fmt"
	"github.com/bwmarrin/discordgo"
	"github.com/olympus-go/eris/utils"
	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
	"math/rand"
	"strings"
	"sync"
	"time"
)

var (
	rockValue     = "rock"
	paperValue    = "newspaper"
	scissorsValue = "scissors"
)

type rpsGame struct {
	Challenger         rpsUser
	Challenged         rpsUser
	Id                 string
	ChallengeChannelId string
}

type rpsUser struct {
	Id               string
	challengeMessage *discordgo.Message
	promptMessage    *discordgo.Message
	Selection        string
}

type RpsPlugin struct {
	activeGames map[string]*rpsGame
	gameLock    sync.RWMutex
	winLock     sync.Mutex
	logger      zerolog.Logger
}

func Rps(logger zerolog.Logger) *RpsPlugin {
	rand.Seed(time.Now().UnixNano())

	return &RpsPlugin{
		activeGames: make(map[string]*rpsGame),
		logger:      logger.With().Str("plugin", "rps").Logger(),
	}
}

func (r *RpsPlugin) Name() string {
	return "Rock Paper Scissors"
}

func (r *RpsPlugin) Description() string {
	return "Enables challenging your friends to rock paper scissors matches"
}

func (r *RpsPlugin) Handlers() map[string]any {
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

			challenger := utils.GetInteractionUserId(i.Interaction)
			challenged, ok := applicationCommandData.Options[0].Value.(string)
			if !ok {
				r.logger.Error().Msgf("expected value to be string, instead got %T", applicationCommandData.Options[0].Value)
				utils.InteractionResponse(session, i.Interaction).Flags(discordgo.MessageFlagsEphemeral).
					Message("Something went wrong.").SendWithLog(r.logger)
				return
			}

			// Make sure the Challenger didn't challenge themselves
			if challenged == challenger {
				utils.InteractionResponse(session, i.Interaction).Flags(discordgo.MessageFlagsEphemeral).
					Message("You can't challenge yourself.").SendWithLog(r.logger)
				return
			}

			game := newRpsGame(challenger, challenged)

			if _, ok = r.getGame(game.Id); ok {
				utils.InteractionResponse(session, i.Interaction).Flags(discordgo.MessageFlagsEphemeral).
					Message("Finish your current match first!").SendWithLog(r.logger)
				return
			}

			// Set the channelId if the interaction was created in a guild
			if i.Interaction.GuildID != "" {
				game.ChallengeChannelId = i.Interaction.ChannelID
			}

			r.setGame(game)
			r.logger.Debug().Interface("game", game).Msg("game created")

			// If the Challenged user is the bot running this
			if challenged == session.State.User.ID {
				r.logger.Debug().Interface("game", game).Msg("bot accepted the challenge")

				utils.InteractionResponse(session, i.Interaction).Flags(discordgo.MessageFlagsEphemeral).
					Message("I'll DM you.").SendWithLog(r.logger)

				var err error
				game.Challenger.promptMessage, err = game.sendMessage(session, game.Challenger, game.generatePrompt(game.Id, true))
				if err != nil {
					r.logger.Error().Err(err).Interface("game", game).Str("user_id", game.Challenger.Id).
						Msg("failed to send prompt to user")
					// TODO add a follow up here informing the user things went wrong
					r.deleteGame(game.Id)
					return
				}
				r.logger.Debug().Interface("game", game).Str("user_id", game.Challenger.Id).Msg("prompt sent to user")

				move := r.generateMove()
				game.Challenged.Selection = move
				r.setGame(game)
				r.logger.Debug().Interface("game", game).Str("Selection", move).Msg("bot made a Selection")
			} else {
				message := ""
				if len(applicationCommandData.Options) > 1 {
					message, _ = applicationCommandData.Options[1].Value.(string)
				}

				var err error
				challengeMessage := game.generateChallenge(game.Id, game.Challenger.Id, message, true)
				game.Challenged.challengeMessage, err = game.sendMessage(session, game.Challenged, challengeMessage)
				if err != nil {
					r.logger.Error().Err(err).Interface("game", game).Str("user_id", game.Challenged.Id).
						Msg("failed to send challenge to user")
					// TODO add a follow up here informing the user things went wrong
					r.deleteGame(game.Id)
					return
				}

				utils.InteractionResponse(session, i.Interaction).Flags(discordgo.MessageFlagsEphemeral).
					Message("Challenge issued.").SendWithLog(r.logger)
				r.logger.Debug().Interface("game", game).Str("user_id", game.Challenged.Id).
					Msg("challenge sent to user")

				// TODO add timeouts back mebe
				//go func() {
				//	time.Sleep(30 * time.Second)
				//	if _, ok := r.activeGames[gameId]; ok {
				//		_ = session.ChannelMessageDelete(r.activeGames[gameId].challengeeChallenge.ChannelID, r.activeGames[gameId].challengeeChallenge.ID)
				//		_, _ = session.FollowupMessageCreate(i.Interaction, true, &discordgo.WebhookParams{
				//			Content: "Challenge timed out.",
				//			Flags:   uint64(discordgo.MessageFlagsEphemeral),
				//		})
				//		r.logger.Debug().Str("game_id", gameId).Msg("challenge timed out")
				//		delete(r.activeGames, gameId)
				//	}
				//}()
			}
		case discordgo.InteractionMessageComponent:
			messageComponentData := i.MessageComponentData()

			if strings.HasPrefix(messageComponentData.CustomID, "rps_challenge") {
				idSlice := strings.Split(messageComponentData.CustomID, "_")
				if len(idSlice) != 4 {
					r.logger.Error().Str("Id", messageComponentData.CustomID).Msg("button Id failed to split as expected")
					utils.InteractionResponse(session, i.Interaction).Flags(discordgo.MessageFlagsEphemeral).
						Message("Something went wrong.").SendWithLog(r.logger)
					return
				}

				// Gather necessary info from interaction
				gameId := idSlice[3]
				responseSelection := idSlice[2]

				// Check if the game still exists
				game, ok := r.getGame(gameId)
				if !ok {
					utils.InteractionResponse(session, i.Interaction).Flags(discordgo.MessageFlagsEphemeral).
						Message("Game no longer exists.").SendWithLog(r.logger)
					return
				}

				switch responseSelection {
				case "accept":
					r.logger.Debug().Interface("game", game).Str("user_id", game.Challenged.Id).
						Msg("challenge accepted by user")

					// Send the prompt for the Challenged user
					var err error
					game.Challenged.promptMessage, err = game.sendMessage(session, game.Challenged, game.generatePrompt(game.Id, true))
					if err != nil {
						r.logger.Error().Err(err).Interface("game", game).Str("user_id", game.Challenged.Id).
							Msg("failed to send prompt to user")
						// TODO add a follow up here informing the user things went wrong. Might also need to inform Challenger
						r.deleteGame(game.Id)
						return
					}

					r.logger.Debug().Interface("game", game).Str("user_id", game.Challenged.Id).
						Msg("prompt sent to user")

					// Send the prompt for the Challenger user
					game.Challenger.promptMessage, err = game.sendMessage(session, game.Challenger, game.generatePrompt(game.Id, true))
					if err != nil {
						r.logger.Error().Err(err).Interface("game", game).Str("user_id", game.Challenger.Id).
							Msg("failed to send prompt to user")
						// TODO add a follow up here informing the user things went wrong. Might also need to inform Challenger
						r.deleteGame(game.Id)
						return
					}

					r.logger.Debug().Interface("game", game).Str("user_id", game.Challenger.Id).
						Msg("prompt sent to user")

					if session == nil {
						r.logger.Error().Msg("session is nil")
					}
					_ = session.ChannelMessageDelete(game.Challenged.challengeMessage.ChannelID, game.Challenged.challengeMessage.ID)
				case "decline":
					r.logger.Debug().Interface("game", game).Str("user_id", game.Challenger.Id).
						Msg("challenge declined by user")

					if _, err := game.sendMessage(session, game.Challenger, game.generateDecline(game.Challenged.Id)); err != nil {
						r.logger.Error().Err(err).Interface("game", game).Str("user_id", game.Challenger.Id).
							Msg("failed to send decline to user")
						// TODO add a follow up here informing the user things went wrong. Might also need to inform Challenger
						r.deleteGame(game.Id)
						return
					}

					r.logger.Debug().Interface("game", game).Str("user_id", game.Challenger.Id).
						Msg("decline notice sent to user")

					r.deleteGame(game.Id)
				default:
					r.logger.Error().Interface("game", game).Str("value", responseSelection).
						Msg("challenge response receive unexpected value")
					utils.InteractionResponse(session, i.Interaction).Flags(discordgo.MessageFlagsEphemeral).
						Message("Something went wrong.").SendWithLog(r.logger)
					r.deleteGame(game.Id)
					return
				}

				//
			} else if strings.HasPrefix(messageComponentData.CustomID, "rps_move") {
				idSlice := strings.Split(messageComponentData.CustomID, "_")
				if len(idSlice) != 4 {
					r.logger.Error().Str("Id", messageComponentData.CustomID).Msg("button Id failed to split as expected")
					utils.InteractionResponse(session, i.Interaction).Flags(discordgo.MessageFlagsEphemeral).
						Message("Something went wrong.").SendWithLog(r.logger)
					return
				}

				// Gather necessary info from interaction
				gameId := idSlice[3]
				moveSelection := idSlice[2]
				userId := utils.GetInteractionUserId(i.Interaction)

				// Check if the game still exists
				game, ok := r.getGame(gameId)
				if !ok {
					utils.InteractionResponse(session, i.Interaction).Flags(discordgo.MessageFlagsEphemeral).
						Message("Game no longer exists.").SendWithLog(r.logger)
					return
				}

				// Store interaction input in active game
				if userId == game.Challenger.Id {
					game.Challenger.Selection = moveSelection
					r.setGame(game)

					// Update the prompt and remove the buttons
					messageEdit := discordgo.NewMessageEdit(game.Challenger.promptMessage.ChannelID, game.Challenger.promptMessage.ID)
					content := fmt.Sprintf("You selected :%s:.", moveSelection)
					messageEdit.Content = &content
					messageEdit.Components = []discordgo.MessageComponent{}
					_, _ = session.ChannelMessageEditComplex(messageEdit)

					r.logger.Debug().Interface("game", game).Str("user_id", game.Challenger.Id).
						Str("Selection", moveSelection).Msg("user made a Selection")
				} else if userId == game.Challenged.Id {
					game.Challenged.Selection = moveSelection
					r.setGame(game)

					// Update the prompt and remove the buttons
					messageEdit := discordgo.NewMessageEdit(game.Challenged.promptMessage.ChannelID, game.Challenged.promptMessage.ID)
					content := fmt.Sprintf("You selected :%s:.", moveSelection)
					messageEdit.Content = &content
					messageEdit.Components = []discordgo.MessageComponent{}
					_, _ = session.ChannelMessageEditComplex(messageEdit)

					r.logger.Debug().Interface("game", game).Str("user_id", game.Challenged.Id).
						Str("Selection", moveSelection).Msg("user made a Selection")
				} else {
					log.Error().Str("gameId", gameId).Str("userId", userId).Msg("user interacted with button not associated with their game")
					utils.InteractionResponse(session, i.Interaction).Flags(discordgo.MessageFlagsEphemeral).
						Message("Something went wrong. This isn't your game.").SendWithLog(r.logger)
					return
				}

				// Send a successful response to the interaction
				utils.InteractionResponse(session, i.Interaction).Type(discordgo.InteractionResponseDeferredMessageUpdate).
					SendWithLog(r.logger)

				r.winCheck(session, game)
			}
		}
	}

	return handlers
}

func (r *RpsPlugin) Commands() map[string]*discordgo.ApplicationCommand {
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

func (r *RpsPlugin) Intents() []discordgo.Intent {
	return nil
}

func (r *RpsPlugin) getGame(id string) (*rpsGame, bool) {
	r.gameLock.RLock()
	defer r.gameLock.RUnlock()

	game, ok := r.activeGames[id]
	return game, ok
}

func (r *RpsPlugin) setGame(game *rpsGame) {
	r.gameLock.Lock()
	r.activeGames[game.Id] = game
	r.gameLock.Unlock()
}

func (r *RpsPlugin) deleteGame(gameId string) {
	r.gameLock.Lock()
	if _, ok := r.activeGames[gameId]; ok {
		delete(r.activeGames, gameId)
	}
	r.gameLock.Unlock()
}

func (r *RpsPlugin) generateMove() string {
	return []string{rockValue, paperValue, scissorsValue}[rand.Int()%3]
}

func (r *RpsPlugin) moveCmp(m1, m2 string) int {
	switch m1 {
	case "":
		if m2 == "" {
			return 0
		} else {
			return 1
		}
	case rockValue:
		if m2 == rockValue {
			return 0
		} else if m2 == paperValue {
			return 1
		} else {
			return -1
		}
	case paperValue:
		if m2 == paperValue {
			return 0
		} else if m2 == scissorsValue {
			return 1
		} else {
			return -1
		}
	case scissorsValue:
		if m2 == scissorsValue {
			return 0
		} else if m2 == rockValue {
			return 1
		} else {
			return -1
		}
	}

	return 0
}

func (r *RpsPlugin) winCheck(session *discordgo.Session, game *rpsGame) {
	if session == nil || game == nil {
		return
	}

	r.winLock.Lock()
	defer r.winLock.Unlock()

	// Game already concluded
	if _, ok := r.getGame(game.Id); !ok {
		return
	}

	// Game not over yet
	if game.Challenger.Selection == "" || game.Challenged.Selection == "" {
		return
	}

	winnerMessage := fmt.Sprintf("<@%s> :%s:  :vs:  :%s: <@%s>\n", game.Challenger.Id, game.Challenger.Selection,
		game.Challenged.Selection, game.Challenged.Id)

	switch r.moveCmp(game.Challenger.Selection, game.Challenged.Selection) {
	case -1:
		winnerMessage += fmt.Sprintf("<@%s> wins!", game.Challenger.Id)
	case 0:
		winnerMessage += fmt.Sprintf("It's a tie!")
	case 1:
		winnerMessage += fmt.Sprintf("<@%s> wins!", game.Challenged.Id)
	}

	// If the game was launched in a channel, report the results back in the channel. Otherwise DM both
	// users.
	if game.ChallengeChannelId != "" {
		_, _ = session.ChannelMessageSend(game.ChallengeChannelId, winnerMessage)
	} else {
		// Send results to Challenger
		challengerUserChannel, _ := session.UserChannelCreate(game.Challenger.Id)
		_, _ = session.ChannelMessageSend(challengerUserChannel.ID, winnerMessage)

		// Send results to challengee (if not the bot itself)
		if game.Challenged.Id != session.State.User.ID {
			challengeeUserChannel, _ := session.UserChannelCreate(game.Challenged.Id)
			_, _ = session.ChannelMessageSend(challengeeUserChannel.ID, winnerMessage)
		}
	}

	// Cleanup the old messages
	_ = session.ChannelMessageDelete(game.Challenger.promptMessage.ChannelID, game.Challenger.promptMessage.ID)
	if game.Challenged.Id != session.State.User.ID {
		_ = session.ChannelMessageDelete(game.Challenged.promptMessage.ChannelID, game.Challenged.promptMessage.ID)
	}

	// Close out the game
	r.deleteGame(game.Id)
}

func newRpsGame(challengerId string, challengedId string) *rpsGame {
	return &rpsGame{
		Challenger: rpsUser{
			Id: challengerId,
		},
		Challenged: rpsUser{
			Id: challengedId,
		},
		Id: fmt.Sprintf("%svs%s", challengerId, challengedId),
	}
}

func (r *rpsGame) generateChallenge(gameId string, challenger string, message string, enabled bool) *discordgo.MessageSend {
	challengeString := fmt.Sprintf("<@%s> Challenged you to rock paper scissors.", challenger)
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

func (r *rpsGame) generatePrompt(gameId string, enabled bool) *discordgo.MessageSend {
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
						CustomID: fmt.Sprintf("rps_move_%s_%s", rockValue, gameId),
						Disabled: !enabled,
					},
					discordgo.Button{
						Label:    "Paper",
						Style:    discordgo.PrimaryButton,
						CustomID: fmt.Sprintf("rps_move_%s_%s", paperValue, gameId),
						Disabled: !enabled,
					},
					discordgo.Button{
						Label:    "Scissors",
						Style:    discordgo.PrimaryButton,
						CustomID: fmt.Sprintf("rps_move_%s_%s", scissorsValue, gameId),
						Disabled: !enabled,
					},
				},
			},
		},
	}

	return &response
}

func (r *rpsGame) generateDecline(userId string) *discordgo.MessageSend {
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

	return &discordgo.MessageSend{
		Content: options[rand.Int()%len(options)],
	}
}

func (r *rpsGame) sendMessage(session *discordgo.Session, user rpsUser, messageSend *discordgo.MessageSend) (*discordgo.Message, error) {
	channel, err := session.UserChannelCreate(user.Id)
	if err != nil {
		return nil, err
	}

	return session.ChannelMessageSendComplex(channel.ID, messageSend)
}
