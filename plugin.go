package eris

import "github.com/bwmarrin/discordgo"

type Plugin interface {
	Name() string
	Description() string
	Handlers() map[string]any
	Commands() map[string]*discordgo.ApplicationCommand
	Intents() []discordgo.Intent
}
