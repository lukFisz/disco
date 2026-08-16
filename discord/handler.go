package discord

import (
	"bytes"
	"context"
	"fmt"
	"log/slog"
	"strconv"

	"disco/config"
	"disco/executor"

	"github.com/bwmarrin/discordgo"
)

// InteractionHandler is registered with discordgo as the handler for all
// interaction events. It validates the incoming slash command against the
// current config, defers a response, and then runs the command asynchronously
// via b.Executor, reporting the result back to Discord.
func (b *Bot) InteractionHandler(s *discordgo.Session, i *discordgo.InteractionCreate) {
	if i.Type != discordgo.InteractionApplicationCommand {
		return
	}

	data := i.ApplicationCommandData()
	b.Logger.Info("command received", "command", data.Name)

	cfg := b.CurrentConfig()

	cmdDef, ok := cfg.Commands[data.Name]
	if !ok {
		b.respondEphemeral(s, i.Interaction, fmt.Sprintf("Unknown command: %s", data.Name))
		b.Audit.Info("command_invocation",
			slog.String("event", "rejected"),
			slog.String("reason", "unknown_command"),
			slog.String("command", data.Name),
		)
		b.Logger.Warn("command rejected", "command", data.Name, "reason", "unknown_command")
		return
	}

	params := extractParams(cmdDef, data.Options)

	if errMsg := validateParams(cmdDef, params); errMsg != "" {
		b.respondEphemeral(s, i.Interaction, errMsg)
		b.Audit.Info("command_invocation",
			slog.String("event", "rejected"),
			slog.String("reason", "invalid_param"),
			slog.String("command", data.Name),
			slog.Any("params", params),
		)
		b.Logger.Warn("command rejected", "command", data.Name, "reason", "invalid_param")
		return
	}

	if err := s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
		Type: discordgo.InteractionResponseDeferredChannelMessageWithSource,
	}); err != nil {
		b.Logger.Error("failed to defer interaction response", "command", data.Name, "error", err)
		return
	}

	go b.runAndReport(s, i, cmdDef, data.Name, params)
}

// runAndReport runs cmdDef via the executor and edits the deferred
// interaction response with the result. It's meant to be launched as a
// goroutine so the InteractionHandler itself returns quickly.
func (b *Bot) runAndReport(s *discordgo.Session, i *discordgo.InteractionCreate, cmdDef config.Command, cmdName string, params map[string]string) {
	var userID, username string
	if i.Member != nil && i.Member.User != nil {
		userID = i.Member.User.ID
		username = i.Member.User.Username
	}

	var onProgress executor.StreamFunc
	if cmdDef.Output == config.OutputStream {
		onProgress = func(stdout, stderr string) {
			progressText := executor.FormatProgress(stdout, stderr)
			_, err := s.InteractionResponseEdit(i.Interaction, &discordgo.WebhookEdit{Content: &progressText})
			if err != nil {
				b.Logger.Warn("progress edit failed", "command", cmdName, "error", err)
			}
		}
	}

	execParams := builtinExecParams(params, cmdName, userID, username, i.ChannelID, i.GuildID)

	ctx := context.Background()
	result, err := b.Executor.Run(ctx, cmdDef, execParams, onProgress)

	logFields := func(event, reason string, exitCode int, durationMS int64, timedOut bool) {
		b.Audit.Info("command_invocation",
			slog.String("event", event),
			slog.String("reason", reason),
			slog.String("user_id", userID),
			slog.String("username", username),
			slog.String("command", cmdName),
			slog.Any("params", params),
			slog.Int("exit_code", exitCode),
			slog.Int64("duration_ms", durationMS),
			slog.Bool("timed_out", timedOut),
		)
	}

	switch {
	case err == executor.ErrAlreadyRunning:
		msg := fmt.Sprintf("Command %s is already running, try again once it finishes.", cmdName)
		b.editOrFallback(s, i, msg)
		logFields("rejected", "already_running", 0, 0, false)
		return
	case result.Err != nil:
		msg := fmt.Sprintf("Failed to start command: %v", result.Err)
		b.editOrFallback(s, i, msg)
		logFields("error", "start_failed", 0, 0, false)
		return
	}

	formatted := executor.Format(cmdName, cmdDef.Output, result)

	edit := &discordgo.WebhookEdit{Content: &formatted.Content}
	if formatted.Attachment != nil {
		edit.Files = []*discordgo.File{{
			Name:   formatted.Attachment.Filename,
			Reader: bytes.NewReader(formatted.Attachment.Data),
		}}
	}

	if _, err := s.InteractionResponseEdit(i.Interaction, edit); err != nil {
		b.Logger.Warn("final edit failed", "command", cmdName, "error", err)
		if _, sendErr := s.ChannelMessageSend(i.ChannelID, formatted.Content); sendErr != nil {
			b.Logger.Error("fallback channel message send failed", "command", cmdName, "error", sendErr)
		}
	}

	logFields("completed", "", result.ExitCode, result.Duration.Milliseconds(), result.TimedOut)
	b.Logger.Info("command finished", "command", cmdName, "exit_code", result.ExitCode, "duration_ms", result.Duration.Milliseconds(), "timed_out", result.TimedOut)
}

// builtinExecParams returns a copy of params with the reserved built-in
// placeholders (config.Placeholder* - {command.name}, {user.id},
// {user.username}, {channel.id}, {guild.id}) merged in, ready to pass to
// executor.Run. These are available in every command's exec without being
// declared under params: - see config.Validate's exemption for them. The
// original params map (used for pattern validation and audit logging) is
// left untouched.
func builtinExecParams(params map[string]string, cmdName, userID, username, channelID, guildID string) map[string]string {
	merged := make(map[string]string, len(params)+5)
	for k, v := range params {
		merged[k] = v
	}
	merged[config.PlaceholderCommandName] = cmdName
	merged[config.PlaceholderUserID] = userID
	merged[config.PlaceholderUsername] = username
	merged[config.PlaceholderChannelID] = channelID
	merged[config.PlaceholderGuildID] = guildID
	return merged
}

// editOrFallback edits the deferred interaction response with msg, falling
// back to a plain channel message if the edit fails (e.g. an expired
// interaction token).
func (b *Bot) editOrFallback(s *discordgo.Session, i *discordgo.InteractionCreate, msg string) {
	if _, err := s.InteractionResponseEdit(i.Interaction, &discordgo.WebhookEdit{Content: &msg}); err != nil {
		b.Logger.Warn("edit failed, falling back to channel message", "error", err)
		if _, sendErr := s.ChannelMessageSend(i.ChannelID, msg); sendErr != nil {
			b.Logger.Error("fallback channel message send failed", "error", sendErr)
		}
	}
}

// respondEphemeral sends an immediate (non-deferred), ephemeral error
// response to an interaction.
func (b *Bot) respondEphemeral(s *discordgo.Session, i *discordgo.Interaction, msg string) {
	err := s.InteractionRespond(i, &discordgo.InteractionResponse{
		Type: discordgo.InteractionResponseChannelMessageWithSource,
		Data: &discordgo.InteractionResponseData{
			Content: msg,
			Flags:   discordgo.MessageFlagsEphemeral,
		},
	})
	if err != nil {
		b.Logger.Error("failed to send ephemeral response", "error", err)
	}
}

// extractParams converts the interaction's option values into the
// map[string]string form expected by executor.Run, using each param's
// declared type (from cmdDef.Params) to pick the right value accessor.
func extractParams(cmdDef config.Command, options []*discordgo.ApplicationCommandInteractionDataOption) map[string]string {
	params := make(map[string]string, len(options))
	for _, opt := range options {
		p, ok := cmdDef.Params[opt.Name]
		if !ok {
			continue
		}
		switch p.Type {
		case config.ParamInteger:
			params[opt.Name] = strconv.FormatInt(opt.IntValue(), 10)
		case config.ParamBoolean:
			params[opt.Name] = strconv.FormatBool(opt.BoolValue())
		case config.ParamNumber:
			params[opt.Name] = strconv.FormatFloat(opt.FloatValue(), 'f', -1, 64)
		case config.ParamUser, config.ParamChannel, config.ParamRole:
			// Discord sends these as the picked entity's snowflake ID (a
			// string), which is what we substitute into argv - resolving
			// the full User/Channel/Role object would need a *Session
			// threaded through just to read its .ID back off.
			if id, ok := opt.Value.(string); ok {
				params[opt.Name] = id
			}
		default:
			params[opt.Name] = opt.StringValue()
		}
	}
	return params
}

// validateParams checks extracted params against cmdDef.Params (required
// presence and regex pattern for string params). It returns a
// human-readable error message describing the first failure found, or "" if
// validation passed.
func validateParams(cmdDef config.Command, params map[string]string) string {
	for pname, p := range cmdDef.Params {
		val, present := params[pname]
		if p.Required && !present {
			return fmt.Sprintf("Missing required parameter: %s", pname)
		}
		if present && p.Type == config.ParamString {
			if re := p.Compiled(); re != nil && !re.MatchString(val) {
				return fmt.Sprintf("Parameter %s does not match the required pattern", pname)
			}
		}
	}
	return ""
}
