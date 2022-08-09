package utils

import (
	"fmt"
	"github.com/bwmarrin/discordgo"
	"github.com/rs/zerolog"
)

type InteractionResponseBuilder struct {
	session     *discordgo.Session
	interaction *discordgo.Interaction
	message     *discordgo.Message
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

// Ephemeral is a convenience function that calls Flags(discordgo.MessageFlagsEphemeral).
func (i *InteractionResponseBuilder) Ephemeral() *InteractionResponseBuilder {
	return i.Flags(discordgo.MessageFlagsEphemeral)
}

func (i *InteractionResponseBuilder) Components(components ...discordgo.MessageComponent) *InteractionResponseBuilder {
	i.response.Data.Components = components
	return i
}

func (i *InteractionResponseBuilder) Response(response *discordgo.InteractionResponse) *InteractionResponseBuilder {
	i.response = response
	return i
}

func (i *InteractionResponseBuilder) Send() error {
	return i.session.InteractionRespond(i.interaction, i.response)
}

func (i *InteractionResponseBuilder) SendWithLog(logger zerolog.Logger) {
	if err := i.Send(); err != nil {
		logger.Error().Err(err).Interface("interaction", i.interaction).Msg("failed to respond to interaction")
	}
}

func (i *InteractionResponseBuilder) Edit() error {
	message, err := i.session.InteractionResponse(i.interaction)
	if message == nil || err != nil {
		return fmt.Errorf("can't edit unavailable interaction")
	}

	messageEdit := discordgo.NewMessageEdit(message.ChannelID, message.ID)
	messageEdit.Content = &i.response.Data.Content
	messageEdit.Components = i.response.Data.Components

	_, err = i.session.ChannelMessageEditComplex(messageEdit)

	return err
}

func (i *InteractionResponseBuilder) EditWithLog(logger zerolog.Logger) {
	if err := i.Edit(); err != nil {
		logger.Error().Err(err).Interface("interaction", i.interaction).Msg("failed to edit interaction")
	}
}

func (i *InteractionResponseBuilder) Delete() error {
	return i.session.InteractionResponseDelete(i.interaction)
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

// SendEphemeralInteractionResponse TODO Deprecated
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
