CONFIG="${XDG_CONFIG_HOME:-$HOME/.config}/opencode/opencode.json"
MODELS=''
if [ -n "$MODEL" ]; then MODELS="$(json_str "$MODEL"): {\"name\": $(json_str "$MODEL")}"; fi
if [ -n "$LIGHT" ] && [ "$LIGHT" != "$MODEL" ]; then
  if [ -n "$MODELS" ]; then MODELS="$MODELS, "; fi
  MODELS="$MODELS$(json_str "$LIGHT"): {\"name\": $(json_str "$LIGHT")}"
fi
# Every --also model joins the provider's list, which is what OpenCode's own
# model switcher offers.
add_model() {
  if [ -n "$MODELS" ]; then MODELS="$MODELS, "; fi
  MODELS="$MODELS$(json_str "$1"): {\"name\": $(json_str "$1")}"
}
for_also add_model
PATCH="{\"\$schema\": \"https://opencode.ai/config.json\", \"provider\": {\"lazu\": {\"npm\": \"@ai-sdk/openai-compatible\", \"name\": \"Lazu\", \"options\": {\"baseURL\": \"$LAZU_BASE_URL/v1\", \"apiKey\": \"$KEY\"}, \"models\": {$MODELS}}}"
if [ -n "$MODEL" ]; then PATCH="$PATCH, \"model\": $(json_str "lazu/$MODEL")"; fi
if [ -n "$LIGHT" ]; then PATCH="$PATCH, \"small_model\": $(json_str "lazu/$LIGHT")"; fi
PATCH="$PATCH}"
LAZU_DROP='provider.lazu' merge_json "$CONFIG" "$PATCH"
say "Wrote the Lazu provider into $CONFIG"

if [ -n "$MODEL" ]; then
  check_request "$MODEL" "$LAZU_BASE_URL/v1/chat/completions" \
    "{\"model\": $(json_str "$MODEL"), \"max_tokens\": 1, \"messages\": [{\"role\": \"user\", \"content\": \"ping\"}]}" \
    "authorization: Bearer $KEY" || exit $?
fi
say "Done. Start a new terminal and run: opencode"
