package plugins

import (
	"github.com/bwmarrin/discordgo"
	"github.com/eolso/apollo/spotify"
	"github.com/eolso/eris"
	"github.com/rs/zerolog/log"
)

type spotifyPlugin struct {
	player          *spotify.Player
	isInVoice       bool
	voiceConnection *discordgo.VoiceConnection
}

func Spotify() eris.Plugin {
	return &spotifyPlugin{
		player: spotify.NewPlayer(),
	}
}

func (s *spotifyPlugin) Name() string {
	return "Spotify"
}

func (s *spotifyPlugin) Description() string {
	return "Play spotify songs in voice chats"
}

func (s *spotifyPlugin) Handlers() map[string]any {
	handlers := make(map[string]any)

	handlers["spotify_join_handler"] = func(session *discordgo.Session, i *discordgo.InteractionCreate) {
		switch i.Type {
		case discordgo.InteractionApplicationCommand:
			applicationCommandData := i.ApplicationCommandData()
			if applicationCommandData.Name != "spotify" || applicationCommandData.Options[0].Name != "join" {
				return
			}

			if s.isInVoice {
				_ = session.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
					Type: discordgo.InteractionResponseChannelMessageWithSource,
					Data: &discordgo.InteractionResponseData{
						Content: "I'm already here!",
						Flags:   uint64(discordgo.MessageFlagsEphemeral),
					},
				})
			} else {
				guild, err := session.State.Guild(i.GuildID)
				if err != nil {
					log.Error().Err(err).Msg("failed to fetch guild from id")
				}

				for _, voiceState := range guild.VoiceStates {
					if i.Member != nil && i.Member.User.ID == voiceState.UserID {
						s.voiceConnection, err = session.ChannelVoiceJoin(guild.ID, voiceState.ChannelID, false, true)
						if err != nil {
							log.Error().Err(err).Msg("failed to join voice channel")
						}
					}
				}
				s.isInVoice = true
				session.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
					Type: discordgo.InteractionResponseDeferredChannelMessageWithSource,
				})
			}
		default:
		}
	}

	handlers["spotify_play_handler"] = func(session *discordgo.Session, i *discordgo.InteractionCreate) {
		switch i.Type {
		case discordgo.InteractionApplicationCommand:
			applicationCommandData := i.ApplicationCommandData()
			if !eris.CheckApplicationCommandData(applicationCommandData, "spotify", "play") {
				return
			}

			song, artist, album := parsePlayOptions(applicationCommandData.Options[0].Options...)
			_ = session.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
				Type: discordgo.InteractionResponseChannelMessageWithSource,
				Data: &discordgo.InteractionResponseData{
					Content: song + artist + album,
					Flags:   uint64(discordgo.MessageFlagsEphemeral),
				},
			})

		}
	}

	return handlers
}

func (s *spotifyPlugin) Commands() map[string]*discordgo.ApplicationCommand {
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
						Name:        "name",
						Description: "The name of the song",
						Type:        discordgo.ApplicationCommandOptionString,
						Required:    true,
					},
					{
						Name:        "artist",
						Description: "The artist of the song",
						Type:        discordgo.ApplicationCommandOptionString,
						Required:    false,
					},
					{
						Name:        "album",
						Description: "The album of the song",
						Type:        discordgo.ApplicationCommandOptionString,
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
		},
	}

	return commands
}

func (s *spotifyPlugin) Intents() []discordgo.Intent {
	return nil
}

func parsePlayOptions(options ...*discordgo.ApplicationCommandInteractionDataOption) (song string, artist string, album string) {
	for _, option := range options {
		switch option.Name {
		case "name":
			song, _ = option.Value.(string)
		case "artist":
			artist, _ = option.Value.(string)
		case "album":
			album, _ = option.Value.(string)
		}
	}

	return song, artist, album
}
