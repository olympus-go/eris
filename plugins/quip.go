package plugins

import (
	"github.com/bwmarrin/discordgo"
	"strings"
)

type QuipPlugin struct {
	quips []struct {
		triggerType string
		trigger     string
		response    string
	}
}

func Quip() *QuipPlugin {
	var plugin QuipPlugin

	plugin.newQuip("contains", "surely you can't be serious", "I am serious, and don't call me shirley")
	plugin.newQuip("contains", "thanks george", "I gotchu boo")
	plugin.newQuip("is", "george", "what?")
	plugin.newQuip("starts_with", "george play", "SIKE you thought")
	plugin.newQuip("starts_with", "g play", "SIKE you thought")
	plugin.newQuip("is", "brb", "I miss you already")
	plugin.newQuip("contains", "wanna play some paladins?", "you never invite me to play paladins :(")
	plugin.newQuip("contains", "george do you wanna play some paladins?", "no hehehehe")
	plugin.newQuip("contains", "do you wanna play some paladins george?", "no hehehehe")
	plugin.newQuip("contains", "george do you wanna play?", "no hehehehe")
	plugin.newQuip("contains", "do you wanna play george?", "no hehehehe")
	plugin.newQuip("contains", "what's 9 + 10?", "21")

	return &plugin
}

func (q *QuipPlugin) Name() string {
	return "Quip"
}

func (q *QuipPlugin) Description() string {
	return "Enables configurable quips"
}

func (q *QuipPlugin) Handlers() map[string]any {
	handlers := make(map[string]any)

	handlers["quip_handler"] = func(session *discordgo.Session, message *discordgo.MessageCreate) {
		for _, quip := range q.quips {
			switch quip.triggerType {
			case "contains":
				if strings.Contains(strings.ToLower(message.Content), strings.ToLower(quip.trigger)) {
					_, _ = session.ChannelMessageSend(message.ChannelID, quip.response)
				}
			case "starts_with":
				if strings.HasPrefix(strings.ToLower(message.Content), strings.ToLower(quip.trigger)) {
					_, _ = session.ChannelMessageSend(message.ChannelID, quip.response)
				}
			case "ends_with":
				if strings.HasSuffix(strings.ToLower(message.Content), strings.ToLower(quip.trigger)) {
					_, _ = session.ChannelMessageSend(message.ChannelID, quip.response)
				}
			case "is":
				if strings.ToLower(message.Content) == strings.ToLower(quip.trigger) {
					_, _ = session.ChannelMessageSend(message.ChannelID, quip.response)
				}
			}
		}
	}

	return handlers
}

func (q *QuipPlugin) Commands() map[string]*discordgo.ApplicationCommand {
	return nil
}

func (q *QuipPlugin) Intents() []discordgo.Intent {
	return nil
}

func (q *QuipPlugin) newQuip(triggerType string, trigger string, response string) {
	quip := struct {
		triggerType string
		trigger     string
		response    string
	}{
		triggerType: triggerType,
		trigger:     trigger,
		response:    response,
	}

	q.quips = append(q.quips, quip)
}
