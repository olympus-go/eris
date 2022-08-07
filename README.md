# eris
eris is a go library that wraps [discordgo](https://github.com/bwmarrin/discordgo) with the goal of creating an extensible
bot framework as well as enabling faster bot development.

## Plugins
eris defines a `Plugin` interface that can be used to add a whole slew of features to your bot while keeping things
organized and readable. eris by default will also add a `/plugins` command to your bot that lets users see what cool 
features you have baked into your bot without requiring extra steps from you. A few working examples can be found in the
`plugins/` directory.

### Using the Plugin Interface
The plugin interface is defined as follows:
```go
type Plugin interface {
	Name() string
	Description() string
	Handlers() map[string]any
	Commands() map[string]*discordgo.ApplicationCommand
	Intents() []discordgo.Intent
}
```
#### Name
A thoughtful name should be returned by your plugin that gives a general idea of what it does.
#### Description
A slightly longer description of what your plugin does.
#### Handlers
A map of handler ids and handler functions. The handler function should be one of the many options provided by
[discordgo](https://github.com/bwmarrin/discordgo) [here](https://github.com/bwmarrin/discordgo/blob/master/eventhandlers.go).
Handler ids are global to an eris bot, so it is recommended that they contain unique identifiers relevant to the plugin
otherwise they might be overwritten. Return nil if not applicable.
#### Commands
A map of command ids and [discordgo](https://github.com/bwmarrin/discordgo) application commands. This is only necessary
if your plugin configures any application commands. Like handlers, the id is global to an eris bot so care should be taken
that there are no possible collisions with other plugin's commands. Return nil if not applicable.
#### Intents
A list of intents that are required by your plugin to function. This helps ensure that any plugins added to an eris bot
will work out of the box without the need to configure additional intents manually.

## Utils
Some additional utils are also packaged in the `utils/` directory. These are aimed to be useful wrappers around
[discordgo](https://github.com/bwmarrin/discordgo) functions to make some calls less involved or more readable.

## Examples
An example bot built using eris can be found in the `_example/` directory. This exact one probably won't live here forever.