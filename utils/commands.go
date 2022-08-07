package utils

import "github.com/bwmarrin/discordgo"

func CompareApplicationCommand(first, second discordgo.ApplicationCommand) bool {
	if first.Name != second.Name {
		return false
	}
	if first.Description != second.Description {
		return false
	}
	if len(first.Options) != len(second.Options) {
		return false
	}

	for index, _ := range first.Options {
		if !CompareApplicationCommandOption(*first.Options[index], *second.Options[index]) {
			return false
		}
	}

	return true
}

func CompareApplicationCommandOption(first, second discordgo.ApplicationCommandOption) bool {
	if first.Name != second.Name {
		return false
	}
	if first.Description != second.Description {
		return false
	}
	if first.Required != second.Required {
		return false
	}
	if len(first.Options) != len(second.Options) {
		return false
	}

	for index, _ := range first.Options {
		if !CompareApplicationCommandOption(*first.Options[index], *second.Options[index]) {
			return false
		}
	}

	if first.Autocomplete != second.Autocomplete {
		return false
	}
	if len(first.Choices) != len(second.Choices) {
		return false
	}

	for index, _ := range first.Choices {
		CompareApplicationCommandOptionChoice(*first.Choices[index], *second.Choices[index])
	}

	if first.MinValue != nil && second.MinValue != nil {
		if *first.MinValue != *second.MinValue {
			return false
		}
	}

	if first.MaxValue != second.MaxValue {
		return false
	}

	return true
}

func CompareApplicationCommandOptionChoice(first, second discordgo.ApplicationCommandOptionChoice) bool {
	return first.Name == second.Name
}

// CheckApplicationCommandData checks a discordgo.ApplicationCommandInteractionData for the matching name and subsequent option names.
func CheckApplicationCommandData(command discordgo.ApplicationCommandInteractionData, name string, options ...string) bool {
	if command.Name != name || len(command.Options) != len(options) {
		return false
	}

	for index, option := range options {
		if command.Options[index].Name != option {
			return false
		}
	}

	return true
}

func GetApplicationCommandOption(command discordgo.ApplicationCommandInteractionData, name string, optionName string) (*discordgo.ApplicationCommandInteractionDataOption, bool) {
	if command.Name != name {
		return nil, false
	}

	for _, option := range command.Options {
		if option.Name == optionName {
			return option, true
		}
	}

	return nil, false
}
