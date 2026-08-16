// Package discord wires up a Discord bot session, registers slash commands
// from the disco config, and handles interactions by calling the executor
// and reporting results back to Discord.
package discord

import (
	"fmt"
	"log/slog"
	"sync/atomic"

	"disco/config"
	"disco/executor"

	"github.com/bwmarrin/discordgo"
)

// Bot ties together a Discord session with the disco config, executor and
// audit logger needed to service slash-command interactions.
type Bot struct {
	Session  *discordgo.Session
	GuildID  string
	config   atomic.Pointer[config.Config]
	Executor *executor.Executor
	Audit    *slog.Logger
	Logger   *slog.Logger
}

// New creates a Bot with a discordgo session (does not open the connection
// yet — call Open() for that).
func New(token, guildID string, cfg *config.Config, exec *executor.Executor, auditLogger *slog.Logger, appLogger *slog.Logger) (*Bot, error) {
	s, err := discordgo.New("Bot " + token)
	if err != nil {
		return nil, fmt.Errorf("creating discord session: %w", err)
	}
	s.Identify.Intents = discordgo.IntentsGuilds

	bot := &Bot{
		Session:  s,
		GuildID:  guildID,
		Executor: exec,
		Audit:    auditLogger,
		Logger:   appLogger,
	}
	bot.config.Store(cfg)

	s.AddHandler(bot.InteractionHandler)

	return bot, nil
}

// CurrentConfig returns the most recently applied Config. Safe for
// concurrent use.
func (b *Bot) CurrentConfig() *config.Config {
	return b.config.Load()
}

// SetConfig atomically replaces the bot's active config and re-registers
// Discord slash commands to match it. Intended to be called (e.g. as a
// config.Watcher's onReload callback) whenever the config file changes on
// disk. Re-registration failures are logged but do not revert the stored
// config — the new config still takes effect for future command dispatch
// even if Discord's command list is temporarily stale.
func (b *Bot) SetConfig(cfg *config.Config) {
	b.config.Store(cfg)
	if err := b.RegisterCommands(); err != nil {
		b.Logger.Error("failed to re-register commands after config reload", "error", err)
		return
	}
	b.Logger.Info("config reloaded, commands re-registered", "command_count", len(cfg.Commands))
}

// Open connects to Discord's gateway, registers the InteractionHandler,
// and — once the session is ready — registers slash commands from the
// current config via RegisterCommands.
func (b *Bot) Open() error {
	if err := b.Session.Open(); err != nil {
		return fmt.Errorf("opening discord session: %w", err)
	}
	b.Logger.Info("discord session opened")
	if err := b.RegisterCommands(); err != nil {
		return fmt.Errorf("registering commands: %w", err)
	}
	b.Logger.Info("commands registered", "count", len(b.CurrentConfig().Commands))
	return nil
}

// RegisterCommands bulk-overwrites this bot's guild-scoped slash commands
// to exactly match the current config's Commands (so removing a command
// from YAML and restarting the bot, or reloading config with the command
// removed, automatically un-registers it from Discord — no separate
// delete-on-shutdown logic needed).
func (b *Bot) RegisterCommands() error {
	cfg := b.CurrentConfig()
	cmds := make([]*discordgo.ApplicationCommand, 0, len(cfg.Commands))
	for _, cmd := range cfg.Commands {
		cmds = append(cmds, buildApplicationCommand(cmd))
	}

	_, err := b.Session.ApplicationCommandBulkOverwrite(b.Session.State.User.ID, b.GuildID, cmds)
	if err != nil {
		return fmt.Errorf("bulk overwriting application commands: %w", err)
	}
	return nil
}

// Close unregisters all of this bot's guild-scoped slash commands and then
// gracefully closes the Discord session. Unregistration failures are logged
// but do not prevent the session from closing — a stale command list is
// harmless since RegisterCommands will overwrite it on the next start.
func (b *Bot) Close() error {
	b.Logger.Info("closing discord session")
	if _, err := b.Session.ApplicationCommandBulkOverwrite(b.Session.State.User.ID, b.GuildID, []*discordgo.ApplicationCommand{}); err != nil {
		b.Logger.Warn("failed to unregister commands", "error", err)
	}
	return b.Session.Close()
}

// descriptionOrPlaceholder returns d, or a non-empty fallback placeholder if
// d is empty — Discord requires a non-empty description for both commands
// and options.
func descriptionOrPlaceholder(d string) string {
	if d == "" {
		return "(no description)"
	}
	return d
}

// buildApplicationCommand converts a config.Command into a
// discordgo.ApplicationCommand ready for registration.
func buildApplicationCommand(cmd config.Command) *discordgo.ApplicationCommand {
	var required, optional []*discordgo.ApplicationCommandOption

	for pname, p := range cmd.Params {
		dtype, err := paramTypeToDiscordType(p.Type)
		if err != nil {
			// Defensive: config.Validate() already constrains param types,
			// so this should be unreachable. Skip rather than panic.
			continue
		}
		opt := &discordgo.ApplicationCommandOption{
			Type:        dtype,
			Name:        pname,
			Description: descriptionOrPlaceholder(p.Description),
			Required:    p.Required,
		}
		if p.Required {
			required = append(required, opt)
		} else {
			optional = append(optional, opt)
		}
	}

	options := append(required, optional...)

	return &discordgo.ApplicationCommand{
		Name:        cmd.Name,
		Description: descriptionOrPlaceholder(cmd.Description),
		Options:     options,
	}
}

// paramTypeToDiscordType maps a config.ParamType to the corresponding
// discordgo.ApplicationCommandOptionType.
func paramTypeToDiscordType(t config.ParamType) (discordgo.ApplicationCommandOptionType, error) {
	switch t {
	case config.ParamString:
		return discordgo.ApplicationCommandOptionString, nil
	case config.ParamInteger:
		return discordgo.ApplicationCommandOptionInteger, nil
	case config.ParamBoolean:
		return discordgo.ApplicationCommandOptionBoolean, nil
	case config.ParamNumber:
		return discordgo.ApplicationCommandOptionNumber, nil
	case config.ParamUser:
		return discordgo.ApplicationCommandOptionUser, nil
	case config.ParamChannel:
		return discordgo.ApplicationCommandOptionChannel, nil
	case config.ParamRole:
		return discordgo.ApplicationCommandOptionRole, nil
	default:
		return 0, fmt.Errorf("unsupported param type %q", t)
	}
}
