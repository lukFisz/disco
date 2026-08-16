package discord

import (
	"testing"

	"disco/config"

	"github.com/bwmarrin/discordgo"
)

func TestExtractParams(t *testing.T) {
	cmdDef := config.Command{
		Params: map[string]config.Param{
			"name":    {Type: config.ParamString},
			"count":   {Type: config.ParamInteger},
			"enabled": {Type: config.ParamBoolean},
			"amount":  {Type: config.ParamNumber},
			"target":  {Type: config.ParamUser},
			"where":   {Type: config.ParamChannel},
			"group":   {Type: config.ParamRole},
		},
	}

	options := []*discordgo.ApplicationCommandInteractionDataOption{
		{Name: "name", Type: discordgo.ApplicationCommandOptionString, Value: "hello"},
		{Name: "count", Type: discordgo.ApplicationCommandOptionInteger, Value: float64(42)},
		{Name: "enabled", Type: discordgo.ApplicationCommandOptionBoolean, Value: true},
		{Name: "amount", Type: discordgo.ApplicationCommandOptionNumber, Value: 3.5},
		{Name: "target", Type: discordgo.ApplicationCommandOptionUser, Value: "111111111111111111"},
		{Name: "where", Type: discordgo.ApplicationCommandOptionChannel, Value: "222222222222222222"},
		{Name: "group", Type: discordgo.ApplicationCommandOptionRole, Value: "333333333333333333"},
	}

	got := extractParams(cmdDef, options)

	want := map[string]string{
		"name":    "hello",
		"count":   "42",
		"enabled": "true",
		"amount":  "3.5",
		"target":  "111111111111111111",
		"where":   "222222222222222222",
		"group":   "333333333333333333",
	}

	if len(got) != len(want) {
		t.Fatalf("extractParams() = %v, want %v", got, want)
	}
	for k, v := range want {
		if got[k] != v {
			t.Errorf("extractParams()[%q] = %q, want %q", k, got[k], v)
		}
	}
}

func TestBuiltinExecParams(t *testing.T) {
	params := map[string]string{"service": "web"}

	got := builtinExecParams(params, "deploy", "111", "alice", "222", "333")

	want := map[string]string{
		"service":       "web",
		"command.name":  "deploy",
		"user.id":       "111",
		"user.username": "alice",
		"channel.id":    "222",
		"guild.id":      "333",
	}
	if len(got) != len(want) {
		t.Fatalf("builtinExecParams() = %v, want %v", got, want)
	}
	for k, v := range want {
		if got[k] != v {
			t.Errorf("builtinExecParams()[%q] = %q, want %q", k, got[k], v)
		}
	}
}

func TestBuiltinExecParams_DoesNotMutateInput(t *testing.T) {
	params := map[string]string{"service": "web"}

	_ = builtinExecParams(params, "deploy", "111", "alice", "222", "333")

	if len(params) != 1 {
		t.Errorf("input params map was mutated: %v", params)
	}
}

func TestExtractParams_NumberWholeValueHasNoTrailingZero(t *testing.T) {
	cmdDef := config.Command{
		Params: map[string]config.Param{"amount": {Type: config.ParamNumber}},
	}
	options := []*discordgo.ApplicationCommandInteractionDataOption{
		{Name: "amount", Type: discordgo.ApplicationCommandOptionNumber, Value: float64(7)},
	}

	got := extractParams(cmdDef, options)
	if got["amount"] != "7" {
		t.Errorf("extractParams()[%q] = %q, want %q", "amount", got["amount"], "7")
	}
}
