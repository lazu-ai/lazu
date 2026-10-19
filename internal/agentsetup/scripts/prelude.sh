#!/bin/sh
# Lazu agent setup — writes this machine's config for one coding agent so it
# talks to Lazu, then sends one tiny request to prove the Key works.
#
#   curl -fsSL __ORIGIN__/setup/__TOOL__ | sh -s -- [--model ID]
#
# The Key is asked for on the terminal, with the echo off: no copied command
# line carries it, so it reaches neither the process list nor the shell
# history file. Unattended installs can set LAZU_API_KEY instead, and --key -
# still reads it from stdin.
# Existing config files are backed up next to themselves before any change.
set -eu

LAZU_BASE_URL='__BASE_URL__'
LAZU_TOOL='__TOOL__'
KEY="${LAZU_API_KEY:-}"
MODEL=''
LIGHT=''
# Claude Code's /model opus and /model sonnet slots.
OPUS=''
SONNET=''
# Extra models a list tool (OpenCode, OpenClaw) imports beside the default
# and light ones, one per --also, newline separated here.
ALSO=''
SKIP_CHECK=0

say() { printf '%s\n' "$*"; }
die() { printf 'lazu setup: %s\n' "$*" >&2; exit 1; }

usage() {
  say "usage: sh -s -- [--model <id>] [--light <id>] [--opus <id>] [--sonnet <id>] [--also <id>]... [--base-url <url>] [--no-check]"
  say "the Key is asked for on the terminal; LAZU_API_KEY=<key> skips the prompt"
}

while [ $# -gt 0 ]; do
  case "$1" in
    --key) [ $# -ge 2 ] || die "--key needs a value"; KEY="$2"; shift 2 ;;
    --key=*) KEY="${1#--key=}"; shift ;;
    --model) [ $# -ge 2 ] || die "--model needs a value"; MODEL="$2"; shift 2 ;;
    --model=*) MODEL="${1#--model=}"; shift ;;
    --light|--haiku|--small) [ $# -ge 2 ] || die "$1 needs a value"; LIGHT="$2"; shift 2 ;;
    --light=*|--haiku=*|--small=*) LIGHT="${1#*=}"; shift ;;
    --opus) [ $# -ge 2 ] || die "--opus needs a value"; OPUS="$2"; shift 2 ;;
    --opus=*) OPUS="${1#--opus=}"; shift ;;
    --sonnet) [ $# -ge 2 ] || die "--sonnet needs a value"; SONNET="$2"; shift 2 ;;
    --sonnet=*) SONNET="${1#--sonnet=}"; shift ;;
    --also) [ $# -ge 2 ] || die "--also needs a value"; ALSO="$ALSO$2
"; shift 2 ;;
    --also=*) ALSO="$ALSO${1#--also=}
"; shift ;;
    --base-url) [ $# -ge 2 ] || die "--base-url needs a value"; LAZU_BASE_URL="$2"; shift 2 ;;
    --base-url=*) LAZU_BASE_URL="${1#--base-url=}"; shift ;;
    --no-check) SKIP_CHECK=1; shift ;;
    -h|--help) usage; exit 0 ;;
    *) usage >&2; die "unknown option: $1" ;;
  esac
done

if [ "$KEY" = "-" ]; then
  IFS= read -r KEY || true
fi

# Curled into sh, this script's own stdin is the pipe, so the prompt has to go
# to the terminal itself. Echo stays off while the Key is typed.
if [ -z "$KEY" ] && [ -r /dev/tty ]; then
  printf 'Paste your Lazu API Key (it will not be shown): ' > /dev/tty
  if command -v stty >/dev/null 2>&1; then
    tty_state=$(stty -g < /dev/tty 2>/dev/null || printf '')
    if [ -n "$tty_state" ]; then
      # Ctrl-C at the prompt would otherwise leave the terminal with its echo
      # off, and every command the user types after that invisible.
      trap 'stty "$tty_state" < /dev/tty 2>/dev/null || true; exit 130' INT HUP TERM
      stty -echo < /dev/tty 2>/dev/null || true
    fi
    IFS= read -r KEY < /dev/tty || true
    if [ -n "$tty_state" ]; then
      stty "$tty_state" < /dev/tty 2>/dev/null || true
      trap - INT HUP TERM
    fi
  else
    IFS= read -r KEY < /dev/tty || true
  fi
  printf '\n' > /dev/tty
fi

KEY=$(printf '%s' "$KEY" | tr -d '[:space:]')
[ -n "$KEY" ] || { usage >&2; die "no Key given"; }

# The Key is Lazu's own format, so it is held to it.
case "$KEY" in *[!A-Za-z0-9_-]*) die "the Key contains unexpected characters" ;; esac

# A model id is whatever the deployment's catalog holds — it is data, and it
# is escaped for each config below rather than narrowed here. The one thing
# nothing downstream can carry is a control character.
has_control() { [ -n "$(printf '%s' "$1" | tr -dc '[:cntrl:]')" ]; }
has_control "$MODEL" && die "the model id contains a control character"
has_control "$LIGHT" && die "the light model id contains a control character"
has_control "$OPUS" && die "the opus model id contains a control character"
has_control "$SONNET" && die "the sonnet model id contains a control character"
has_control "$(printf '%s' "$ALSO" | tr -d '\n')" && die "an --also model id contains a control character"

# for_also CALLBACK — runs CALLBACK once per --also model, skipping the
# default and light ones and any repeat, in the order given.
for_also() {
  lazu_seen="
$MODEL
$LIGHT
"
  # The split is on newlines only: globbing is off for it, or an id spelled
  # with a * or ? would expand against the current directory.
  lazu_ifs=$IFS
  case $- in *f*) lazu_glob=off ;; *) lazu_glob=on; set -f ;; esac
  IFS='
'
  for lazu_id in $ALSO; do
    IFS=$lazu_ifs
    [ -n "$lazu_id" ] || continue
    case "$lazu_seen" in *"
$lazu_id
"*) continue ;; esac
    lazu_seen="$lazu_seen$lazu_id
"
    "$1" "$lazu_id"
  done
  IFS=$lazu_ifs
  [ "$lazu_glob" = on ] && set +f
  return 0
}

case "$LAZU_BASE_URL" in
  http://*|https://*) ;;
  *) die "--base-url must start with http:// or https://" ;;
esac
# An address is not catalog data: it is held to what a URL may contain, with
# brackets for IPv6 literals such as https://[2001:db8::1]:8443. Spelled with
# tr rather than a case pattern, whose bracket expression reads differently
# from one /bin/sh to the next.
if [ -n "$(printf '%s' "$LAZU_BASE_URL" | tr -d 'A-Za-z0-9.:/_[]-')" ]; then
  die "--base-url contains unexpected characters"
fi
LAZU_BASE_URL=${LAZU_BASE_URL%/}

command -v curl >/dev/null 2>&1 || die "curl is required"

# JSON_TOOL picks whichever interpreter this machine already has, so merging a
# config never needs anything installed.
JSON_TOOL=''
if command -v node >/dev/null 2>&1; then
  JSON_TOOL=node
elif command -v python3 >/dev/null 2>&1; then
  JSON_TOOL=python3
fi

# json_str VALUE — VALUE as a JSON string literal, quotes included. TOML basic
# strings escape the same way, so the Codex config uses it too. Every model id
# below goes through this instead of being pasted between two quote marks.
json_str() {
  if [ "$JSON_TOOL" = node ]; then
    LAZU_V="$1" node -e 'process.stdout.write(JSON.stringify(process.env.LAZU_V || ""))'
  elif [ "$JSON_TOOL" = python3 ]; then
    LAZU_V="$1" python3 -c 'import json, os, sys; sys.stdout.write(json.dumps(os.environ.get("LAZU_V", "")))'
  else
    printf '"%s"' "$(printf '%s' "$1" | sed -e 's/\\/\\\\/g' -e 's/"/\\"/g')"
  fi
}

# url_seg VALUE — VALUE as one URL path segment.
url_seg() {
  if [ "$JSON_TOOL" = node ]; then
    LAZU_V="$1" node -e 'process.stdout.write(encodeURIComponent(process.env.LAZU_V || ""))'
  elif [ "$JSON_TOOL" = python3 ]; then
    LAZU_V="$1" python3 -c 'import os, sys, urllib.parse; sys.stdout.write(urllib.parse.quote(os.environ.get("LAZU_V", ""), safe=""))'
  else
    printf '%s' "$1"
  fi
}

# dotenv_value VALUE — VALUE as the right-hand side of a .env assignment. A
# plain id is written bare; anything else is double-quoted and escaped, which
# is what dotenv readers unwrap.
dotenv_value() {
  case "$1" in
    '' | *[!A-Za-z0-9._:/@-]*) json_str "$1" ;;
    *) printf '%s' "$1" ;;
  esac
}

# Every file this script backs up is one it is about to write a Key into, so
# the copy is made private too — cp keeps the source's mode, which on a
# typical rc file is 0644.
backup() {
  if [ -f "$1" ]; then
    copy="$1.lazu-backup-$(date +%Y%m%d%H%M%S)"
    cp "$1" "$copy"
    chmod 600 "$copy" 2>/dev/null || true
  fi
}

# merge_json FILE PATCH_JSON — deep-merges PATCH_JSON into FILE (objects merge,
# everything else replaces). Keys listed in LAZU_DROP (dotted paths, space
# separated) are removed first. A file that is not valid JSON is left alone.
merge_json() {
  file="$1"
  patch="$2"
  mkdir -p "$(dirname "$file")"
  if [ -z "$JSON_TOOL" ]; then
    if [ -f "$file" ]; then
      say ""
      say "Neither node nor python3 is available, so $file was not changed."
      say "Merge this into it by hand:"
      say "$patch"
      return 2
    fi
    (umask 077; printf '%s\n' "$patch" > "$file")
    return 0
  fi
  backup "$file"
  # The interpreters below only apply their mode when they create the file, so
  # an existing world-readable config keeps its old mode unless we tighten it.
  [ -f "$file" ] && chmod 600 "$file" 2>/dev/null || true
  if [ "$JSON_TOOL" = node ]; then
    LAZU_FILE="$file" LAZU_PATCH="$patch" LAZU_DROP="${LAZU_DROP:-}" node -e '
const fs = require("fs");
const file = process.env.LAZU_FILE;
let doc = {};
if (fs.existsSync(file)) {
  const raw = fs.readFileSync(file, "utf8").trim();
  if (raw) {
    try { doc = JSON.parse(raw); } catch (e) {
      console.error("lazu setup: " + file + " is not valid JSON; fix it or move it away, then run again.");
      process.exit(3);
    }
    // Properties assigned to an array or a scalar are dropped on the way back
    // out, so the merge would report success and change nothing.
    if (doc === null || typeof doc !== "object" || Array.isArray(doc)) {
      console.error("lazu setup: " + file + " is not a JSON object; move it away, then run again.");
      process.exit(3);
    }
  }
}
for (const path of (process.env.LAZU_DROP || "").split(" ").filter(Boolean)) {
  const parts = path.split(".");
  let node = doc;
  for (const part of parts.slice(0, -1)) { node = node && node[part]; }
  if (node && typeof node === "object") delete node[parts[parts.length - 1]];
}
const merge = (target, patch) => {
  for (const [k, v] of Object.entries(patch)) {
    if (v && typeof v === "object" && !Array.isArray(v) && target[k] && typeof target[k] === "object" && !Array.isArray(target[k])) {
      merge(target[k], v);
    } else {
      target[k] = v;
    }
  }
  return target;
};
merge(doc, JSON.parse(process.env.LAZU_PATCH));
fs.writeFileSync(file, JSON.stringify(doc, null, 2) + "\n", { mode: 0o600 });
'
  else
    LAZU_FILE="$file" LAZU_PATCH="$patch" LAZU_DROP="${LAZU_DROP:-}" python3 -c '
import json, os, sys
path = os.environ["LAZU_FILE"]
doc = {}
if os.path.exists(path):
    raw = open(path, encoding="utf-8").read().strip()
    if raw:
        try:
            doc = json.loads(raw)
        except ValueError:
            sys.stderr.write("lazu setup: %s is not valid JSON; fix it or move it away, then run again.\n" % path)
            sys.exit(3)
        # A list or a scalar cannot carry the keys we merge in.
        if not isinstance(doc, dict):
            sys.stderr.write("lazu setup: %s is not a JSON object; move it away, then run again.\n" % path)
            sys.exit(3)
for dotted in filter(None, os.environ.get("LAZU_DROP", "").split(" ")):
    parts = dotted.split(".")
    node = doc
    for part in parts[:-1]:
        node = node.get(part) if isinstance(node, dict) else None
    if isinstance(node, dict):
        node.pop(parts[-1], None)
def merge(target, patch):
    for k, v in patch.items():
        if isinstance(v, dict) and isinstance(target.get(k), dict):
            merge(target[k], v)
        else:
            target[k] = v
    return target
merge(doc, json.loads(os.environ["LAZU_PATCH"]))
fd = os.open(path, os.O_WRONLY | os.O_CREAT | os.O_TRUNC, 0o600)
with os.fdopen(fd, "w", encoding="utf-8") as out:
    json.dump(doc, out, indent=2, ensure_ascii=False)
    out.write("\n")
'
  fi
}

# check_request LABEL URL BODY HEADER... — one small real request through the
# same protocol the agent will use. Prints Lazu's error body when it fails.
#
# The headers hold the Key, so they are handed to curl through a config on its
# stdin: an argument would be readable in `ps` by every account on the machine
# for as long as the request takes.
check_request() {
  label="$1"; url="$2"; body="$3"; shift 3
  if [ "$SKIP_CHECK" = 1 ]; then
    say "Skipped the test request (--no-check)."
    return 0
  fi
  tmp=$(mktemp 2>/dev/null || printf '/tmp/lazu-setup-%s' "$$")
  headers=''
  for header in "$@"; do
    headers="${headers}header = \"${header}\"
"
  done
  code=$(printf '%s' "$headers" | curl --config - \
    -sS -o "$tmp" -w '%{http_code}' -X POST "$url" \
    -H 'content-type: application/json' --data "$body" 2>/dev/null || printf '000')
  if [ "$code" -ge 200 ] 2>/dev/null && [ "$code" -lt 300 ]; then
    say "Test request OK: $label answered through $LAZU_BASE_URL."
    rm -f "$tmp"
    return 0
  fi
  say ""
  say "The config was written, but the test request failed (HTTP $code):"
  cat "$tmp" 2>/dev/null || true
  say ""
  rm -f "$tmp"
  return 4
}

