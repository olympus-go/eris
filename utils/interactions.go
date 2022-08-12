package utils

import (
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
	if len(components) == 0 {
		i.response.Data.Components = []discordgo.MessageComponent{}
	} else {
		i.response.Data.Components = components
	}
	return i
}

func (i *InteractionResponseBuilder) Embeds(embeds ...*discordgo.MessageEmbed) *InteractionResponseBuilder {
	if len(embeds) == 0 {
		i.response.Data.Embeds = nil
	} else {
		i.response.Data.Embeds = embeds
	}
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

func (i *InteractionResponseBuilder) FollowUpCreate() (*discordgo.Message, error) {
	webhookParams := &discordgo.WebhookParams{
		Content:    i.response.Data.Content,
		Components: i.response.Data.Components,
		Embeds:     i.response.Data.Embeds,
		Flags:      i.response.Data.Flags,
	}

	return i.session.FollowupMessageCreate(i.interaction, true, webhookParams)
}

func (i *InteractionResponseBuilder) FollowUpEdit(id string) (*discordgo.Message, error) {
	webhookParams := &discordgo.WebhookEdit{
		Content:    i.response.Data.Content,
		Components: i.response.Data.Components,
		Embeds:     i.response.Data.Embeds,
	}

	return i.session.FollowupMessageEdit(i.interaction, id, webhookParams)
}

func (i *InteractionResponseBuilder) FollowUpDelete(id string) error {
	return i.session.FollowupMessageDelete(i.interaction, id)
}

func (i *InteractionResponseBuilder) Edit() error {
	webhookEdit := &discordgo.WebhookEdit{
		Content:    i.response.Data.Content,
		Embeds:     i.response.Data.Embeds,
		Components: i.response.Data.Components,
	}

	_, err := i.session.InteractionResponseEdit(i.interaction, webhookEdit)

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

func (i *InteractionResponseBuilder) DeleteWithLog(logger zerolog.Logger) {
	if err := i.Delete(); err != nil {
		logger.Error().Err(err).Interface("interaction", i.interaction).Msg("failed to delete interaction")
	}
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
