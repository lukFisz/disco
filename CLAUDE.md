# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## What this is

disco is a Discord bot that reads a declarative YAML config defining
commands, registers them as native Discord guild slash commands, and — when
invoked — runs a corresponding shell command on the host. Parameters are
substituted into argv via a plain string replacer *before* `exec.Cmd` is
built, never through a shell, so there's no injection risk. See README.md
for full user-facing docs (Discord app setup, config schema, Docker usage,
known limitations); this file is about working on disco's own code.

## Commands

```sh
go build ./...                       # build
go run . -config config.yaml         # run (requires DISCORD_TOKEN, DISCORD_GUILD_ID env vars)
go test ./...                        # run all tests
go test ./executor/...               # run one package's tests
go test ./config/ -run TestValidate  # run a single test
go vet ./...                         # static checks
air                                   # live reload during development (rebuilds on .go/.yaml/.yml changes)
```

There's no lint config beyond `go vet`; keep code `gofmt`-clean.

## Architecture

Five packages under `main.go`, each deliberately decoupled — dependencies
flow one way and lower layers know nothing about Discord:

```
config   → parses/validates YAML, no knowledge of Discord or execution
executor → runs shell commands, formats output; imports config, not discordgo
audit    → structured JSON audit logging via slog + lumberjack; imports config, not discordgo
logging  → disco's own operational slog logger (plain text to stdout)
discord  → the only package that imports discordgo; wires everything together
```

`main.go` wires these up: loads config, builds the audit logger and
executor, constructs the `discord.Bot`, opens the Discord session, starts
the config file watcher, and blocks on SIGINT/SIGTERM for graceful
shutdown.

### config package

- `Config.Validate()` (config/config.go) does two things in sequence: it
  first collects *all* validation errors via `errors.Join` (never stops at
  the first), and only if that list is empty does it run a second
  defaulting/merge pass that resolves each `Command`'s `Output`, `Timeout`,
  and `AllowConcurrent` against `Defaults`. Downstream code can assume every
  `Command` coming out of `Load()` has these fields fully resolved —
  `AllowConcurrent` in particular is guaranteed non-nil.
- `Duration` is a `time.Duration` wrapper with custom YAML unmarshaling via
  `time.ParseDuration` (accepts `"30s"`, `"2m"`, etc).
- Besides user-declared `params:`, `exec` placeholders also accept a
  reserved, dotted set — `{command.name}`, `{user.id}`, `{user.username}`,
  `{channel.id}`, `{guild.id}` (see `config.Placeholder*` consts and
  `builtinPlaceholders`) — exempted from the "undeclared param" check in
  `validateCommand`. They're populated per-invocation by
  `discord.builtinExecParams` (handler.go), not by `config`; `config` only
  knows their names. Dotted names can never collide with a declared param
  since `nameRegex` forbids dots in param names.
- `Watcher` (config/watcher.go) polls the config file's mtime every ~2.5s
  in a background goroutine, reloads+revalidates on change, and atomically
  swaps the served config via `atomic.Pointer`. A bad reload is logged and
  the previous valid config keeps serving — reload failure never crashes
  the bot. Tests use `newWatcherWithInterval` to shrink the poll interval.

### executor package

- `Executor.Run` enforces per-command-name non-concurrency: when
  `*cmd.AllowConcurrent` is false, it uses `sync.Mutex.TryLock` (never
  blocks) keyed by command name, returning `ErrAlreadyRunning` immediately
  if another invocation is in flight.
- Output is captured into a size-capped `syncBuffer` (1MB per stream);
  writes beyond the cap are silently dropped and `Result.Truncated` is set
  — never an error.
- `Result.Err` is non-nil *only* when the process failed to start (e.g. bad
  binary path); a normal non-zero exit is reflected in `Result.ExitCode`,
  not `Result.Err`.
- Stream-mode progress (`StreamFunc`) is polled/emitted roughly once per
  second by a background goroutine inside `Run`, only started when
  `onProgress != nil`.
- output.go formats `Result` into Discord-ready content, handling the
  2000-char message cap (see `maxDiscordContentLen`) by truncating and
  attaching full output as a file when needed. Silent mode reports only the
  exit code, never stdout/stderr.

### discord package

- `Bot` holds the live config behind an `atomic.Pointer` (`SetConfig`/
  `CurrentConfig`), so config reloads and in-flight command handling never
  race.
- `RegisterCommands` always *bulk-overwrites* the guild's slash commands to
  exactly match the current config — this is how removing a command from
  YAML also removes it from Discord without extra delete logic.
- handler.go's `InteractionHandler` validates the command/params
  synchronously, immediately sends a deferred response, then hands off to
  `runAndReport` as a goroutine (so the handler itself returns fast, well
  under Discord's interaction ack window). `runAndReport` calls the
  executor, formats the result, edits the deferred response, and writes an
  audit log record — with a same-channel fallback message if the edit
  fails (e.g. an expired ~15min interaction token; that fallback never
  carries file attachments).

### audit vs logging

Two independent slog loggers exist on purpose — don't conflate them:

- `logging` — disco's own operational log (startup, errors, reloads),
  plain text to stdout, level adjustable at runtime via `slog.LevelVar`
  (config's `log_level`, hot-reloadable).
- `audit` — structured JSON record of every command invocation
  (`command_invocation` events: `rejected`/`error`/`completed`, with
  user/command/params/exit_code/duration/timed_out), written to a
  lumberjack-rotated file (and also stdout). This is the compliance/audit
  trail, not a debugging log.

## Testing conventions

- Each package's tests live alongside it (`*_test.go`), no separate test
  directory.
- `main_test.go` at the repo root (`TestExampleConfigLoads`) guards against
  `config.yaml.example` drifting out of sync with the schema — update the
  example whenever the config schema changes.
- Constructors that need faster timing in tests are split into an exported
  wrapper plus an unexported one taking an explicit interval/writer (e.g.
  `NewWatcher`/`newWatcherWithInterval`, `logging.New`/`newWithWriter`) —
  follow this pattern for new time- or IO-dependent code rather than adding
  sleeps to tests.
