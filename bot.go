package eris

import (
	"fmt"
	"log/slog"

	"github.com/bwmarrin/discordgo"
	"github.com/olympus-go/eris/utils"
)

type BotState int

const (
	StartedState BotState = iota
	StoppedState
	UnknownState
)

type Bot struct {
	discordSession *discordgo.Session
	handlers       map[string]func()
	commands       map[string]func()
	plugins        map[string]Plugin
	state          BotState
	Logger         *slog.Logger
}

func NewBot(config Config, h slog.Handler) (*Bot, error) {
	var err error

	if h == nil {
		h = utils.NopLogHandler{}
	}

	bot := Bot{
		handlers: make(map[string]func()),
		commands: make(map[string]func()),
		plugins:  make(map[string]Plugin),
		state:    UnknownState,
		Logger:   slog.New(h),
	}

	bot.discordSession, err = discordgo.New("Bot " + config.Token)
	if err != nil {
		return nil, err
	}

	if err = bot.Start(); err != nil {
		return nil, err
	}

	bot.Logger = bot.Logger.With(slog.Any("eris", bot.botData()))

	bot.AddPlugin(PluginManager{
		plugins: &bot.plugins,
		logger:  slog.New(bot.Logger.Handler()),
	})

	return &bot, nil
}

func (b *Bot) AddHandler(name string, handler any) {
	if _, ok := b.handlers[name]; ok {
		b.handlers[name]()
	}

	b.handlers[name] = b.discordSession.AddHandler(handler)
}

func (b *Bot) RemoveHandler(name string) {
	if _, ok := b.handlers[name]; ok {
		b.handlers[name]()
		delete(b.handlers, name)
	}
}

// AddCommand registers an ApplicationCommand to the specified guild Ids (global if empty). The supplied command will
// be compared against currently registered commands to prevent re-registering the same exact command.
func (b *Bot) AddCommand(cmd *discordgo.ApplicationCommand, guildIds ...string) {
	if len(guildIds) == 0 {
		guildIds = []string{""}
	}

	var commandFound bool
	for _, guildId := range guildIds {
		commandFound = false
		registeredCommands, _ := b.discordSession.ApplicationCommands(b.Id(), guildId)
		for _, registeredCommand := range registeredCommands {
			if utils.CompareApplicationCommand(*cmd, *registeredCommand) {
				b.Logger.Debug("skipping command already registered", slog.String("command_name", cmd.Name))
				commandFound = true
				break
			}
		}

		if !commandFound {
			_, err := b.discordSession.ApplicationCommandCreate(b.Id(), guildId, cmd)
			if err != nil {
				b.Logger.Error("failed to create application command",
					slog.String("error", err.Error()),
					slog.Any("command", cmd),
				)
			}
		}
	}
}

func (b *Bot) RemoveCommand(cmdId string, guildIds ...string) {
	if len(guildIds) == 0 {
		if err := b.discordSession.ApplicationCommandDelete(b.Id(), "", cmdId); err != nil {
			b.Logger.Error("failed to remove application command", slog.String("error", err.Error()))
		}
	} else {
		for _, guildId := range guildIds {
			if err := b.discordSession.ApplicationCommandDelete(b.Id(), guildId, cmdId); err != nil {
				b.Logger.Error("failed to remove application command", slog.String("error", err.Error()))
			}
		}
	}
}

func (b *Bot) AddIntent(intent discordgo.Intent) {
	b.discordSession.Identify.Intents |= intent
}

func (b *Bot) AddPlugin(plugin Plugin, guildIds ...string) error {
	if _, ok := b.plugins[plugin.Name()]; ok {
		return fmt.Errorf("plugin already exists")
	}

	b.plugins[plugin.Name()] = plugin

	handlers := plugin.Handlers()
	for name, handler := range handlers {
		b.AddHandler(name, handler)
	}

	commands := plugin.Commands()
	for _, command := range commands {
		b.AddCommand(command, guildIds...)
	}

	for _, intent := range plugin.Intents() {
		b.AddIntent(intent)
	}

	return nil
}

func (b *Bot) RemovePlugin(plugin Plugin, guildIds ...string) {
	for name, _ := range plugin.Handlers() {
		b.RemoveHandler(name)
	}

	for _, command := range plugin.Commands() {
		b.RemoveCommand(command.ID, guildIds...)
	}

	delete(b.plugins, plugin.Name())
}

func (b *Bot) ReloadPlugin(name string) {
	if plugin, ok := b.plugins[name]; ok {
		handlers := plugin.Handlers()
		for handlerName, handler := range handlers {
			b.AddHandler(handlerName, handler)
		}

		commands := plugin.Commands()
		for _, command := range commands {
			b.AddCommand(command)
		}

		for _, intent := range plugin.Intents() {
			b.AddIntent(intent)
		}
	}
}

func (b *Bot) Id() string {
	if b.state == StartedState {
		return b.discordSession.State.Application.ID
	}

	return ""
}

func (b *Bot) Start() error {
	if b.state == StartedState {
		return nil
	}

	if err := b.discordSession.Open(); err != nil {
		b.state = UnknownState
		return err
	}

	b.state = StartedState

	return nil
}

func (b *Bot) Restart() error {
	if err := b.Stop(); err != nil {
		return err
	}

	return b.Start()
}

func (b *Bot) Stop() error {
	if b.state == StoppedState {
		return nil
	}

	if err := b.discordSession.Close(); err != nil {
		b.state = UnknownState
		return err
	}

	b.state = StoppedState

	return nil
}

func (b *Bot) botData() any {
	type data struct {
		Name string
		Id   string
	}

	var d data

	if b.state == StartedState {
		d.Name = b.discordSession.State.User.Username
		d.Id = b.discordSession.State.Application.ID
	}

	return d
}
