# <img src="disco-icon.png" width="120" alt="disco logo"> disco

disco is a Discord bot that reads a declarative YAML config defining
commands, registers them as native Discord slash commands on a single
guild, and — when invoked — runs a corresponding shell command on the
host. Parameters are substituted safely into argv (never through a shell,
so there's no injection risk). Output is reported back to Discord per a
configurable mode (`full`, `stream`, or `silent`), with per-command
timeout and concurrency control, and every invocation is recorded as a
structured JSON audit log with automatic rotation.

## Quick start (recommended: Docker Compose)

Copy `config.yaml.example` to `config.yaml` and edit as needed, then
create a `.env` file next to `docker-compose.yml` with:

| Variable | Required | Description |
| --- | --- | --- |
| `DISCORD_TOKEN` | Yes | Bot token from the Discord Developer Portal. |
| `DISCORD_GUILD_ID` | Yes | The guild (server) ID to register commands on. |
| `HA_URL` | No | Home Assistant base URL, only for the `automation` example. |
| `HA_TOKEN` | No | Home Assistant Long-Lived Access Token, same as above. |

`docker-compose.yml`:

```yaml
services:
  disco:
    image: ghcr.io/lukfisz/disco:latest
    restart: unless-stopped
    env_file:
      - .env
    volumes:
      - ./config.yaml:/app/config.yaml:ro
      - audit-data:/data

volumes:
  audit-data:
```

Then run `docker compose up -d`.

It pulls the published image, bind-mounts your `config.yaml` read-only,
and mounts the named `audit-data` volume at `/data` — `config.yaml.example`
already sets `audit.file: /data/audit.log` by default, so the audit log is
persisted across container restarts/recreates out of the box. **Commands
under `exec:` run inside the container**, not on your physical host — if a command
needs host scripts/binaries/a Docker socket, mount them in via
`volumes:`, or run disco outside Docker instead. To build the image from
local source instead of pulling it, replace `image:` with `build: .`.

### Other ways to run

- **Prebuilt binary:** download the archive for your OS/arch from
  [Releases](https://github.com/lukfisz/disco/releases), extract it, then
  `DISCORD_TOKEN=... DISCORD_GUILD_ID=... ./disco -config config.yaml`.
- **From source** (needs Go 1.22+):
  `go run . -config config.yaml`. For live reload during development,
  install [`air`](https://github.com/air-verse/air) and run `air` (uses
  the repo's `.air.toml`). Note: `config.yaml.example`'s `audit.file`
  defaults to `/data/audit.log` to match the Docker setup above — running
  outside Docker, change it to a local path such as `./audit.log`, since
  `/data` won't exist on your host.

## Discord setup

1. Create an Application + Bot at the
   [Discord Developer Portal](https://discord.com/developers/applications).
2. Copy the bot token → `DISCORD_TOKEN`.
3. OAuth2 → URL Generator → scopes `bot` + `applications.commands`,
   permissions **Send Messages** + **Attach Files**. Open the generated
   URL to invite the bot to your guild.
4. Enable Developer Mode (User Settings → Advanced), right-click your
   guild → "Copy Server ID" → `DISCORD_GUILD_ID`.

`DISCORD_TOKEN` and `DISCORD_GUILD_ID` are read from the environment only
— never put them in the YAML config.

On startup disco bulk-overwrites the guild's slash commands to exactly
match the config, so removing a command from YAML and restarting also
removes it from Discord. Stop with Ctrl-C/SIGTERM for graceful shutdown.

## Config

[`config.yaml.example`](./config.yaml.example) is the authoritative,
fully-worked reference (all output modes, every field). Key points:

- **Param types** (`params:` under a command): `string` (with optional
  `pattern:`), `integer`, `boolean`, `number`, and Discord-native pickers
  `user`/`channel`/`role` (substituted as the picked entity's numeric ID).
- **Built-in placeholders** available in every `exec` without declaring a
  param: `{command.name}`, `{user.id}`, `{user.username}`, `{channel.id}`,
  `{guild.id}`.
- **Hot-reload:** disco polls the config file roughly every 2.5s; a valid
  change is applied live (including Discord command re-registration and
  `log_level`), an invalid one is logged and ignored, previous config
  keeps serving.
- `audit:` and `log_level` are optional, with sensible defaults — see the
  example file.

`examples/*.sh` are toy scripts for local testing only (referenced by
`config.yaml.example`'s `deploy`/`status`/`restart` commands) and are
**not** included in the Docker image. `scripts/` holds built-in scripts
that *are* baked into the image at build time — currently
`ha_trigger_automation.sh`, used by the `automation` example command to
trigger a Home Assistant automation via its REST API (needs `HA_URL` and
`HA_TOKEN` in the environment, read by the script, not by disco). It also
forwards `{channel.id}` to HA as a `variables` entry so the automation
knows where to notify back — reference it as `{{ channel_id }}` in the
automation's actions.

**Docker networking:** if Home Assistant runs on the same host but disco
is in a container, `localhost` won't reach it. Use HA's LAN IP,
`host.docker.internal` (Docker Desktop), or a shared Docker network.

## Known limitations

- Timeout kill only reaches the direct child process — a wrapper script's
  own grandchildren may outlive it.
- Discord interaction tokens expire after ~15 minutes; keep timeouts
  comfortably under that. The same-channel fallback message (used if the
  final edit fails) never carries file attachments.
- No per-role/per-channel access control — anyone in the guild can invoke
  any configured command.

## Development

```sh
go build ./...
go test ./...
go vet ./...
```

Two independent logs: disco's own operational log (plain text to stdout,
`log_level`-controlled) and the structured JSON audit trail of every
command invocation (`audit:` config, rotated via lumberjack).

## Releases & container images

Tagged commits (`vX.Y.Z`) are built and published automatically by GitHub
Actions:

- Cross-platform binaries (Linux/macOS/Windows, amd64/arm64) attach to the
  [GitHub Release](https://github.com/lukfisz/disco/releases).
- A multi-arch image publishes to
  [`ghcr.io/lukfisz/disco`](https://github.com/lukfisz/disco/pkgs/container/disco)
  tagged with the version and `latest`.
