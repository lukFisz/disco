package config

import (
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"
)

func writeConfig(t *testing.T, contents string) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(path, []byte(contents), 0o644); err != nil {
		t.Fatalf("writing fixture: %v", err)
	}
	return path
}

const validYAML = `
defaults:
  timeout: 30s
  output: full
  allow_concurrent: true
audit:
  file: /var/log/disco/audit.log
  max_size_mb: 10
  max_backups: 3
  max_age_days: 30
  compress: true
commands:
  deploy:
    exec: ["./deploy.sh", "{service}"]
    params:
      service:
        type: string
        required: true
        pattern: "^[a-zA-Z0-9_-]+$"
    output: stream
    timeout: 5m
    allow_concurrent: false
  status:
    exec: ["./status.sh"]
`

func TestLoad_ValidConfig_DefaultsMerge(t *testing.T) {
	path := writeConfig(t, validYAML)
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load() returned error: %v", err)
	}

	// "status" command overrides nothing, should inherit defaults.
	status, ok := cfg.Commands["status"]
	if !ok {
		t.Fatalf("expected command %q to exist", "status")
	}
	if status.Name != "status" {
		t.Errorf("status.Name = %q, want %q", status.Name, "status")
	}
	if status.Output != OutputFull {
		t.Errorf("status.Output = %q, want %q (inherited from defaults)", status.Output, OutputFull)
	}
	if time.Duration(status.Timeout) != 30*time.Second {
		t.Errorf("status.Timeout = %v, want 30s (inherited from defaults)", time.Duration(status.Timeout))
	}
	if status.AllowConcurrent == nil {
		t.Fatalf("status.AllowConcurrent is nil, want non-nil after Validate()")
	}
	if *status.AllowConcurrent != true {
		t.Errorf("status.AllowConcurrent = %v, want true (inherited from defaults)", *status.AllowConcurrent)
	}
}

func TestLoad_CommandOverridesKeepOwnValues(t *testing.T) {
	path := writeConfig(t, validYAML)
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load() returned error: %v", err)
	}

	deploy, ok := cfg.Commands["deploy"]
	if !ok {
		t.Fatalf("expected command %q to exist", "deploy")
	}
	if deploy.Output != OutputStream {
		t.Errorf("deploy.Output = %q, want %q (own override)", deploy.Output, OutputStream)
	}
	if time.Duration(deploy.Timeout) != 5*time.Minute {
		t.Errorf("deploy.Timeout = %v, want 5m (own override)", time.Duration(deploy.Timeout))
	}
	if deploy.AllowConcurrent == nil {
		t.Fatalf("deploy.AllowConcurrent is nil, want non-nil")
	}
	if *deploy.AllowConcurrent != false {
		t.Errorf("deploy.AllowConcurrent = %v, want false (own override, must NOT inherit true from defaults)", *deploy.AllowConcurrent)
	}

	// Param pattern should be compiled and reusable.
	svcParam := deploy.Params["service"]
	if svcParam.Compiled() == nil {
		t.Fatalf("deploy param %q: Compiled() is nil, want compiled regexp", "service")
	}
	if !svcParam.Compiled().MatchString("my-service_1") {
		t.Errorf("compiled pattern did not match expected valid value")
	}
}

func TestLoad_AllowConcurrentFalseNotNilDistinction(t *testing.T) {
	const yamlStr = `
defaults:
  timeout: 10s
  output: full
  allow_concurrent: true
audit:
  file: /tmp/audit.log
commands:
  explicit_false:
    exec: ["./run.sh"]
    allow_concurrent: false
  inherit:
    exec: ["./run.sh"]
`
	path := writeConfig(t, yamlStr)
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load() returned error: %v", err)
	}

	ef := cfg.Commands["explicit_false"]
	if ef.AllowConcurrent == nil || *ef.AllowConcurrent != false {
		t.Errorf("explicit_false.AllowConcurrent = %v, want pointer to false", ef.AllowConcurrent)
	}

	inh := cfg.Commands["inherit"]
	if inh.AllowConcurrent == nil || *inh.AllowConcurrent != true {
		t.Errorf("inherit.AllowConcurrent = %v, want pointer to true (inherited default)", inh.AllowConcurrent)
	}
}

func TestValidate_MissingExec(t *testing.T) {
	const yamlStr = `
defaults:
  timeout: 10s
audit:
  file: /tmp/audit.log
commands:
  broken:
    description: "no exec"
`
	path := writeConfig(t, yamlStr)
	_, err := Load(path)
	if err == nil {
		t.Fatal("expected error for missing exec, got nil")
	}
}

func TestValidate_EmptyExec(t *testing.T) {
	const yamlStr = `
defaults:
  timeout: 10s
audit:
  file: /tmp/audit.log
commands:
  broken:
    exec: []
`
	path := writeConfig(t, yamlStr)
	_, err := Load(path)
	if err == nil {
		t.Fatal("expected error for empty exec, got nil")
	}
}

func TestValidate_BadRegexPattern(t *testing.T) {
	const yamlStr = `
defaults:
  timeout: 10s
audit:
  file: /tmp/audit.log
commands:
  cmd:
    exec: ["./run.sh", "{name}"]
    params:
      name:
        type: string
        pattern: "["
`
	path := writeConfig(t, yamlStr)
	_, err := Load(path)
	if err == nil {
		t.Fatal("expected error for uncompilable regex, got nil")
	}
}

func TestValidate_UnknownParamType(t *testing.T) {
	const yamlStr = `
defaults:
  timeout: 10s
audit:
  file: /tmp/audit.log
commands:
  cmd:
    exec: ["./run.sh", "{n}"]
    params:
      n:
        type: float
`
	path := writeConfig(t, yamlStr)
	_, err := Load(path)
	if err == nil {
		t.Fatal("expected error for unknown param type, got nil")
	}
}

func TestValidate_AllParamTypesAccepted(t *testing.T) {
	for _, typ := range []string{"string", "integer", "boolean", "number", "user", "channel", "role"} {
		typ := typ
		t.Run(typ, func(t *testing.T) {
			yamlStr := `
defaults:
  timeout: 10s
audit:
  file: /tmp/audit.log
commands:
  cmd:
    exec: ["./run.sh", "{p}"]
    params:
      p:
        type: ` + typ + `
`
			path := writeConfig(t, yamlStr)
			if _, err := Load(path); err != nil {
				t.Fatalf("Load() returned error for param type %q: %v", typ, err)
			}
		})
	}
}

func TestValidate_BuiltinPlaceholdersAllowedWithoutDeclaration(t *testing.T) {
	const yamlStr = `
defaults:
  timeout: 10s
audit:
  file: /tmp/audit.log
commands:
  cmd:
    exec: ["./run.sh", "{command.name}", "{user.id}", "{user.username}", "{channel.id}", "{guild.id}"]
`
	path := writeConfig(t, yamlStr)
	if _, err := Load(path); err != nil {
		t.Fatalf("Load() returned error for exec using only built-in placeholders: %v", err)
	}
}

func TestValidate_UnknownDottedPlaceholderRejected(t *testing.T) {
	const yamlStr = `
defaults:
  timeout: 10s
audit:
  file: /tmp/audit.log
commands:
  cmd:
    exec: ["./run.sh", "{bogus.thing}"]
`
	path := writeConfig(t, yamlStr)
	_, err := Load(path)
	if err == nil {
		t.Fatal("expected error for undeclared dotted placeholder, got nil")
	}
}

func TestValidate_InvalidCommandName(t *testing.T) {
	const yamlStr = `
defaults:
  timeout: 10s
audit:
  file: /tmp/audit.log
commands:
  "Bad Name":
    exec: ["./run.sh"]
`
	path := writeConfig(t, yamlStr)
	_, err := Load(path)
	if err == nil {
		t.Fatal("expected error for invalid command name, got nil")
	}
}

func TestValidate_InvalidParamName(t *testing.T) {
	const yamlStr = `
defaults:
  timeout: 10s
audit:
  file: /tmp/audit.log
commands:
  cmd:
    exec: ["./run.sh"]
    params:
      "Bad Param":
        type: string
`
	path := writeConfig(t, yamlStr)
	_, err := Load(path)
	if err == nil {
		t.Fatal("expected error for invalid param name, got nil")
	}
}

func TestValidate_UndeclaredPlaceholder(t *testing.T) {
	const yamlStr = `
defaults:
  timeout: 10s
audit:
  file: /tmp/audit.log
commands:
  cmd:
    exec: ["./run.sh", "{missing}"]
    params:
      other:
        type: string
`
	path := writeConfig(t, yamlStr)
	_, err := Load(path)
	if err == nil {
		t.Fatal("expected error for undeclared placeholder, got nil")
	}
}

func TestDuration_UnmarshalRoundTrip(t *testing.T) {
	const yamlStr = `
defaults:
  timeout: 30s
audit:
  file: /tmp/audit.log
commands:
  cmd:
    exec: ["./run.sh"]
    timeout: 90s
`
	path := writeConfig(t, yamlStr)
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load() returned error: %v", err)
	}
	if time.Duration(cfg.Defaults.Timeout) != 30*time.Second {
		t.Errorf("Defaults.Timeout = %v, want 30s", time.Duration(cfg.Defaults.Timeout))
	}
	if time.Duration(cfg.Commands["cmd"].Timeout) != 90*time.Second {
		t.Errorf("cmd.Timeout = %v, want 90s", time.Duration(cfg.Commands["cmd"].Timeout))
	}
}

func TestValidate_MultipleErrorsJoined(t *testing.T) {
	const yamlStr = `
defaults:
  timeout: 10s
audit:
  file: /tmp/audit.log
commands:
  "Bad Name":
    exec: []
    params:
      n:
        type: float
`
	path := writeConfig(t, yamlStr)
	_, err := Load(path)
	if err == nil {
		t.Fatal("expected error, got nil")
	}

	// Unwrap through Load's fmt.Errorf wrapping to reach the joined error.
	var joined interface{ Unwrap() []error }
	found := errors.As(err, &joined)
	if !found {
		t.Fatalf("expected to find a joined error (via errors.As) in chain: %v", err)
	}
	sub := joined.Unwrap()
	if len(sub) < 3 {
		t.Errorf("expected at least 3 distinct validation errors joined, got %d: %v", len(sub), sub)
	}
}

func TestValidate_DefaultsTimeoutMustBePositive(t *testing.T) {
	const yamlStr = `
defaults:
  timeout: 0s
audit:
  file: /tmp/audit.log
commands:
  cmd:
    exec: ["./run.sh"]
`
	path := writeConfig(t, yamlStr)
	_, err := Load(path)
	if err == nil {
		t.Fatal("expected error for zero defaults.timeout, got nil")
	}
}

func TestValidate_AuditSectionOptional_DefaultsApplied(t *testing.T) {
	const yamlStr = `
defaults:
  timeout: 10s
commands:
  cmd:
    exec: ["./run.sh"]
`
	path := writeConfig(t, yamlStr)
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load() with no audit section returned error: %v", err)
	}
	if cfg.Audit.File != defaultAuditFile {
		t.Errorf("Audit.File = %q, want default %q", cfg.Audit.File, defaultAuditFile)
	}
	if cfg.Audit.MaxSizeMB != defaultAuditMaxSizeMB {
		t.Errorf("Audit.MaxSizeMB = %d, want default %d", cfg.Audit.MaxSizeMB, defaultAuditMaxSizeMB)
	}
	if cfg.Audit.MaxBackups != defaultAuditMaxBackups {
		t.Errorf("Audit.MaxBackups = %d, want default %d", cfg.Audit.MaxBackups, defaultAuditMaxBackups)
	}
	if cfg.Audit.MaxAgeDays != defaultAuditMaxAgeDays {
		t.Errorf("Audit.MaxAgeDays = %d, want default %d", cfg.Audit.MaxAgeDays, defaultAuditMaxAgeDays)
	}
}

func TestValidate_AuditPartialOverride_KeepsOwnValues(t *testing.T) {
	const yamlStr = `
defaults:
  timeout: 10s
audit:
  file: /var/log/disco/audit.log
  max_backups: 2
commands:
  cmd:
    exec: ["./run.sh"]
`
	path := writeConfig(t, yamlStr)
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load() returned error: %v", err)
	}
	if cfg.Audit.File != "/var/log/disco/audit.log" {
		t.Errorf("Audit.File = %q, want own value preserved", cfg.Audit.File)
	}
	if cfg.Audit.MaxBackups != 2 {
		t.Errorf("Audit.MaxBackups = %d, want own value 2 preserved", cfg.Audit.MaxBackups)
	}
	// Unset fields still fall back to defaults.
	if cfg.Audit.MaxSizeMB != defaultAuditMaxSizeMB {
		t.Errorf("Audit.MaxSizeMB = %d, want default %d", cfg.Audit.MaxSizeMB, defaultAuditMaxSizeMB)
	}
	if cfg.Audit.MaxAgeDays != defaultAuditMaxAgeDays {
		t.Errorf("Audit.MaxAgeDays = %d, want default %d", cfg.Audit.MaxAgeDays, defaultAuditMaxAgeDays)
	}
}

func TestValidate_LogLevelDefaultApplied(t *testing.T) {
	const yamlStr = `
defaults:
  timeout: 10s
audit:
  file: /tmp/audit.log
commands:
  cmd:
    exec: ["./run.sh"]
`
	path := writeConfig(t, yamlStr)
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load() with no log_level returned error: %v", err)
	}
	if cfg.LogLevel != "info" {
		t.Errorf("LogLevel = %q, want default %q", cfg.LogLevel, "info")
	}
}

func TestValidate_LogLevelValidValuesAccepted(t *testing.T) {
	for _, level := range []string{"debug", "info", "warn", "error"} {
		level := level
		t.Run(level, func(t *testing.T) {
			yamlStr := `
defaults:
  timeout: 10s
audit:
  file: /tmp/audit.log
log_level: ` + level + `
commands:
  cmd:
    exec: ["./run.sh"]
`
			path := writeConfig(t, yamlStr)
			cfg, err := Load(path)
			if err != nil {
				t.Fatalf("Load() returned error: %v", err)
			}
			if cfg.LogLevel != level {
				t.Errorf("LogLevel = %q, want %q", cfg.LogLevel, level)
			}
		})
	}
}

func TestValidate_LogLevelInvalidRejected(t *testing.T) {
	const yamlStr = `
defaults:
  timeout: 10s
audit:
  file: /tmp/audit.log
log_level: verbose
commands:
  cmd:
    exec: ["./run.sh"]
`
	path := writeConfig(t, yamlStr)
	_, err := Load(path)
	if err == nil {
		t.Fatal("expected error for invalid log_level, got nil")
	}
}

func TestValidate_AuditNegativeValuesRejected(t *testing.T) {
	const yamlStr = `
defaults:
  timeout: 10s
audit:
  max_size_mb: -1
commands:
  cmd:
    exec: ["./run.sh"]
`
	path := writeConfig(t, yamlStr)
	_, err := Load(path)
	if err == nil {
		t.Fatal("expected error for negative audit.max_size_mb, got nil")
	}
}

func TestValidate_ChoicesScalarAndMapping(t *testing.T) {
	const yamlStr = `
defaults:
  timeout: 10s
commands:
  cmd:
    exec: ["./run.sh", "{env}", "{level}", "{ratio}"]
    params:
      env:
        type: string
        choices:
          - staging
          - name: Production
            value: prod
      level:
        type: integer
        choices: [1, "02"]
      ratio:
        type: number
        choices: ["1.50", 2]
`
	cfg, err := Load(writeConfig(t, yamlStr))
	if err != nil {
		t.Fatalf("Load() returned error: %v", err)
	}
	params := cfg.Commands["cmd"].Params

	wantEnv := []Choice{{Name: "staging", Value: "staging"}, {Name: "Production", Value: "prod"}}
	for i, want := range wantEnv {
		if got := params["env"].Choices[i]; got != want {
			t.Errorf("env choice %d = %+v, want %+v", i, got, want)
		}
	}
	// integer/number values are canonicalized, names kept as written.
	if got := params["level"].Choices[1]; got.Value != "2" || got.Name != "02" {
		t.Errorf("level choice 1 = %+v, want value %q name %q", got, "2", "02")
	}
	if got := params["ratio"].Choices[0]; got.Value != "1.5" {
		t.Errorf("ratio choice 0 value = %q, want %q", got.Value, "1.5")
	}
}

func TestValidate_ChoicesInvalid(t *testing.T) {
	tests := map[string]string{
		"unsupported type": `
        type: boolean
        choices: ["true"]`,
		"non-integer value": `
        type: integer
        choices: [abc]`,
		"non-number value": `
        type: number
        choices: [abc]`,
		"duplicate value": `
        type: integer
        choices: [1, "01"]`,
		"empty string value": `
        type: string
        choices: [{name: Empty, value: ""}]`,
		"value fails pattern": `
        type: string
        pattern: "^[a-z]+$"
        choices: [ok, NOT_OK]`,
	}
	for name, param := range tests {
		t.Run(name, func(t *testing.T) {
			yamlStr := `
defaults:
  timeout: 10s
commands:
  cmd:
    exec: ["./run.sh", "{p}"]
    params:
      p:` + param + "\n"
			if _, err := Load(writeConfig(t, yamlStr)); err == nil {
				t.Fatal("expected error, got nil")
			}
		})
	}
}

func TestValidate_TooManyChoices(t *testing.T) {
	yamlStr := `
defaults:
  timeout: 10s
commands:
  cmd:
    exec: ["./run.sh", "{p}"]
    params:
      p:
        type: integer
        choices: [`
	for i := 0; i <= maxChoices; i++ {
		if i > 0 {
			yamlStr += ", "
		}
		yamlStr += strconv.Itoa(i)
	}
	yamlStr += "]\n"
	if _, err := Load(writeConfig(t, yamlStr)); err == nil {
		t.Fatalf("expected error for more than %d choices, got nil", maxChoices)
	}
}
