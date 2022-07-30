package plugins

import (
	"github.com/bwmarrin/discordgo"
	"github.com/eolso/eris"
	"strings"
)

type quip struct {
}

func Quip() eris.Plugin {
	return quip{}
}

func (q quip) Name() string {
	return "Quip"
}

func (q quip) Description() string {
	return "Enables configurable quips"
}

func (q quip) Handlers() map[string]any {
	handlers := make(map[string]any)

	handlers["quip_handler"] = func(session *discordgo.Session, message *discordgo.MessageCreate) {
		if strings.Contains(message.Content, "surely you can't be serious") {
			_, _ = session.ChannelMessageSend(message.ChannelID, "I am serious, and don't call me Shirley.")
		}
	}

	return handlers
}

func (q quip) Commands() map[string]*discordgo.ApplicationCommand {
	return nil
}

func (q quip) Intents() []discordgo.Intent {
	return nil
}

func QuipHandler(handler func(*discordgo.Session, *discordgo.MessageCreate)) func(*eris.Bot) {
	return func(b *eris.Bot) {
		b.AddHandler("quip", handler)
	}
}

func DefaultQuip(bot *eris.Bot) {
	defaultQuipHandler := func(session *discordgo.Session, message *discordgo.MessageCreate) {
		if strings.Contains(message.Content, "surely you can't be serious") {
			_, err := session.ChannelMessageSend(message.ChannelID, "I am serious, and don't call me Shirley.")
			if err != nil {
				bot.ErrChan <- err
			}
		}
	}

	bot.AddHandler("quip", defaultQuipHandler)
}
