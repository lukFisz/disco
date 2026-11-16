// Package config defines the YAML schema for the disco bot's command
// configuration, and provides parsing/validation for it.
package config

import (
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"time"

	"gopkg.in/yaml.v3"
)

// nameRegex validates command names and param names. This is a simplified
// but valid subset of Discord's actual slash-command-name rules (Discord
// allows a broader Unicode range and different length limits); we
// deliberately restrict to lowercase ASCII letters, digits, underscore and
// hyphen, 1-32 characters, to keep things predictable and shell-safe.
var nameRegex = regexp.MustCompile(`^[a-z0-9_-]{1,32}$`)

// placeholderRegex matches `{name}` style placeholders inside exec entries,
// including dotted names like `{user.id}` used by the built-in placeholders
// below.
var placeholderRegex = regexp.MustCompile(`\{([a-zA-Z0-9_.-]+)\}`)

// Built-in placeholders: reserved `{name}` substitutions available in every
// command's exec without being declared under params. They're populated by
// the discord package from the invoking interaction (see
// discord.builtinExecParams) - config only needs to know their names, to
// exempt them from the "exec references undeclared param" check below.
// Dotted names can never collide with a user-declared param, since
// nameRegex forbids dots in param names.
const (
	PlaceholderCommandName = "command.name"
	PlaceholderUserID      = "user.id"
	PlaceholderUsername    = "user.username"
	PlaceholderChannelID   = "channel.id"
	PlaceholderGuildID     = "guild.id"
)

var builtinPlaceholders = map[string]bool{
	PlaceholderCommandName: true,
	PlaceholderUserID:      true,
	PlaceholderUsername:    true,
	PlaceholderChannelID:   true,
	PlaceholderGuildID:     true,
}

// Default values applied to any AuditConfig field left unset (zero value)
// in YAML. The `audit:` section itself is entirely optional.
const (
	defaultAuditFile       = "audit.log"
	defaultAuditMaxSizeMB  = 100
	defaultAuditMaxBackups = 5
	defaultAuditMaxAgeDays = 30
)

// Duration is a time.Duration that unmarshals from a YAML string (e.g.
// "30s", "5m") via time.ParseDuration.
type Duration time.Duration

// UnmarshalYAML implements yaml.v3's Unmarshaler interface.
func (d *Duration) UnmarshalYAML(value *yaml.Node) error {
	var s string
	if err := value.Decode(&s); err != nil {
		return err
	}
	parsed, err := time.ParseDuration(s)
	if err != nil {
		return fmt.Errorf("invalid duration %q: %w", s, err)
	}
	*d = Duration(parsed)
	return nil
}

// OutputMode controls how a command's output is reported back to Discord.
type OutputMode string

const (
	OutputFull   OutputMode = "full"
	OutputStream OutputMode = "stream"
	OutputSilent OutputMode = "silent"
)

// ParamType is the type of a command parameter, mapped to a Discord slash
// command option type by downstream packages.
type ParamType string

const (
	ParamString  ParamType = "string"
	ParamInteger ParamType = "integer"
	ParamBoolean ParamType = "boolean"
	ParamNumber  ParamType = "number"
	ParamUser    ParamType = "user"
	ParamChannel ParamType = "channel"
	ParamRole    ParamType = "role"
)

// Config is the top-level YAML configuration for the bot.
type Config struct {
	Defaults Defaults           `yaml:"defaults"`
	Audit    AuditConfig        `yaml:"audit"`
	LogLevel string             `yaml:"log_level"`
	Commands map[string]Command `yaml:"commands"`
}

// Defaults holds fallback values applied to commands that don't override
// them. After Config.Validate() succeeds, every Command in Config.Commands
// has its Output, Timeout and AllowConcurrent fields fully resolved, so
// downstream packages never need to consult Defaults directly.
type Defaults struct {
	Timeout         Duration   `yaml:"timeout"`
	Output          OutputMode `yaml:"output"`
	AllowConcurrent bool       `yaml:"allow_concurrent"`
}

// AuditConfig configures audit logging of command executions. The entire
// `audit:` section is optional; any field left unset (zero value) falls
// back to a sensible default during Config.Validate() — see
// defaultAuditFile and friends.
type AuditConfig struct {
	File       string `yaml:"file"`
	MaxSizeMB  int    `yaml:"max_size_mb"`
	MaxBackups int    `yaml:"max_backups"`
	MaxAgeDays int    `yaml:"max_age_days"`
	Compress   bool   `yaml:"compress"`
}

// Command defines a single slash command and the shell command it executes.
type Command struct {
	Name            string           `yaml:"-"` // filled from the map key after unmarshal
	Description     string           `yaml:"description"`
	Exec            []string         `yaml:"exec"`
	Params          map[string]Param `yaml:"params"`
	Output          OutputMode       `yaml:"output"`
	Timeout         Duration         `yaml:"timeout"`
	AllowConcurrent *bool            `yaml:"allow_concurrent"` // nil = inherit from Defaults; guaranteed non-nil after Validate() succeeds
}

// Param defines a single parameter (Discord slash command option) accepted
// by a command.
type Param struct {
	Type        ParamType `yaml:"type"`
	Description string    `yaml:"description"`
	Required    bool      `yaml:"required"`
	Pattern     string    `yaml:"pattern"`
	Choices     []Choice  `yaml:"choices"`

	compiled *regexp.Regexp
}

// Discord's limits on slash command option choices.
const (
	maxChoices         = 25
	maxChoiceNameLen   = 100
	maxChoiceStringLen = 100
)

// Choice is one fixed value a param accepts, shown in Discord as a
// dropdown entry. In YAML it's either a plain scalar (used as both name and
// value) or a {name, value} mapping. Value is kept as a string; for
// integer/number params Validate() canonicalizes it to the same form the
// discord package produces when extracting the invoked option, so the two
// can be compared as strings.
type Choice struct {
	Name  string `yaml:"name"`
	Value string `yaml:"value"`
}

// UnmarshalYAML implements yaml.v3's Unmarshaler interface.
func (c *Choice) UnmarshalYAML(value *yaml.Node) error {
	if value.Kind == yaml.ScalarNode {
		c.Name = value.Value
		c.Value = value.Value
		return nil
	}
	type plain Choice
	return value.Decode((*plain)(c))
}

// Compiled returns the compiled regexp for Pattern, or nil if Pattern was
// empty. It is only populated after a successful Config.Validate() call.
func (p Param) Compiled() *regexp.Regexp {
	return p.compiled
}

// Validate checks the configuration for errors, combining ALL problems
// found (via errors.Join) rather than stopping at the first one. On
// success, it also performs a merge/defaulting pass: each Command's
// Output, Timeout and AllowConcurrent fields are resolved against
// Defaults, so after Validate() returns nil, Command.AllowConcurrent is
// guaranteed non-nil for every command in Config.Commands.
func (c *Config) Validate() error {
	var errs []error

	// Defaults.Output
	if c.Defaults.Output == "" {
		c.Defaults.Output = OutputFull
	} else if !validOutputMode(c.Defaults.Output) {
		errs = append(errs, fmt.Errorf("defaults.output: invalid value %q", c.Defaults.Output))
	}

	// Defaults.Timeout
	if c.Defaults.Timeout <= 0 {
		errs = append(errs, fmt.Errorf("defaults.timeout: must be > 0"))
	}

	// LogLevel
	if c.LogLevel == "" {
		c.LogLevel = "info"
	} else if !validLogLevel(c.LogLevel) {
		errs = append(errs, fmt.Errorf("log_level: invalid value %q", c.LogLevel))
	}

	// Audit: the whole section is optional, so unset (zero-value) fields
	// fall back to sensible defaults rather than erroring. Negative values
	// are still rejected since they're never valid.
	if c.Audit.File == "" {
		c.Audit.File = defaultAuditFile
	}
	if c.Audit.MaxSizeMB < 0 {
		errs = append(errs, fmt.Errorf("audit.max_size_mb: must be >= 0"))
	} else if c.Audit.MaxSizeMB == 0 {
		c.Audit.MaxSizeMB = defaultAuditMaxSizeMB
	}
	if c.Audit.MaxBackups < 0 {
		errs = append(errs, fmt.Errorf("audit.max_backups: must be >= 0"))
	} else if c.Audit.MaxBackups == 0 {
		c.Audit.MaxBackups = defaultAuditMaxBackups
	}
	if c.Audit.MaxAgeDays < 0 {
		errs = append(errs, fmt.Errorf("audit.max_age_days: must be >= 0"))
	} else if c.Audit.MaxAgeDays == 0 {
		c.Audit.MaxAgeDays = defaultAuditMaxAgeDays
	}

	// Commands
	for name, cmd := range c.Commands {
		cmdErrs := validateCommand(name, &cmd)
		errs = append(errs, cmdErrs...)
		c.Commands[name] = cmd
	}

	if len(errs) > 0 {
		return errors.Join(errs...)
	}

	// Merge/defaulting pass - only runs once per-field validation succeeds.
	for name, cmd := range c.Commands {
		if cmd.Output == "" {
			cmd.Output = c.Defaults.Output
		}
		if cmd.Timeout == 0 {
			cmd.Timeout = c.Defaults.Timeout
		}
		if cmd.AllowConcurrent == nil {
			v := c.Defaults.AllowConcurrent
			cmd.AllowConcurrent = &v
		}
		c.Commands[name] = cmd
	}

	return nil
}

func validateCommand(name string, cmd *Command) []error {
	var errs []error

	if !nameRegex.MatchString(name) {
		errs = append(errs, fmt.Errorf("command %q: invalid name (must match %s)", name, nameRegex.String()))
	}

	if len(cmd.Exec) == 0 {
		errs = append(errs, fmt.Errorf("command %q: exec must be non-empty", name))
	}

	if cmd.Output != "" && !validOutputMode(cmd.Output) {
		errs = append(errs, fmt.Errorf("command %q: output: invalid value %q", name, cmd.Output))
	}

	if cmd.Timeout < 0 {
		errs = append(errs, fmt.Errorf("command %q: timeout: must be >= 0", name))
	}

	for pname, p := range cmd.Params {
		if !nameRegex.MatchString(pname) {
			errs = append(errs, fmt.Errorf("command %q: param %q: invalid name (must match %s)", name, pname, nameRegex.String()))
		}
		if !validParamType(p.Type) {
			errs = append(errs, fmt.Errorf("command %q: param %q: invalid type %q", name, pname, p.Type))
		}
		if p.Pattern != "" {
			compiled, err := regexp.Compile(p.Pattern)
			if err != nil {
				errs = append(errs, fmt.Errorf("command %q: param %q: invalid pattern %q: %w", name, pname, p.Pattern, err))
			} else {
				p.compiled = compiled
				cmd.Params[pname] = p
			}
		}
		if len(p.Choices) > 0 {
			errs = append(errs, validateChoices(name, pname, &p)...)
			cmd.Params[pname] = p
		}
	}

	// Scan exec entries for {name} placeholders and ensure each references
	// either a declared param or a reserved built-in placeholder (see
	// builtinPlaceholders). Unused-param detection (params declared but
	// never referenced in exec) is intentionally not implemented - dropped
	// as unnecessary for v1.
	for _, entry := range cmd.Exec {
		matches := placeholderRegex.FindAllStringSubmatch(entry, -1)
		for _, m := range matches {
			placeholder := m[1]
			if builtinPlaceholders[placeholder] {
				continue
			}
			if _, ok := cmd.Params[placeholder]; !ok {
				errs = append(errs, fmt.Errorf("command %q: exec references undeclared param {%s}", name, placeholder))
			}
		}
	}

	return errs
}

// validateChoices checks p.Choices against Discord's limits and p.Type,
// canonicalizing integer/number values in place (e.g. "1.50" -> "1.5").
func validateChoices(name, pname string, p *Param) []error {
	var errs []error
	prefix := fmt.Sprintf("command %q: param %q: choices", name, pname)

	switch p.Type {
	case ParamString, ParamInteger, ParamNumber:
	default:
		return []error{fmt.Errorf("%s: only supported for string, integer and number params, not %q", prefix, p.Type)}
	}
	if len(p.Choices) > maxChoices {
		errs = append(errs, fmt.Errorf("%s: at most %d allowed, got %d", prefix, maxChoices, len(p.Choices)))
	}

	choices := make([]Choice, len(p.Choices))
	seen := make(map[string]bool, len(p.Choices))
	for i, ch := range p.Choices {
		if ch.Name == "" || len(ch.Name) > maxChoiceNameLen {
			errs = append(errs, fmt.Errorf("%s[%d]: name must be 1-%d characters", prefix, i, maxChoiceNameLen))
		}
		switch p.Type {
		case ParamString:
			if ch.Value == "" || len(ch.Value) > maxChoiceStringLen {
				errs = append(errs, fmt.Errorf("%s[%d]: value must be 1-%d characters", prefix, i, maxChoiceStringLen))
			} else if p.compiled != nil && !p.compiled.MatchString(ch.Value) {
				errs = append(errs, fmt.Errorf("%s[%d]: value %q does not match pattern %q", prefix, i, ch.Value, p.Pattern))
			}
		case ParamInteger:
			n, err := strconv.ParseInt(ch.Value, 10, 64)
			if err != nil {
				errs = append(errs, fmt.Errorf("%s[%d]: value %q is not a valid integer", prefix, i, ch.Value))
			} else {
				ch.Value = strconv.FormatInt(n, 10)
			}
		case ParamNumber:
			f, err := strconv.ParseFloat(ch.Value, 64)
			if err != nil {
				errs = append(errs, fmt.Errorf("%s[%d]: value %q is not a valid number", prefix, i, ch.Value))
			} else {
				ch.Value = strconv.FormatFloat(f, 'f', -1, 64)
			}
		}
		if seen[ch.Value] {
			errs = append(errs, fmt.Errorf("%s[%d]: duplicate value %q", prefix, i, ch.Value))
		}
		seen[ch.Value] = true
		choices[i] = ch
	}
	p.Choices = choices

	return errs
}

func validOutputMode(o OutputMode) bool {
	switch o {
	case OutputFull, OutputStream, OutputSilent:
		return true
	default:
		return false
	}
}

func validLogLevel(s string) bool {
	switch s {
	case "debug", "info", "warn", "error":
		return true
	default:
		return false
	}
}

func validParamType(t ParamType) bool {
	switch t {
	case ParamString, ParamInteger, ParamBoolean, ParamNumber, ParamUser, ParamChannel, ParamRole:
		return true
	default:
		return false
	}
}
