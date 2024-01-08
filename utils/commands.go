package utils

import (
	"strings"

	"github.com/bwmarrin/discordgo"
)

// CompareApplicationCommand compares two discordgo.ApplicationCommand(s) and returns first == second.
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

// CompareApplicationCommandOption recursively traverses two discordgo.ApplicationCommandOption(s) to test for value
// equivalence. This can be called directly or through CompareApplicationCommand to compare higher level commands.
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
	if first.Type != second.Type {
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
		if (*first.Choices[index]).Name != (*second.Choices[index]).Name {
			return false
		}
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

// GetCommandOption takes either a discordgo.ApplicationCommandInteractionData or a
// discordgo.ApplicationCommandInteractionDataOption, and checks that:
//  1. the `.Name` property matches `name`
//  2. there exists and option in `.Options` with the name optionName
//
// If those conditions are met then the *discordgo.ApplicationCommandInteractionDataOption that matches is returned.
// If the conditions aren't met, then nil is returned.
func GetCommandOption(command any, name string, optionName string) *discordgo.ApplicationCommandInteractionDataOption {
	// TODO: when generics support shared properties, this can be fixed to avoid type assertions.
	switch cmd := command.(type) {
	case discordgo.ApplicationCommandInteractionData:
		if cmd.Name != name {
			return nil
		}

		for _, option := range cmd.Options {
			if option.Name == optionName {
				return option
			}
		}
	case discordgo.ApplicationCommandInteractionDataOption:
		// TODO too dumb to figure out a clean way of de-duping this
		if cmd.Name != name {
			return nil
		}

		for _, option := range cmd.Options {
			if option.Name == optionName {
				return option
			}
		}
	}

	return nil
}

// IsInteractionMessageComponent checks an interaction to see if it's of type discordgo.InteractionMessageComponent. It
// also compares the CustomID of it to name using the compareType supplied. compareType can be any of the following:
// "is", or "startsWith".
func IsInteractionMessageComponent(i *discordgo.InteractionCreate, compareType string, name string) bool {
	switch strings.ToLower(compareType) {
	case "startswith":
		return i.Interaction.Type == discordgo.InteractionMessageComponent && strings.HasPrefix(i.MessageComponentData().CustomID, name)
	case "is":
		return i.Interaction.Type == discordgo.InteractionMessageComponent && i.MessageComponentData().CustomID == name
	default:
		return false
	}
}
