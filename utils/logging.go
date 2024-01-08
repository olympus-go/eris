package utils

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/bwmarrin/discordgo"
)

// This file provides helper functions useful for logging.

type NopLogHandler struct{}

func (n NopLogHandler) Enabled(_ context.Context, _ slog.Level) bool  { return false }
func (n NopLogHandler) Handle(_ context.Context, _ slog.Record) error { return nil }
func (n NopLogHandler) WithAttrs(_ []slog.Attr) slog.Handler          { return n }
func (n NopLogHandler) WithGroup(_ string) slog.Handler               { return n }

// CommandDataString recursively traverses a discordgo.ApplicationCommandInteractionData and returns a formatted string
// representing the command.
func CommandDataString(command discordgo.ApplicationCommandInteractionData) string {

	str := command.Name

	// Pre-declare this so we can recursively call it in the function definition
	var buildOpts func(str string, opts []*discordgo.ApplicationCommandInteractionDataOption) string

	buildOpts = func(str string, opts []*discordgo.ApplicationCommandInteractionDataOption) string {
		for _, option := range opts {
			if option.Type == discordgo.ApplicationCommandOptionSubCommand {
				str += fmt.Sprintf(" %s", option.Name)
			} else {
				str += fmt.Sprintf(" [%s", option.Name)
				if option.Value != nil {
					str += fmt.Sprintf(": %v]", option.Value)
				} else {
					str += "]"
				}
			}

			buildOpts(str, option.Options)
		}

		return str
	}

	for _, option := range command.Options {
		if option.Type == discordgo.ApplicationCommandOptionSubCommand {
			str += fmt.Sprintf(" %s", option.Name)
		} else {
			str += fmt.Sprintf(" [%s", option.Name)
			if option.Value != nil {
				str += fmt.Sprintf(": %v]", option.Value)
			} else {
				str += "]"
			}
		}
		str = buildOpts(str, option.Options)
	}

	return str
}

// CommandDataInterface returns a trimmed version of discordgo.ApplicationCommandInteractionData.
func CommandDataInterface(command discordgo.ApplicationCommandInteractionData) any {
	data := struct {
		Name    string
		Options []struct {
			Name  string
			Value any
		}
	}{
		Name: command.Name,
	}

	for _, option := range command.Options {
		data.Options = append(data.Options, struct {
			Name  string
			Value any
		}{Name: option.Name, Value: option.Value})
	}

	return data
}

// MessageComponentInterface returns a trimmed version of discordgo.MessageComponentInteractionData.
func MessageComponentInterface(message discordgo.MessageComponentInteractionData) any {
	data := struct {
		Id     string
		Values []string
	}{
		Id:     message.CustomID,
		Values: message.Values,
	}

	return data
}
