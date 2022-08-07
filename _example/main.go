package main

import (
	"github.com/bwmarrin/discordgo"
	"github.com/olympus-go/eris"
	"github.com/olympus-go/eris/plugins"
	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
	"os"
	"os/signal"
	"syscall"
)

func main() {
	log.Logger = log.Output(zerolog.ConsoleWriter{Out: os.Stderr})

	token, ok := os.LookupEnv("DISCORD_TOKEN")
	if !ok {
		log.Fatal().Msg("env var DISCORD_TOKEN not set")
	}
	appId, ok := os.LookupEnv("DISCORD_APP_ID")
	if !ok {
		log.Fatal().Msg("env var DISCORD_APP_ID not set")
	}

	bot, err := eris.NewBot(token, appId)
	if err != nil {
		log.Fatal().Err(err).Msg("failed to create discord session")
	}

	bot.AddPlugin(plugins.Stats())

	bot.DiscordSession.Identify.Intents = discordgo.IntentsGuilds | discordgo.IntentsGuildMessages |
		discordgo.IntentsGuildVoiceStates | discordgo.IntentMessageContent

	err = bot.Start()
	if err != nil {
		log.Fatal().Err(err).Msg("failed to open discord session")
	}

	sc := make(chan os.Signal, 1)
	signal.Notify(sc, syscall.SIGINT, syscall.SIGTERM, os.Interrupt, os.Kill)
	<-sc

	if err = bot.Stop(); err != nil {
		log.Fatal().Err(err).Msg("failed to close discord session")
	}
}
