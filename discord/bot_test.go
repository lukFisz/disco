package discord

import (
	"testing"

	"disco/config"

	"github.com/bwmarrin/discordgo"
)

func TestParamTypeToDiscordType(t *testing.T) {
	tests := []struct {
		name    string
		in      config.ParamType
		want    discordgo.ApplicationCommandOptionType
		wantErr bool
	}{
		{"string", config.ParamString, discordgo.ApplicationCommandOptionString, false},
		{"integer", config.ParamInteger, discordgo.ApplicationCommandOptionInteger, false},
		{"boolean", config.ParamBoolean, discordgo.ApplicationCommandOptionBoolean, false},
		{"number", config.ParamNumber, discordgo.ApplicationCommandOptionNumber, false},
		{"user", config.ParamUser, discordgo.ApplicationCommandOptionUser, false},
		{"channel", config.ParamChannel, discordgo.ApplicationCommandOptionChannel, false},
		{"role", config.ParamRole, discordgo.ApplicationCommandOptionRole, false},
		{"invalid", config.ParamType("bogus"), 0, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := paramTypeToDiscordType(tt.in)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("paramTypeToDiscordType(%q): expected error, got nil", tt.in)
				}
				return
			}
			if err != nil {
				t.Fatalf("paramTypeToDiscordType(%q): unexpected error: %v", tt.in, err)
			}
			if got != tt.want {
				t.Errorf("paramTypeToDiscordType(%q) = %v, want %v", tt.in, got, tt.want)
			}
		})
	}
}

func TestBuildApplicationCommand(t *testing.T) {
	cmd := config.Command{
		Name:        "deploy",
		Description: "Deploy the app",
		Params: map[string]config.Param{
			"z-optional-1": {Type: config.ParamString, Description: "opt 1", Required: false},
			"a-required-1": {Type: config.ParamString, Description: "req 1", Required: true},
			"m-optional-2": {Type: config.ParamBoolean, Description: "opt 2", Required: false},
			"b-required-2": {Type: config.ParamInteger, Description: "req 2", Required: true},
			"n-optional-3": {Type: config.ParamString, Required: false}, // empty description
		},
	}

	got := buildApplicationCommand(cmd)

	if got.Name != "deploy" {
		t.Errorf("Name = %q, want %q", got.Name, "deploy")
	}
	if got.Description != "Deploy the app" {
		t.Errorf("Description = %q, want %q", got.Description, "Deploy the app")
	}

	if len(got.Options) != len(cmd.Params) {
		t.Fatalf("len(Options) = %d, want %d", len(got.Options), len(cmd.Params))
	}

	seenOptional := false
	for _, opt := range got.Options {
		if opt.Required {
			if seenOptional {
				t.Fatalf("found required option %q after an optional one; Options must have all required entries before optional ones: %+v", opt.Name, got.Options)
			}
		} else {
			seenOptional = true
		}
		if opt.Description == "" {
			t.Errorf("option %q has empty description, expected placeholder fallback", opt.Name)
		}
	}

	// Verify the empty-description param fell back to a non-empty placeholder.
	var found bool
	for _, opt := range got.Options {
		if opt.Name == "n-optional-3" {
			found = true
			if opt.Description == "" {
				t.Errorf("expected non-empty placeholder description for param with empty Description")
			}
		}
	}
	if !found {
		t.Fatalf("expected to find option %q in built command", "n-optional-3")
	}
}

func TestBuildApplicationCommandEmptyDescriptionFallback(t *testing.T) {
	cmd := config.Command{
		Name:        "noop",
		Description: "",
	}

	got := buildApplicationCommand(cmd)

	if got.Description == "" {
		t.Errorf("expected non-empty placeholder description when Command.Description is empty")
	}
}
