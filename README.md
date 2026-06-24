# wherenv

**Which startup file set this environment variable?**

You open a new shell and `PATH` has some entry you don't recognize. Or `EDITOR`
is `nano` and you have no idea which dotfile is to blame. `wherenv` answers the
question by tracing what your shell *actually does* at startup.

By default it prints one tab-separated record per line — built to pipe into
`grep`, `awk`, `cut`, and friends:

```console
$ wherenv PATH
PATH	startup	/etc/zprofile	12	exact	login
PATH	startup	/Users/you/.orbstack/shell/init.zsh	1	exact	login
PATH	startup	/Users/you/.config/zsh/conf.d/07-tools.zsh	39	exact	login	winner=login
```

Add `--human` (or `-H`) for the formatted, stack-trace-style view:

```console
$ wherenv --human PATH
PATH: set by startup  (3 places, most recent first)

  → /Users/you/.config/zsh/conf.d/07-tools.zsh:39   ← ran last
    /Users/you/.orbstack/shell/init.zsh:1
    /etc/zprofile:12
```

Assignments are listed like a stack trace — most recent on top, with the line
that **ran last** marked. `wherenv` reports *where* and *in what order*, which it
knows for certain; it doesn't guess which assignment "wins" (with `path=($path …)`
arrays and `$VAR`-expansion that can't be decided from the trace alone). To see
what a line actually assigns, open that file at that line yourself.

It works with **zsh** and **bash**, follows `source`d files, and tells you when
a variable was inherited from your terminal/login session rather than set by any
dotfile.

**`wherenv` never reads, prints, stores, or logs your variables' values.** It is
built to answer *where*, not *what* — so you can run `wherenv AWS_SECRET_ACCESS_KEY`
and the only thing it can possibly show you is a `file:line`. See
[Secrets & memory](#secrets--memory) for exactly what this guarantee covers.

> Unlike grepping your dotfiles, `wherenv` runs your real startup in a traced
> subshell, so it sees through conditionals, loops, `eval`, and sourced files —
> and reports the line that actually ran.

---

## Install

With Go (1.25+):

```sh
go install github.com/S-Nakamur-a/wherenv/cmd/wherenv@latest
```

This drops a `wherenv` binary in `$(go env GOPATH)/bin` (add it to your `PATH`
if it isn't already).

From source:

```sh
git clone https://github.com/S-Nakamur-a/wherenv
cd wherenv
make install     # or: make build  → ./bin/wherenv
```

Platform: developed and tested on **macOS**. The `launchctl` probe (used for
inherited variables) is macOS-specific; the core tracing relies only on zsh/bash
`xtrace`, so most of it works on Linux too, but that path is not yet tested.

---

## Usage

```
wherenv [flags] [VARNAME...]
```

```sh
wherenv                       # no VARNAME: trace every visible variable (sorted)
wherenv PATH                  # default: tab-separated, one record per line
wherenv PATH GOPATH EDITOR    # several at once
wherenv PATH | awk -F'\t' '$7 ~ /winner/'   # the effective assignment(s)
wherenv --human PATH          # formatted, human-readable view
wherenv --json PATH           # JSON (for jq)
wherenv --mode both PATH      # show how login and non-login differ
```

With **no `VARNAME`**, `wherenv` traces every environment variable currently
visible to the process — the same provenance trace it runs for a single variable,
applied to all of them and emitted in sorted (variable-name) order. This is still
a single shell trace per mode: the startup files are sourced once and every
assignment is attributed, so there is no per-variable spawn.

### Flags

| Flag | Default | Description |
|------|---------|-------------|
| `--human`, `-H` | off | Human-readable, formatted output instead of the default TSV. |
| `--mode` | `login` | Shell mode(s) to trace: `login`, `non-login`, or `both`. |
| `--color` | `auto` | Colorize the `--human` output: `auto` (TTY only, respects `NO_COLOR`), `always`, `never`. Never applies to TSV/JSON. |
| `--json` | off | Emit JSON instead of the default TSV. |
| `--timeout` | 8.0 | Per-spawn timeout in seconds (each traced mode gets this budget). |

The default output is **machine-readable TSV** regardless of whether stdout is a
terminal, so a pipe or redirect always gets the same clean records. The formatted
view is opt-in via `--human`. (`-h` is reserved for help, so the short alias is
`-H`.)

There is intentionally **no flag to show values** — `wherenv` does not have the
value to show (see [Secrets & memory](#secrets--memory)). When a variable is
overwritten several times and you want to see how the value was built up, open
the listed `file:line`s yourself.

**Why `--mode` defaults to `login`:** on zsh a login shell also sources the
non-login files (`.zshrc`), so login is a superset and matches how macOS
terminals start — one fast trace covers everything. Use `--mode both` to see how
login and non-login differ (each assignment is then tagged with the mode it ran
in). On **bash**, login does *not* read `.bashrc` unless your `.bash_profile`
sources it — use `--mode both` or `--mode non-login` if your settings live in
`.bashrc`.

### Exit codes

| Code | Meaning |
|------|---------|
| 0 | Ran successfully (including "not set"). |
| 1 | Internal error while formatting output. |
| 2 | Bad usage: an invalid variable name or flag. |

Variable names must match `^[A-Za-z_][A-Za-z0-9_]*$`. This is validated up front
and is also what keeps your input from ever reaching a shell.

---

## How it works

`wherenv` spawns your shell (`$SHELL`) twice — once non-login, once login — with
`set -x` (xtrace) enabled and a unique, random marker baked into `PS4` so it can
recognize its own trace lines. It parses that trace to find where each requested
variable was assigned, recording the file and line for every assignment. The
right-hand side of each assignment (the value) is used only long enough to
recover the variable name and the `+=` vs `=` operator, then discarded — it is
never stored.

- **Source-following**: because the trace reports the *actual* file being
  executed, assignments inside `source`d scripts are attributed to that script,
  not to the file that sourced it.
- **Login vs non-login**: the two modes read different files (e.g. `.zprofile`
  is login-only). Each result is tagged with the mode(s) it appeared in rather
  than blended together.
- **Tool-set variables**: after tracing startup files, `wherenv` probes
  developer tools that set environment variables through hooks rather than
  startup files. Each probe inspects only enough metadata to confirm *which file*
  set the variable; the value recorded by the tool is never copied out.
  Currently supported:
  - **direnv** — the `DIRENV_DIFF` variable is decoded to identify which
    variables were loaded from the current `.envrc`, and their source file is
    reported.
  - **mise** — `mise env --json-extended` is run once to learn which variables
    mise set via `[env]` in `mise.toml`, with the source file for each. Variables
    mise manages for other reasons (PATH, shims) are excluded. The probe degrades
    gracefully if mise is not installed or fails.
- **Inherited variables**: if nothing in startup or a known tool assigns a
  variable but it exists in your environment, it was inherited from the parent
  process (terminal, `launchd`, login session). On macOS, `wherenv` checks
  whether the variable is present in the `launchd` session — see below for how it
  does this without reading the value.

There's a deeper write-up of the mechanism and design trade-offs if you're
curious about the internals — see the project's design notes.

### Secrets & memory

`wherenv`'s entire job is provenance (*where* a variable was set), and it is
built so that values are not its business:

- **Never retained.** No value is ever stored in `wherenv`'s data structures,
  printed, written to disk, or logged (including under `WHERENV_DEBUG`). The
  output is locations and tool/provenance only.
- **`launchctl` without reading the value.** `launchctl getenv NAME` prints the
  value and exits `0` whether or not the variable is set, so presence can only be
  told from whether it printed anything. `wherenv` pipes `launchctl`'s output
  straight into `wc -c` (through an OS pipe, no shell) and reads back only the
  byte count — the value flows `launchctl → kernel pipe → wc` and never enters
  `wherenv`'s address space.
- **What can't be avoided.** Like *any* program, `wherenv` starts with your full
  environment block already in memory (the OS hands every process its `envp`).
  And the xtrace stream, `DIRENV_DIFF`, and mise's JSON inherently contain values
  while they are being parsed. `wherenv` does not parse what it doesn't need and
  drops these the instant it has the `file:line`, but it cannot pretend the bytes
  never transit memory. The guarantee is **"not retained, not surfaced"** — not
  "never touched."

### ⚠️ Side effects

**`wherenv` executes your real shell startup files.** Anything your `.zshrc`,
`.zprofile`, `.bashrc`, etc. (and the scripts they source) does — network calls,
starting `ssh-agent`, spawning daemons, writing files — runs each time you
invoke `wherenv`. Background processes started during the trace are cleaned up
afterward. This is inherent to tracing rather than guessing; avoid running
`wherenv` in tight loops or automation where those side effects matter.

### Supported shells and modes

| Shell | Non-login reads | Login reads |
|-------|-----------------|-------------|
| zsh   | `.zshenv`, `.zshrc`, sourced scripts | the above **plus** `.zprofile`, `.zlogin`, `/etc/zprofile` |
| bash  | `.bashrc` | `.bash_profile` (or `.profile`) |

Other shells (fish, sh, dash, …) are not traced. `wherenv` will warn and
classify variables from the current environment only (inherited / not set).

---

## Output

### Default: tab-separated records (TSV)

The default format is one record per line, with **no header row** (so every line
is data) and exactly nine tab-separated columns:

| # | Column | Notes |
|---|--------|-------|
| 1 | `name` | variable name |
| 2 | `origin` | `startup`, `inherited`, `toolset`, or `unset` |
| 3 | `file` | source file (empty when none); terminal-control chars and tabs are neutralized |
| 4 | `line` | 1-based line number (empty when unknown / not applicable) |
| 5 | `line_confidence` | `exact`, `best-effort`, or `unknown` (empty when not applicable) |
| 6 | `modes` | e.g. `login`, `non-login`, `non-login+login` (empty when not applicable) |
| 7 | `attrs` | comma-separated tokens (empty when none) |
| 8 | `caller_file` | when the assignment ran inside a helper function, the file that called it (the line you edit); empty for a direct assignment. Columns 3/4 stay the precise mechanism. |
| 9 | `caller_line` | 1-based line in `caller_file` (empty when not applicable) |

`caller_file`/`caller_line` are their own columns (not an `attrs` token) because a
file path may contain a comma, which would break `attrs` parsing.

A `startup` variable emits **one line per assignment site** (so you can `grep` by
file or `cut` the line number); every other origin emits one line. The `attrs`
column carries the extra semantics:

| Token | Meaning |
|-------|---------|
| `winner=<mode>` | this site is the effective last assignment for `<mode>` |
| `append` | this site used `+=` (cumulative) |
| `tool=<name>` | the tool that set a `toolset` variable (e.g. `tool=direnv`) |
| `launchd` | inherited from the macOS launchd session |
| `incomplete` | the startup trace ended before its sentinel — treat as uncertain |

```console
$ wherenv PATH EDITOR TERM_PROGRAM NOPE | cat   # cat -> not a TTY, same output
PATH	startup	/etc/zprofile	12	exact	login
PATH	startup	/Users/you/.zshrc	39	exact	login	winner=login
EDITOR	toolset	/Users/you/project/.envrc			tool=direnv
TERM_PROGRAM	inherited					launchd
NOPE	unset
```

### `--human`: a variable set during startup

```
PATH: set by startup  (3 places, most recent first)

  → /Users/you/.zshrc:12   ← ran last
    /opt/homebrew/etc/profile.d/x.sh:2
    /etc/zprofile:12
```

The assignments are listed like a stack trace — most recent on top, the
last-executed one marked `← ran last`. This is purely about *order*, not
precedence: `wherenv` won't claim which assignment "wins", because cumulative
forms like `export PATH=$PATH:…` and `path=($path …)` mean earlier lines often
still contribute. Open the listed `file:line`s to see how the value was built up.

With `--mode both`, each assignment is also tagged with the mode it ran in
(`[login]`, `[non-login]`, or `[non-login+login]`), since which files run depends
on whether your session is a login shell.

### A variable set through a helper function

When a variable is exported by a generic helper (for example an `envsource`-style
loader that reads a `.env` file and runs `export "$key=$value"`), the bare
`file:line` of the `export` points at the helper, not at the file you would edit.
`wherenv` surfaces the **call site** as the primary location and keeps the helper
as a `(via …)` note:

```
AWS_PROFILE: set by startup
  ~/.config/zsh/conf.d/08-profile.zsh:7  (via ~/.config/zsh/functions/envsource:16)
```

Here line 7 of `08-profile.zsh` is the `envsource …` call you control; the helper
that physically ran the `export` is shown in parentheses. In the default TSV the
same fact is the `caller_file`/`caller_line` columns (8/9), with `file`/`line`
(3/4) still the helper:

```
AWS_PROFILE	startup	~/.config/zsh/functions/envsource	16	exact	login	winner=login	~/.config/zsh/conf.d/08-profile.zsh	7
```

(zsh only — it relies on `funcfiletrace`, captured by briefly enabling
`prompt_subst` during the trace. The `.env` data file itself is read as data,
never executed, so it cannot be traced; the call site is the closest provable
location.)

### Output streams

`wherenv` follows the usual convention: the **findings go to stdout**, while the
spinner, warnings, and `WHERENV_DEBUG` logs go to **stderr**. So
`wherenv FOO > out.txt` captures just the result, and `wherenv FOO 2>/dev/null`
silences the progress noise.

### `--human`: a variable not set by any startup file

```
TERM_PROGRAM: present in the environment, not set by any startup file
  → inherited from the parent process, or exported interactively / by a tool (these can't be traced)
```

(In the default TSV this is `TERM_PROGRAM⇥inherited⇥…`, with a `launchd` token in
the `attrs` column when it came from the macOS launchd session.)

`wherenv` only claims what it can prove: the variable is in your environment but
no traced startup file sets it. It doesn't assert "inherited from the parent",
because the same observation also covers a value you `export`ed by hand or one a
runtime hook set. When the variable is present in the macOS `launchd` session,
`wherenv` says so (`→ set in the launchd session`) instead.

### `--human`: an unset variable

```
MY_VAR: not set
```

(In the default TSV this is `MY_VAR⇥unset`.)

---

## Known limitations

- **bash 3.2 line numbers** — macOS ships bash 3.2, whose `PS4` expansion is
  capped at 99 bytes. With long file paths the exact line number can be lost;
  `wherenv` keeps the file and marks the line as approximate / uncertain. bash
  4+ (e.g. via Homebrew) gives exact line numbers.
- **`direnv`** — when direnv is active, `wherenv` reads the `DIRENV_DIFF`
  environment variable to identify which variables were loaded from the current
  `.envrc` and reports their source file. Variables set by direnv appear as
  `set by direnv` rather than `inherited`. Note: this relies on `DIRENV_DIFF`
  being present in the environment; if direnv is not active in the current
  directory no direnv-specific output is shown.
- **`mise`** — when mise is installed, `wherenv` runs `mise env --json-extended`
  once to identify variables set via `[env]` in the nearest `mise.toml`. Those
  variables appear as `set by mise` with the source file. Variables that mise
  manages but did not explicitly set (PATH, shims) are not claimed. If mise is
  not installed, not configured, or exits non-zero, `wherenv` degrades gracefully
  with no change in output.
- **Other version managers** (asdf, nix-env, etc.) are not yet traced — they
  activate through hooks outside the standard startup sequence.
- **Interactively-typed exports can't be traced** — a variable you `export` by
  hand in a session isn't part of any startup file; it may show up as
  `inherited`.
- **No cross-mode "winner of winners"** — `wherenv` reports the login and
  non-login winners separately, because which one applies depends on how your
  terminal launches shells.
- **Local, single-user** — it reads your actual dotfiles; no remote/container
  support.

---

## License

MIT — see [LICENSE](LICENSE).
