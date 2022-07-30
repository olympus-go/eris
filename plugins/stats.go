package plugins

import (
	"fmt"
	"github.com/bwmarrin/discordgo"
	"github.com/eolso/eris"
	"runtime"
	"time"
)

const statusString = `Uptime: %s
Runtime: %s
Version: %s`

type stats struct {
	startTime time.Time
	runtime   string
}

func Stats() eris.Plugin {
	return stats{
		startTime: time.Now(),
		runtime:   fmt.Sprintf("%s/%s", runtime.GOOS, runtime.GOARCH),
	}
}

func (s stats) Name() string {
	return "Stats"
}

func (s stats) Description() string {
	return "Prints out various technical stats"
}

func (s stats) Handlers() map[string]any {
	handlers := make(map[string]any)

	handlers["stats_handler"] = func(session *discordgo.Session, i *discordgo.InteractionCreate) {
		switch i.Type {
		case discordgo.InteractionApplicationCommand:
			if i.ApplicationCommandData().Name == "stats" {
				_ = session.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
					Type: discordgo.InteractionResponseChannelMessageWithSource,
					Data: &discordgo.InteractionResponseData{
						Content: "```" + fmt.Sprintf(statusString,
							time.Since(s.startTime).Round(time.Second).String(),
							s.runtime,
							eris.Version) + "```",
					},
				})
			}
		}
	}

	return handlers
}

func (s stats) Commands() map[string]*discordgo.ApplicationCommand {
	commands := make(map[string]*discordgo.ApplicationCommand)

	commands["stats_command"] = &discordgo.ApplicationCommand{
		Name:        "stats",
		Description: "Displays bot stats",
	}

	return commands
}

func (s stats) Intents() []discordgo.Intent {
	return nil
}
