[ -n "$MODEL" ] || die "Codex needs --model"
CODEX_DIR="${CODEX_HOME:-$HOME/.codex}"
CONFIG="$CODEX_DIR/config.toml"
mkdir -p "$CODEX_DIR"
backup "$CONFIG"

# Drop the top-level model/model_provider keys and any previous
# [model_providers.lazu] table, keep everything else, then write ours.
# The rewrite keeps every other provider's table, so this file may hold
# somebody else's credentials as well as pointing at ours. Both the scratch
# copy and the file it replaces stay owner-only; a plain redirect would create
# the copy 0644 under the usual umask and mv would carry that mode across.
# With --also models, Codex gets a model catalog of its own: the file behind
# /model, so the picker lists exactly the models this Key connects, each with
# the reasoning levels it takes. Without --also the bundled catalog stays and
# the default model rides on the model key alone, as before.
#
# The entries are cloned from the installed Codex's own bundled catalog
# (`codex debug models --bundled`), so the file carries whatever fields — and
# whatever base prompt — this Codex version requires; a hand-written entry
# would fail to parse on the next schema change and stop Codex from starting.
CATALOG="$CODEX_DIR/lazu-models.json"
CATALOG_WRITTEN=0
build_catalog() {
  if ! command -v codex >/dev/null 2>&1; then
    say "codex is not on PATH, so the extra models were not written; install Codex and run this again."
    return 1
  fi
  if [ -z "$JSON_TOOL" ]; then
    say "Neither node nor python3 is available, so the extra models were not written."
    return 1
  fi
  bundled=$(codex debug models --bundled 2>/dev/null) || bundled=''
  if [ -z "$bundled" ]; then
    say "codex debug models --bundled gave nothing, so the extra models were not written."
    return 1
  fi
  backup "$CATALOG"
  if [ "$JSON_TOOL" = node ]; then
    LAZU_BUNDLED="$bundled" LAZU_MODELS="$MODEL
$ALSO" LAZU_FILE="$CATALOG" node -e '
const fs = require("fs");
const bundled = JSON.parse(process.env.LAZU_BUNDLED);
const list = [...new Set(process.env.LAZU_MODELS.split("\n").filter(Boolean))];
const models = Array.isArray(bundled.models) ? bundled.models : [];
const template = models.find((m) => m.visibility === "list") || models[0];
if (!template) { console.error("lazu setup: the Codex catalog has no entry to copy from"); process.exit(5); }
const openai = (id) => /^(gpt|o\d|chatgpt|codex)/i.test(id);
const vision = (id) => /^(gpt|o\d|chatgpt|codex|claude|gemini)/i.test(id);
const level = (effort, description) => ({ effort, description });
const deep = [
  level("minimal", "Fastest, little reasoning"),
  level("low", "Fast responses with lighter reasoning"),
  level("medium", "Balances speed and reasoning depth"),
  level("high", "Greater reasoning depth for complex problems"),
  level("xhigh", "Extra high reasoning depth for the hardest problems"),
];
const plain = deep.slice(1, 4);
const out = list.map((id, index) => {
  const entry = {
    ...template,
    slug: id,
    display_name: id,
    description: "via Lazu",
    default_reasoning_level: "medium",
    supported_reasoning_levels: openai(id) ? deep : plain,
    default_reasoning_summary: openai(id) ? "auto" : "none",
    visibility: "list",
    supported_in_api: true,
    priority: index + 1,
    input_modalities: vision(id) ? ["text", "image"] : ["text"],
    upgrade: null,
    availability_nux: null,
    // OpenAI-only transports and tiers stay off for a gateway model.
    service_tiers: [],
    additional_speed_tiers: [],
    default_service_tier: null,
    prefer_websockets: false,
    use_responses_lite: false,
    supports_experimental_context: false,
  };
  delete entry.tool_mode;
  delete entry.multi_agent_version;
  delete entry.multi_agent_reasoning_effort;
  return entry;
});
fs.writeFileSync(process.env.LAZU_FILE, JSON.stringify({ models: out }, null, 2) + "\n", { mode: 0o600 });
' || return 1
  else
    LAZU_BUNDLED="$bundled" LAZU_MODELS="$MODEL
$ALSO" LAZU_FILE="$CATALOG" python3 -c '
import json, os, re, sys
bundled = json.loads(os.environ["LAZU_BUNDLED"])
seen = []
for item in os.environ["LAZU_MODELS"].split("\n"):
    if item and item not in seen:
        seen.append(item)
models = bundled.get("models") or []
template = next((m for m in models if m.get("visibility") == "list"), models[0] if models else None)
if template is None:
    sys.stderr.write("lazu setup: the Codex catalog has no entry to copy from\n"); sys.exit(5)
openai = lambda i: re.match(r"^(gpt|o\d|chatgpt|codex)", i, re.I) is not None
vision = lambda i: re.match(r"^(gpt|o\d|chatgpt|codex|claude|gemini)", i, re.I) is not None
deep = [
    {"effort": "minimal", "description": "Fastest, little reasoning"},
    {"effort": "low", "description": "Fast responses with lighter reasoning"},
    {"effort": "medium", "description": "Balances speed and reasoning depth"},
    {"effort": "high", "description": "Greater reasoning depth for complex problems"},
    {"effort": "xhigh", "description": "Extra high reasoning depth for the hardest problems"},
]
plain = deep[1:4]
out = []
for index, model in enumerate(seen):
    entry = dict(template)
    entry.update({
        "slug": model, "display_name": model, "description": "via Lazu",
        "default_reasoning_level": "medium",
        "supported_reasoning_levels": deep if openai(model) else plain,
        "default_reasoning_summary": "auto" if openai(model) else "none",
        "visibility": "list", "supported_in_api": True, "priority": index + 1,
        "input_modalities": ["text", "image"] if vision(model) else ["text"],
        "upgrade": None, "availability_nux": None,
        "service_tiers": [], "additional_speed_tiers": [], "default_service_tier": None,
        "prefer_websockets": False, "use_responses_lite": False, "supports_experimental_context": False,
    })
    for key in ("tool_mode", "multi_agent_version", "multi_agent_reasoning_effort"):
        entry.pop(key, None)
    out.append(entry)
fd = os.open(os.environ["LAZU_FILE"], os.O_WRONLY | os.O_CREAT | os.O_TRUNC, 0o600)
with os.fdopen(fd, "w") as fh:
    json.dump({"models": out}, fh, indent=2); fh.write("\n")
' || return 1
  fi
  chmod 600 "$CATALOG" 2>/dev/null || true
  CATALOG_WRITTEN=1
  return 0
}
if [ -n "$ALSO" ]; then
  build_catalog || true
fi

tmp="$CONFIG.lazu-tmp"
umask 077
{
  printf 'model_provider = "lazu"\nmodel = %s\n' "$(json_str "$MODEL")"
  if [ "$CATALOG_WRITTEN" = 1 ]; then printf 'model_catalog_json = %s\n' "$(json_str "$CATALOG")"; fi
  printf '\n'
  if [ -f "$CONFIG" ]; then
    # A table header ends the previous table. Ours is [model_providers.lazu]
    # and everything nested under it ([model_providers.lazu.http_headers], say)
    # — a nested table left behind would attach a stale Authorization header
    # to the provider written below. A model_catalog_json line goes when it
    # points at our own catalog (stale, or about to be rewritten above) or
    # when this run writes a new one; a catalog the user keeps elsewhere
    # stays, and the extra models are then left out rather than replacing it.
    awk -v ours="$CATALOG_WRITTEN" '
      /^[ \t]*\[/ { in_table = 1; skip = ($0 ~ /^[ \t]*\[\[?model_providers\.lazu[].]/) }
      !in_table && /^[ \t]*(model|model_provider)[ \t]*=/ { next }
      !in_table && /^[ \t]*model_catalog_json[ \t]*=/ && (ours == 1 || $0 ~ /lazu-models\.json/) { next }
      skip { next }
      { print }
    ' "$CONFIG"
  fi
  printf '\n[model_providers.lazu]\nname = "Lazu"\nbase_url = "%s/v1"\nenv_key = "LAZU_API_KEY"\nwire_api = "responses"\n' "$LAZU_BASE_URL"
} > "$tmp"
mv "$tmp" "$CONFIG"
chmod 600 "$CONFIG" 2>/dev/null || true
say "Wrote the Lazu provider into $CONFIG"
if [ "$CATALOG_WRITTEN" = 1 ]; then
  say "Wrote the model catalog into $CATALOG (Codex lists these models under /model)"
elif [ -z "$ALSO" ]; then
  rm -f "$CATALOG"
fi

# Codex reads the Key only from the environment variable named by env_key.
#
# Which file a new terminal reads depends on how it starts the shell. zsh and
# fish read the same file either way, but bash reads .bashrc when it is
# interactive and .bash_profile / .bash_login / .profile when it is a login
# shell — an ssh session, or a macOS terminal profile — and only sources
# .bashrc from there if the user set that up. So bash gets both, or the
# installer would report success on a machine where Codex never sees the Key.
shell_name=$(basename "${SHELL:-sh}")
LINE="export LAZU_API_KEY='$KEY' # lazu"

# persist_key FILE — put the managed line in FILE, replacing an older one.
# The file becomes a secret the moment the Key lands in it, so it is written
# in place (a mv would replace the user's file with our scratch copy, mode and
# all) and both it and the scratch copy end up owner-only. FILE is passed as
# one quoted argument throughout: a $HOME with a space in it is still one path.
persist_key() {
  rc="$1"
  mkdir -p "$(dirname "$rc")"
  touch "$rc"
  backup "$rc"
  (umask 077; grep -v 'LAZU_API_KEY.*# lazu$' "$rc" > "$rc.lazu-tmp" || true)
  printf '%s\n' "$LINE" >> "$rc.lazu-tmp"
  chmod 600 "$rc" 2>/dev/null || true
  cat "$rc.lazu-tmp" > "$rc"
  rm -f "$rc.lazu-tmp"
  say "Added LAZU_API_KEY to $rc (now owner-only, since it holds a Key)"
}

case "$shell_name" in
  zsh) persist_key "$HOME/.zshrc" ;;
  bash)
    persist_key "$HOME/.bashrc"
    if [ -f "$HOME/.bash_profile" ]; then
      persist_key "$HOME/.bash_profile"
    elif [ -f "$HOME/.bash_login" ]; then
      persist_key "$HOME/.bash_login"
    else
      persist_key "$HOME/.profile"
    fi
    ;;
  fish)
    LINE="set -gx LAZU_API_KEY '$KEY' # lazu"
    persist_key "$HOME/.config/fish/config.fish"
    ;;
  *) persist_key "$HOME/.profile" ;;
esac

check_request "$MODEL" "$LAZU_BASE_URL/v1/responses" \
  "{\"model\": $(json_str "$MODEL"), \"input\": \"ping\", \"max_output_tokens\": 16}" \
  "authorization: Bearer $KEY" || exit $?
say "Done. Open a new terminal and run: codex"
