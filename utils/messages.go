package utils

import "github.com/bwmarrin/discordgo"

type ActionsRowBuilder struct {
	messageComponents []discordgo.MessageComponent
}

type MessageComponentBuilder struct {
	messageComponents []discordgo.MessageComponent
}

func ActionsRow() *ActionsRowBuilder {
	return &ActionsRowBuilder{}
}

func (a *ActionsRowBuilder) Button(style discordgo.ButtonStyle, message string, id string) *ActionsRowBuilder {
	button := discordgo.Button{
		Style:    style,
		Label:    message,
		CustomID: id,
	}
	a.messageComponents = append(a.messageComponents, button)
	return a
}

func (a *ActionsRowBuilder) Value() discordgo.ActionsRow {
	return discordgo.ActionsRow{
		Components: a.messageComponents,
	}
}

func MessageComponent() *MessageComponentBuilder {
	return &MessageComponentBuilder{}
}

func (m *MessageComponentBuilder) Button(style discordgo.ButtonStyle, message string, id string) *MessageComponentBuilder {
	button := discordgo.Button{
		Style:    style,
		Label:    message,
		CustomID: id,
	}

	m.messageComponents = append(m.messageComponents, button)
	return m
}

func (m *MessageComponentBuilder) Value() []discordgo.MessageComponent {
	return m.messageComponents
}

//		Components: []discordgo.MessageComponent{
//			discordgo.ActionsRow{
//				Components: []discordgo.MessageComponent{
//					discordgo.Button{
//						Label:    "Characters",
//						Style:    discordgo.PrimaryButton,
//						CustomID: "21q_theme_characters",
//					},
//					discordgo.Button{
//						Label:    "Animals",
//						Style:    discordgo.PrimaryButton,
//						CustomID: "21q_theme_animals",
//					},
//					discordgo.Button{
//						Label:    "Objects",
//						Style:    discordgo.PrimaryButton,
//						CustomID: "21q_theme_objects",
//					},
//				},
//			},
//		},
//	}
//
//	return &responseData
//}
