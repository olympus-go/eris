package plugins

import (
	"fmt"
	"github.com/bwmarrin/discordgo"
	"github.com/olympus-go/eris/utils"
	"github.com/rs/zerolog"
	"runtime"
	"time"
)

type StatsPlugin struct {
	data []struct {
		key   string
		value func() string
	}
	logger zerolog.Logger
}

func Stats(logger zerolog.Logger) *StatsPlugin {
	plugin := StatsPlugin{
		logger: logger.With().Str("plugin", "stats").Logger(),
	}

	startTime := time.Now()
	plugin.AddStatFunc("Uptime", func() string { return time.Since(startTime).Round(time.Second).String() })
	plugin.AddStat("Runtime", fmt.Sprintf("%s/%s", runtime.GOOS, runtime.GOARCH))

	return &plugin
}

func (s *StatsPlugin) Name() string {
	return "Stats"
}

func (s *StatsPlugin) Description() string {
	return "Prints out various technical stats"
}

func (s *StatsPlugin) Handlers() map[string]any {
	handlers := make(map[string]any)

	handlers["stats_handler"] = func(session *discordgo.Session, i *discordgo.InteractionCreate) {
		switch i.Type {
		case discordgo.InteractionApplicationCommand:
			if i.ApplicationCommandData().Name == "stats" {
				message := ""
				for _, stat := range s.data {
					message += fmt.Sprintf("%s: %s\n", stat.key, stat.value())
				}

				utils.InteractionResponse(session, i.Interaction).Ephemeral().
					Message("```" + message + "```").SendWithLog(s.logger)
			}
		}
	}

	return handlers
}

func (s *StatsPlugin) Commands() map[string]*discordgo.ApplicationCommand {
	commands := make(map[string]*discordgo.ApplicationCommand)

	commands["stats_command"] = &discordgo.ApplicationCommand{
		Name:        "stats",
		Description: "Displays bot stats",
	}

	return commands
}

func (s *StatsPlugin) Intents() []discordgo.Intent {
	return nil
}

func (s *StatsPlugin) AddStat(key string, value string) {
	kv := struct {
		key   string
		value func() string
	}{
		key,
		func() string { return value },
	}

	s.data = append(s.data, kv)
}

func (s *StatsPlugin) AddStatFunc(key string, value func() string) {
	kv := struct {
		key   string
		value func() string
	}{
		key,
		value,
	}

	s.data = append(s.data, kv)
}
