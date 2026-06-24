#!/usr/bin/env bash
# Integration test for wherenv against REAL direnv and mise.
#
# It scaffolds a temp project where four origins overlap, activates direnv and
# mise into this script's environment, then runs the built wherenv binary and
# asserts each variable is attributed to the correct source.
#
# Requires: go, zsh, direnv, mise. Skips (exit 0) if direnv or mise is missing.
#
# Usage:  bash test/integration.sh
set -uo pipefail

ROOT="$(cd "$(dirname "$0")/.." && pwd)"
BIN="$ROOT/bin/wherenv"

have() { command -v "$1" >/dev/null 2>&1; }

if ! have go;     then echo "SKIP: go not found";     exit 0; fi
if ! have zsh;    then echo "SKIP: zsh not found";    exit 0; fi
if ! have direnv; then echo "SKIP: direnv not found"; exit 0; fi
if ! have mise;   then echo "SKIP: mise not found";   exit 0; fi

echo "building wherenv…"
( cd "$ROOT" && go build -o "$BIN" ./cmd/wherenv ) || { echo "build failed"; exit 1; }

WORK="$(mktemp -d)"
PROJECT="$WORK/project"
ZDOT="$WORK/zdotdir"
mkdir -p "$PROJECT" "$ZDOT"

cleanup() {
  direnv deny "$PROJECT/.envrc" >/dev/null 2>&1 || true
  rm -rf "$WORK"
}
trap cleanup EXIT

# ── Controlled zsh startup (traced by wherenv via ZDOTDIR) ────────────────────
cat > "$ZDOT/.zshrc" <<'EOF'
export WHERENV__STARTUP_VAR=from-zshrc
export WHERENV__STARTUP_AND_DIRENV=zshrc-value
EOF
cat > "$ZDOT/.zprofile" <<'EOF'
export WHERENV__LOGIN_VAR=from-zprofile
EOF

# ── direnv: project .envrc ────────────────────────────────────────────────────
# NB: the var is NOT named DIRENV_*, since wherenv deliberately ignores
# DIRENV_-prefixed names (they are direnv's own metadata keys like DIRENV_FILE).
cat > "$PROJECT/.envrc" <<'EOF'
export WHERENV__ENVRC_VAR=from-direnv
export WHERENV__OVERLAP_DIRENV_MISE=direnv-wins
export WHERENV__STARTUP_AND_DIRENV=direnv-value
EOF

# ── mise: project mise.toml ───────────────────────────────────────────────────
cat > "$PROJECT/mise.toml" <<'EOF'
[env]
WHERENV__MISE_VAR = "from-mise"
WHERENV__OVERLAP_DIRENV_MISE = "mise-loses"
EOF

cd "$PROJECT"

# Activate mise, then direnv, into THIS script's environment so the variables
# are present (classify needs presence) and DIRENV_DIFF is populated.
mise trust >/dev/null 2>&1 || true
eval "$(mise env -s bash 2>/dev/null)" || true

direnv allow . >/dev/null 2>&1 || true
eval "$(direnv export bash 2>/dev/null)" || true

# A plain inherited var that no startup file or tool sets.
export WHERENV__INHERITED_VAR=just-inherited

# Trace our controlled zsh startup, not the user's real dotfiles.
export ZDOTDIR="$ZDOT"
export SHELL="$(command -v zsh)"

ALL_VARS=(WHERENV__STARTUP_VAR WHERENV__ENVRC_VAR WHERENV__MISE_VAR WHERENV__OVERLAP_DIRENV_MISE WHERENV__STARTUP_AND_DIRENV PATH WHERENV__INHERITED_VAR WHERENV__UNSET_VAR)

echo "── wherenv output ───────────────────────────────────────────"
"$BIN" --mode both "${ALL_VARS[@]}" || true
echo "─────────────────────────────────────────────────────────────"

# ── Assertions ────────────────────────────────────────────────────────────────
fails=0

check() { # check VAR "expected substring"
  local var="$1" want="$2" out
  out="$("$BIN" "$var" 2>/dev/null)"
  if grep -qF -- "$want" <<<"$out"; then
    echo "ok   $var -> $want"
  else
    echo "FAIL $var: expected to contain [$want]"
    echo "     got: $(tr '\n' '|' <<<"$out")"
    fails=$((fails+1))
  fi
}

check_not() { # check_not VAR "forbidden substring"
  local var="$1" deny="$2" out
  out="$("$BIN" "$var" 2>/dev/null)"
  if grep -qF -- "$deny" <<<"$out"; then
    echo "FAIL $var: must NOT contain [$deny]"
    echo "     got: $(tr '\n' '|' <<<"$out")"
    fails=$((fails+1))
  else
    echo "ok   $var -> not [$deny]"
  fi
}

# secrets must never leak, in any output
leak_check() {
  local out secret
  out="$("$BIN" --mode both "${ALL_VARS[@]}" 2>/dev/null)"
  out+=$'\n'"$("$BIN" --json "${ALL_VARS[@]}" 2>/dev/null)"
  for secret in from-zshrc from-direnv from-mise direnv-wins mise-loses zshrc-value direnv-value from-zprofile just-inherited; do
    if grep -qF -- "$secret" <<<"$out"; then
      echo "FAIL leak: value [$secret] appeared in output"
      fails=$((fails+1))
    fi
  done
  echo "ok   no values leaked in text or JSON output"
}

check     WHERENV__STARTUP_VAR         "set by startup"
check     WHERENV__ENVRC_VAR           "set by direnv"
check     WHERENV__MISE_VAR            "set by mise"
check     WHERENV__OVERLAP_DIRENV_MISE "set by direnv"   # direnv has priority over mise
check     WHERENV__STARTUP_AND_DIRENV  "set by startup"  # startup is never overridden by a tool
check     PATH                "set by startup"  # mise touches PATH but must not claim it
check_not PATH                "set by mise"
check     WHERENV__INHERITED_VAR       "not set by any startup file"
check     WHERENV__UNSET_VAR           "not set"
leak_check

echo "─────────────────────────────────────────────────────────────"
if [[ "$fails" -eq 0 ]]; then
  echo "PASS: all integration assertions passed"
  exit 0
fi
echo "FAILED: $fails assertion(s)"
exit 1
