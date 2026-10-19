[ -n "$MODEL" ] || die "OpenClaw needs --model"
CONFIG="${OPENCLAW_CONFIG_PATH:-$HOME/.openclaw/openclaw.json}"
MODELS="{\"id\": $(json_str "$MODEL"), \"name\": $(json_str "$MODEL")}"
# Every --also model joins the provider's list, which is what OpenClaw's own
# model switcher offers.
add_model() { MODELS="$MODELS, {\"id\": $(json_str "$1"), \"name\": $(json_str "$1")}"; }
for_also add_model
PATCH="{\"models\": {\"providers\": {\"lazu\": {\"baseUrl\": \"$LAZU_BASE_URL/v1\", \"apiKey\": \"$KEY\", \"api\": \"openai-completions\", \"models\": [$MODELS]}}}, \"agents\": {\"defaults\": {\"model\": $(json_str "lazu/$MODEL")}}}"
LAZU_DROP='models.providers.lazu' merge_json "$CONFIG" "$PATCH"
say "Wrote the Lazu provider into $CONFIG"

check_request "$MODEL" "$LAZU_BASE_URL/v1/chat/completions" \
  "{\"model\": $(json_str "$MODEL"), \"max_tokens\": 1, \"messages\": [{\"role\": \"user\", \"content\": \"ping\"}]}" \
  "authorization: Bearer $KEY" || exit $?
say "Done. Restart OpenClaw to pick up the provider."
