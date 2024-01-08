package eris

import (
	"fmt"
	"log/slog"

	"github.com/bwmarrin/discordgo"
	"github.com/olympus-go/eris/utils"
)

// Plugin is the baseline interface for extending bot behavior.
type Plugin interface {
	Name() string
	Description() string
	Handlers() map[string]any
	Commands() map[string]*discordgo.ApplicationCommand
	Intents() []discordgo.Intent
}

type PluginManager struct {
	plugins *map[string]Plugin
	logger  *slog.Logger
}

func (p PluginManager) Name() string {
	return "Plugin Info"
}

func (p PluginManager) Description() string {
	return "Lists currently loaded plugins"
}

func (p PluginManager) Handlers() map[string]any {
	handlers := make(map[string]any)

	handlers["plugin_handler"] = func(session *discordgo.Session, i *discordgo.InteractionCreate) {
		switch i.Type {
		case discordgo.InteractionApplicationCommand:
			if i.ApplicationCommandData().Name == "plugins" {
				message := ""

				for _, plugin := range *p.plugins {
					message += fmt.Sprintf("%s - %s\n", plugin.Name(), plugin.Description())
				}

				utils.InteractionResponse(session, i.Interaction).
					Ephemeral().
					Message("```" + message + "```").
					SendWithLog(p.logger)
			}
		}
	}

	return handlers
}

func (p PluginManager) Commands() map[string]*discordgo.ApplicationCommand {
	commands := make(map[string]*discordgo.ApplicationCommand)

	commands["plugin_cmd"] = &discordgo.ApplicationCommand{
		Name:        "plugins",
		Description: "Lists currently loaded plugins",
	}

	return commands
}

func (p PluginManager) Intents() []discordgo.Intent {
	return []discordgo.Intent{
		discordgo.IntentsGuildMessages,
	}
}
