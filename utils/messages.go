package utils

import "github.com/bwmarrin/discordgo"

type ButtonBuilder struct {
	button discordgo.Button
}

type ActionsRowBuilder struct {
	messageComponents []discordgo.MessageComponent
}

func Button() *ButtonBuilder {
	return &ButtonBuilder{
		button: discordgo.Button{
			Style:    discordgo.PrimaryButton,
			Label:    "Button",
			Disabled: false,
		},
	}
}

func (b *ButtonBuilder) Style(style discordgo.ButtonStyle) *ButtonBuilder {
	b.button.Style = style
	return b
}

func (b *ButtonBuilder) Label(label string) *ButtonBuilder {
	b.button.Label = label
	return b
}

func (b *ButtonBuilder) Enabled(enabled bool) *ButtonBuilder {
	b.button.Disabled = !enabled
	return b
}

func (b *ButtonBuilder) URL(url string) *ButtonBuilder {
	b.button.URL = url
	return b
}

func (b *ButtonBuilder) Id(id string) *ButtonBuilder {
	b.button.CustomID = id
	return b
}

func (b *ButtonBuilder) Build() discordgo.Button {
	return b.button
}

func ActionsRow() *ActionsRowBuilder {
	return &ActionsRowBuilder{}
}

func (a *ActionsRowBuilder) Button(button discordgo.Button) *ActionsRowBuilder {
	a.messageComponents = append(a.messageComponents, button)
	return a
}

func (a *ActionsRowBuilder) SelectMenu() *ActionsRowBuilder {
	selectMenu := discordgo.SelectMenu{
		CustomID:    "",
		Placeholder: "",
		MinValues:   nil,
		MaxValues:   0,
		Options:     nil,
		Disabled:    false,
	}

	a.messageComponents = append(a.messageComponents, selectMenu)

	return a
}

func (a *ActionsRowBuilder) Build() discordgo.ActionsRow {
	return discordgo.ActionsRow{
		Components: a.messageComponents,
	}
}
