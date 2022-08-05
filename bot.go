package eris

import (
	"fmt"
	"github.com/bwmarrin/discordgo"
	"github.com/eolso/eris/utils"
	"github.com/rs/zerolog/log"
)

type Bot struct {
	DiscordSession *discordgo.Session
	appId          string
	handlers       map[string]func()
	commands       map[string]func()
	plugins        []Plugin
	ErrChan        chan error
}

func NewBot(token string, appId string) (*Bot, error) {
	var bot Bot
	var err error

	bot.DiscordSession, err = discordgo.New("Bot " + token)
	if err != nil {
		return nil, err
	}

	bot.handlers = make(map[string]func())
	bot.ErrChan = make(chan error, 100)
	bot.appId = appId

	go func() {
		for err := range bot.ErrChan {
			if err != nil {
				log.Error().Err(err).Msg("")
			}
		}
	}()

	bot.AddCommand(bot.pluginCommand(), "158727419941879818")
	bot.AddHandler("eris_plugins", bot.pluginHandler())

	return &bot, nil
}

func (b *Bot) AddHandler(name string, handler any) {
	if _, ok := b.handlers[name]; ok {
		b.handlers[name]()
	}

	b.handlers[name] = b.DiscordSession.AddHandler(handler)
}

func (b *Bot) RemoveHandler(name string) {
	if _, ok := b.handlers[name]; ok {
		b.handlers[name]()
		delete(b.handlers, name)
	}
}

func (b *Bot) AddCommand(cmd *discordgo.ApplicationCommand, guildIds ...string) {
	if len(guildIds) == 0 {
		guildIds = []string{""}
	}

	var commandFound bool
	for _, guildId := range guildIds {
		commandFound = false
		registeredCommands, _ := b.DiscordSession.ApplicationCommands(b.appId, guildId)
		for _, registeredCommand := range registeredCommands {
			if CompareApplicationCommand(*cmd, *registeredCommand) {
				log.Debug().Str("command_name", cmd.Name).Msg("skipping command already registered")
				commandFound = true
			}
		}

		if !commandFound {
			_, err := b.DiscordSession.ApplicationCommandCreate(b.appId, guildId, cmd)
			if err != nil {
				b.ErrChan <- err
			}
		}
	}
}

func (b *Bot) RemoveCommand(cmdId string, guildIds ...string) {
	if len(guildIds) == 0 {
		b.ErrChan <- b.DiscordSession.ApplicationCommandDelete(b.appId, "", cmdId)
	} else {
		for _, guildId := range guildIds {
			b.ErrChan <- b.DiscordSession.ApplicationCommandDelete(b.appId, guildId, cmdId)
		}
	}
}

func (b *Bot) AddPlugin(plugin Plugin, guildIds ...string) {
	b.plugins = append(b.plugins, plugin)

	handlers := plugin.Handlers()
	for name, handler := range handlers {
		b.AddHandler(name, handler)
	}

	commands := plugin.Commands()
	for _, command := range commands {
		b.AddCommand(command, guildIds...)
	}
}

func (b *Bot) Id() string {
	return b.appId
}

func (b *Bot) Start() error {
	return b.DiscordSession.Open()
}

func (b *Bot) pluginCommand() *discordgo.ApplicationCommand {
	return &discordgo.ApplicationCommand{
		Name:        "plugins",
		Description: "Lists currently loaded plugins",
	}
}

func (b *Bot) pluginHandler() any {
	return func(session *discordgo.Session, i *discordgo.InteractionCreate) {
		switch i.Type {
		case discordgo.InteractionApplicationCommand:
			if i.ApplicationCommandData().Name == "plugins" {
				message := ""
				for _, plugin := range b.plugins {
					message += fmt.Sprintf("%s - %s\n", plugin.Name(), plugin.Description())
				}
				_ = utils.SendEphemeralInteractionResponse(session, i.Interaction, "```"+message+"```")
			}
		}
	}
}
