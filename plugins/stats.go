package plugins

import (
	"fmt"
	"github.com/bwmarrin/discordgo"
	"github.com/olympus-go/eris/utils"
	"runtime"
	"time"
)

const statusString = `Uptime: %s
Runtime: %s
Version: %s`

type StatsPlugin struct {
	startTime time.Time
	runtime   string
}

func Stats() StatsPlugin {
	return StatsPlugin{
		startTime: time.Now(),
		runtime:   fmt.Sprintf("%s/%s", runtime.GOOS, runtime.GOARCH),
	}
}

func (s StatsPlugin) Name() string {
	return "Stats"
}

func (s StatsPlugin) Description() string {
	return "Prints out various technical stats"
}

func (s StatsPlugin) Handlers() map[string]any {
	handlers := make(map[string]any)

	handlers["stats_handler"] = func(session *discordgo.Session, i *discordgo.InteractionCreate) {
		switch i.Type {
		case discordgo.InteractionApplicationCommand:
			if i.ApplicationCommandData().Name == "stats" {
				message := fmt.Sprintf(statusString, time.Since(s.startTime).Round(time.Second).String(), s.runtime, "v0.0.1")
				_ = utils.SendEphemeralInteractionResponse(session, i.Interaction, "```"+message+"```")
			}
		}
	}

	return handlers
}

func (s StatsPlugin) Commands() map[string]*discordgo.ApplicationCommand {
	commands := make(map[string]*discordgo.ApplicationCommand)

	commands["stats_command"] = &discordgo.ApplicationCommand{
		Name:        "stats",
		Description: "Displays bot stats",
	}

	return commands
}

func (s StatsPlugin) Intents() []discordgo.Intent {
	return nil
}
