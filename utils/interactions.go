package utils

import "github.com/bwmarrin/discordgo"

type InteractionResponseBuilder struct {
	session     *discordgo.Session
	interaction *discordgo.Interaction
	response    *discordgo.InteractionResponse
}

func InteractionResponse(session *discordgo.Session, interaction *discordgo.Interaction) *InteractionResponseBuilder {
	return &InteractionResponseBuilder{
		session:     session,
		interaction: interaction,
		response: &discordgo.InteractionResponse{
			Type: discordgo.InteractionResponseChannelMessageWithSource,
			Data: &discordgo.InteractionResponseData{},
		},
	}
}

func (i *InteractionResponseBuilder) Type(t discordgo.InteractionResponseType) *InteractionResponseBuilder {
	i.response.Type = t
	return i
}

func (i *InteractionResponseBuilder) Message(message string) *InteractionResponseBuilder {
	i.response.Data.Content = message
	return i
}

func (i *InteractionResponseBuilder) Flags(flags discordgo.MessageFlags) *InteractionResponseBuilder {
	i.response.Data.Flags = uint64(flags)
	return i
}

func (i *InteractionResponseBuilder) Components(components ...discordgo.MessageComponent) *InteractionResponseBuilder {
	i.response.Data.Components = components
	return i
}

func (i *InteractionResponseBuilder) Send() error {
	return i.session.InteractionRespond(i.interaction, i.response)
}

func SendInteractionResponse(session *discordgo.Session, interaction *discordgo.Interaction, message string, flags discordgo.MessageFlags) error {
	return session.InteractionRespond(interaction, &discordgo.InteractionResponse{
		Type: discordgo.InteractionResponseChannelMessageWithSource,
		Data: &discordgo.InteractionResponseData{
			Content: message,
		},
	})
}

func SendEphemeralInteractionResponse(session *discordgo.Session, interaction *discordgo.Interaction, message string, components ...discordgo.MessageComponent) error {
	return session.InteractionRespond(interaction, &discordgo.InteractionResponse{
		Type: discordgo.InteractionResponseChannelMessageWithSource,
		Data: &discordgo.InteractionResponseData{
			Content:    message,
			Components: components,
			Flags:      uint64(discordgo.MessageFlagsEphemeral),
		},
	})
}

func GetInteractionUserId(interaction *discordgo.Interaction) string {
	if interaction.User != nil {
		return interaction.User.ID
	}

	if interaction.Member != nil {
		return interaction.Member.User.ID
	}

	return ""
}

func GetInteractionUserName(interaction *discordgo.Interaction) string {
	if interaction.User != nil {
		return interaction.User.Username
	}

	if interaction.Member != nil {
		if interaction.Member.Nick != "" {
			return interaction.Member.Nick
		}
		if interaction.Member.User != nil {
			return interaction.Member.User.Username
		}
	}

	return ""
}

// CheckApplicationCommandData checks a discordgo.ApplicationCommandInteractionData for the matching name and subsequent option names.
func CheckApplicationCommandData(command discordgo.ApplicationCommandInteractionData, name string, options ...string) bool {
	if command.Name != name || len(command.Options) != len(options) {
		return false
	}

	for index, option := range options {
		if command.Options[index].Name != option {
			return false
		}
	}

	return true
}
