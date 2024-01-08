package utils

import (
	"log/slog"

	"github.com/bwmarrin/discordgo"
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

// Deferred marks the response as "will respond later"
func (i *InteractionResponseBuilder) Deferred() *InteractionResponseBuilder {
	i.response.Type = discordgo.InteractionResponseDeferredChannelMessageWithSource
	return i
}

// DeferredUpdate marks the response as "will update response later"
func (i *InteractionResponseBuilder) DeferredUpdate() *InteractionResponseBuilder {
	i.response.Type = discordgo.InteractionResponseDeferredMessageUpdate
	return i
}

func (i *InteractionResponseBuilder) Message(message string) *InteractionResponseBuilder {
	i.response.Data.Content = message
	return i
}

func (i *InteractionResponseBuilder) Flags(flags discordgo.MessageFlags) *InteractionResponseBuilder {
	i.response.Data.Flags = flags
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

func (i *InteractionResponseBuilder) SendWithLog(logger *slog.Logger) {
	if err := i.Send(); err != nil {
		logger.Error("failed to respond to interaction",
			slog.String("error", err.Error()),
			slog.Any("interaction", i.interaction),
		)
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

func (i *InteractionResponseBuilder) FollowUpCreateWithLog(logger *slog.Logger) {
	if _, err := i.FollowUpCreate(); err != nil {
		logger.Error("failed to create followup",
			slog.String("error", err.Error()),
			slog.Any("interaction", i.interaction),
		)
	}
}

func (i *InteractionResponseBuilder) FollowUpEdit(id string) (*discordgo.Message, error) {
	webhookParams := &discordgo.WebhookEdit{
		Content:    &i.response.Data.Content,
		Components: &i.response.Data.Components,
		Embeds:     &i.response.Data.Embeds,
	}

	return i.session.FollowupMessageEdit(i.interaction, id, webhookParams)
}

func (i *InteractionResponseBuilder) FollowUpEditWithLog(id string, logger *slog.Logger) {
	if _, err := i.FollowUpEdit(id); err != nil {
		logger.Error("failed to edit followup",
			slog.String("error", err.Error()),
			slog.Any("interaction", i.interaction),
		)
	}
}

func (i *InteractionResponseBuilder) FollowUpDelete(id string) error {
	return i.session.FollowupMessageDelete(i.interaction, id)
}

func (i *InteractionResponseBuilder) FollowUpDeleteWithLog(id string, logger *slog.Logger) {
	if err := i.FollowUpDelete(id); err != nil {
		logger.Error("failed to delete followup",
			slog.String("error", err.Error()),
			slog.Any("interaction", i.interaction),
		)
	}
}

func (i *InteractionResponseBuilder) Edit() error {
	webhookEdit := &discordgo.WebhookEdit{
		Content:    &i.response.Data.Content,
		Embeds:     &i.response.Data.Embeds,
		Components: &i.response.Data.Components,
	}

	_, err := i.session.InteractionResponseEdit(i.interaction, webhookEdit)

	return err
}

func (i *InteractionResponseBuilder) EditWithLog(logger *slog.Logger) {
	if err := i.Edit(); err != nil {
		logger.Error("failed to edit interaction",
			slog.String("error", err.Error()),
			slog.Any("interaction", i.interaction),
		)
	}
}

func (i *InteractionResponseBuilder) Delete() error {
	return i.session.InteractionResponseDelete(i.interaction)
}

func (i *InteractionResponseBuilder) DeleteWithLog(logger *slog.Logger) {
	if err := i.Delete(); err != nil {
		logger.Error("failed to delete interaction",
			slog.String("error", err.Error()),
			slog.Any("interaction", i.interaction),
		)
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

func GetInteractionUser(interaction *discordgo.Interaction) map[string]string {
	return map[string]string{
		"id":   GetInteractionUserId(interaction),
		"name": GetInteractionUserName(interaction),
	}
}

func GetInteractionUserVoiceStateId(session *discordgo.Session, interaction *discordgo.Interaction) string {
	if interaction.Member == nil {
		return ""
	}

	guild, err := session.State.Guild(interaction.GuildID)
	if err != nil {
		return ""
	}

	for _, voiceState := range guild.VoiceStates {
		if interaction.Member.User.ID == voiceState.UserID {
			return voiceState.ChannelID
		}
	}

	return ""
}
