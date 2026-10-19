SETTINGS="${CLAUDE_CONFIG_DIR:-$HOME/.claude}/settings.json"

ENV_JSON="\"ANTHROPIC_BASE_URL\": \"$LAZU_BASE_URL\", \"ANTHROPIC_AUTH_TOKEN\": \"$KEY\", \"CLAUDE_CODE_DISABLE_EXPERIMENTAL_BETAS\": \"1\", \"CLAUDE_CODE_ATTRIBUTION_HEADER\": \"0\""
if [ -n "$MODEL" ]; then ENV_JSON="$ENV_JSON, \"ANTHROPIC_MODEL\": $(json_str "$MODEL")"; fi
# The three aliases behind /model opus, sonnet and haiku, so the agent can
# switch between them without editing this file.
if [ -n "$OPUS" ]; then ENV_JSON="$ENV_JSON, \"ANTHROPIC_DEFAULT_OPUS_MODEL\": $(json_str "$OPUS")"; fi
if [ -n "$SONNET" ]; then ENV_JSON="$ENV_JSON, \"ANTHROPIC_DEFAULT_SONNET_MODEL\": $(json_str "$SONNET")"; fi
if [ -n "$LIGHT" ]; then ENV_JSON="$ENV_JSON, \"ANTHROPIC_DEFAULT_HAIKU_MODEL\": $(json_str "$LIGHT")"; fi

# An ANTHROPIC_API_KEY left in the same env block would be sent instead of the
# Lazu Key, so the managed block drops it.
LAZU_DROP='env.ANTHROPIC_API_KEY' merge_json "$SETTINGS" "{\"env\": {$ENV_JSON}}"
say "Wrote the Lazu settings into $SETTINGS"

if [ -n "$MODEL" ]; then
  check_request "$MODEL" "$LAZU_BASE_URL/v1/messages" \
    "{\"model\": $(json_str "$MODEL"), \"max_tokens\": 1, \"messages\": [{\"role\": \"user\", \"content\": \"ping\"}]}" \
    "x-api-key: $KEY" 'anthropic-version: 2023-06-01' || exit $?
fi
say "Done. Start a new terminal and run: claude"
