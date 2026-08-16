// Command disco runs a Discord bot that registers slash commands from a
// declarative YAML config and executes corresponding shell commands on the
// host when invoked. See README.md for setup and usage.
package main

import (
	"flag"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"disco/audit"
	"disco/config"
	"disco/discord"
	"disco/executor"
	"disco/logging"
)

// version is set at build time via -ldflags "-X main.version=...", e.g. by
// GoReleaser. It stays "dev" for local `go build`/`go run`.
var version = "dev"

func main() {
	configPath := flag.String("config", "config.yaml", "path to the YAML config file")
	showVersion := flag.Bool("version", false, "print version and exit")
	flag.Parse()

	if *showVersion {
		fmt.Println(version)
		return
	}

	logger, levelVar, _ := logging.New("info")

	token := os.Getenv("DISCORD_TOKEN")
	guildID := os.Getenv("DISCORD_GUILD_ID")
	if token == "" || guildID == "" {
		logger.Error("DISCORD_TOKEN and DISCORD_GUILD_ID environment variables are required")
		os.Exit(1)
	}

	cfg, err := config.Load(*configPath)
	if err != nil {
		logger.Error("failed to load config", "path", *configPath, "error", err)
		os.Exit(1)
	}

	if lvl, err := logging.ParseLevel(cfg.LogLevel); err == nil {
		levelVar.Set(lvl)
	} else {
		logger.Warn("invalid log_level in config, keeping default", "log_level", cfg.LogLevel)
	}
	logger.Info("config loaded", "path", *configPath, "commands", len(cfg.Commands), "log_level", cfg.LogLevel)

	auditLogger, err := audit.New(cfg.Audit, true)
	if err != nil {
		logger.Error("failed to initialize audit logger", "error", err)
		os.Exit(1)
	}

	exec := executor.New()
	exec.SetLogger(logger)

	bot, err := discord.New(token, guildID, cfg, exec, auditLogger, logger)
	if err != nil {
		logger.Error("failed to create bot", "error", err)
		os.Exit(1)
	}

	if err := bot.Open(); err != nil {
		logger.Error("failed to start bot", "error", err)
		os.Exit(1)
	}

	logger.Info("disco bot started", "commands_registered", len(cfg.Commands))

	watcher := config.NewWatcher(*configPath, cfg, logger)
	watcher.Start(func(newCfg *config.Config) {
		if lvl, err := logging.ParseLevel(newCfg.LogLevel); err == nil {
			levelVar.Set(lvl)
		} else {
			logger.Warn("invalid log_level in reloaded config, keeping previous level", "log_level", newCfg.LogLevel)
		}
		bot.SetConfig(newCfg)
	})

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	<-sigCh

	logger.Info("shutdown signal received")
	watcher.Stop()
	if err := bot.Close(); err != nil {
		logger.Error("error during shutdown", "error", err)
		os.Exit(1)
	}
}
