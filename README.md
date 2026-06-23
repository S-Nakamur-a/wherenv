# wherenv

**Which startup file set this environment variable?**

You open a new shell and `PATH` has some entry you don't recognize. Or `EDITOR`
is `nano` and you have no idea which dotfile is to blame. `wherenv` answers the
question by tracing what your shell *actually does* at startup:

```console
$ wherenv -v PATH
PATH: set by startup  (3 places, most recent first)

  → /Users/you/.config/zsh/conf.d/07-tools.zsh:39   ← ran last
      export PATH=/Users/you/.nodenv/shims:/opt/homebrew/bin:…
    /Users/you/.orbstack/shell/init.zsh:1
      export PATH=/usr/local/bin:…
    /etc/zprofile:12
      PATH=/usr/local/bin:…
```

Assignments are listed like a stack trace — most recent on top, with the line
that **ran last** marked. `wherenv` reports *where* and *in what order*, which it
knows for certain; it doesn't guess which assignment "wins" (with `path=($path …)`
arrays and `$VAR`-expansion that can't be decided from the trace alone). Use
`-v` to see the values and judge precedence yourself.

It works with **zsh** and **bash**, follows `source`d files, and tells you when
a variable was inherited from your terminal/login session rather than set by any
dotfile.

**Values are hidden by default** — so you can run `wherenv AWS_SECRET_ACCESS_KEY`
without spilling a secret onto your screen. The locations (file:line) are always
shown; pass `-v` to reveal the values, as above.

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
wherenv [flags] VARNAME [VARNAME...]
```

```sh
wherenv PATH                  # where is PATH built up? (values hidden)
wherenv -v PATH               # ...and show the values
wherenv PATH GOPATH EDITOR    # several at once
wherenv --json PATH           # machine-readable output
wherenv --full-value PATH     # show full, untruncated values
```

### Flags

| Flag | Default | Description |
|------|---------|-------------|
| `-v`, `--show-value` | off | Reveal variable values (truncated at 120 chars). Hidden by default so secrets aren't printed. |
| `--full-value` | off | Reveal full, untruncated values (implies `-v`). |
| `--mode` | `login` | Shell mode(s) to trace: `login`, `non-login`, or `both`. |
| `--color` | `auto` | Colorize output: `auto` (TTY only, respects `NO_COLOR`), `always`, `never`. |
| `--json` | off | Emit JSON instead of human-readable text. |
| `--timeout` | 8.0 | Per-spawn timeout in seconds (each traced mode gets this budget). |

**Why `--mode` defaults to `login`:** on zsh a login shell also sources the
non-login files (`.zshrc`), so login is a superset and matches how macOS
terminals start — one fast trace covers everything. Use `--mode both` to see how
login and non-login differ (each assignment is then tagged with the mode it ran
in). On **bash**, login does *not* read `.bashrc` unless your `.bash_profile`
sources it — use `--mode both` or `--mode non-login` if your settings live in
`.bashrc`.

Values are **hidden by default**: assignments display as `export FOO=<hidden>`
and only the location is shown. This makes it safe to inspect secret-bearing
variables. Use `-v` when you need to see what each assignment actually set —
handy when a variable is overwritten several times and you want the winning
value.

### Exit codes

| Code | Meaning |
|------|---------|
| 0 | Ran successfully (including "not set"). |
| 1 | Internal error while formatting output. |
| 2 | Bad usage: no variable given, or an invalid variable name. |

Variable names must match `^[A-Za-z_][A-Za-z0-9_]*$`. This is validated up front
and is also what keeps your input from ever reaching a shell.

---

## How it works

`wherenv` spawns your shell (`$SHELL`) twice — once non-login, once login — with
`set -x` (xtrace) enabled and a unique, random marker baked into `PS4` so it can
recognize its own trace lines. It parses that trace to find where each requested
variable was assigned, recording the file and line for every assignment.

- **Source-following**: because the trace reports the *actual* file being
  executed, assignments inside `source`d scripts are attributed to that script,
  not to the file that sourced it.
- **Login vs non-login**: the two modes read different files (e.g. `.zprofile`
  is login-only). Each result is tagged with the mode(s) it appeared in rather
  than blended together.
- **Tool-set variables**: after tracing startup files, `wherenv` probes
  developer tools that set environment variables through hooks rather than
  startup files. Currently supported: **direnv** — the `DIRENV_DIFF` variable
  is decoded to identify which variables were loaded from the current `.envrc`,
  and their source file is reported. More tools (mise, etc.) may be added in
  future versions.
- **Inherited variables**: if nothing in startup or a known tool assigns a
  variable but it exists in your environment, it was inherited from the parent
  process (terminal, `launchd`, login session). On macOS, `launchctl getenv`
  is probed to identify a system-level source.

There's a deeper write-up of the mechanism and design trade-offs if you're
curious about the internals — see the project's design notes.

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

### A variable set during startup (default — values hidden)

```
PATH: set by startup  (3 places, most recent first)

  → /Users/you/.zshrc:12   ← ran last
      PATH=<hidden>
    /opt/homebrew/etc/profile.d/x.sh:2
      export PATH=<hidden>
    /etc/zprofile:12
      PATH=<hidden>
```

The assignments are listed like a stack trace — most recent on top, the
last-executed one marked `← ran last`. This is purely about *order*, not
precedence: `wherenv` won't claim which assignment "wins", because cumulative
forms like `export PATH=$PATH:…` and `path=($path …)` mean earlier lines often
still contribute. Values are hidden by default; add `-v` to reveal them and see
how the value was built up.

With `--mode both`, each assignment is also tagged with the mode it ran in
(`[login]`, `[non-login]`, or `[non-login+login]`), since which files run depends
on whether your session is a login shell.

### Output streams

`wherenv` follows the usual convention: the **findings go to stdout**, while the
spinner, warnings, the "values hidden" hint, and `WHERENV_DEBUG` logs go to
**stderr**. So `wherenv FOO > out.txt` captures just the result, and
`wherenv FOO 2>/dev/null` silences the progress noise.

### A variable not set by any startup file

```
TERM_PROGRAM: present in the environment, not set by any startup file
  → inherited from the parent process, or exported interactively / by a tool (these can't be traced)
```

`wherenv` only claims what it can prove: the variable is in your environment but
no traced startup file sets it. It doesn't assert "inherited from the parent",
because the same observation also covers a value you `export`ed by hand or one a
runtime hook set. When a macOS `launchctl` session value is found, that concrete
source is shown instead.

### An unset variable

```
MY_VAR: not set
```

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
- **`mise` and other version managers are not traced** — they activate through
  hooks outside the standard startup sequence.
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
