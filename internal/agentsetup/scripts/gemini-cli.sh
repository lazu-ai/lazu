GEMINI_DIR="$HOME/.gemini"
ENV_FILE="$GEMINI_DIR/.env"
mkdir -p "$GEMINI_DIR"
touch "$ENV_FILE"
backup "$ENV_FILE"
(umask 077; grep -v -E '^(export[[:space:]]+)?(GEMINI_API_KEY|GOOGLE_GEMINI_BASE_URL|GEMINI_MODEL)=' "$ENV_FILE" > "$ENV_FILE.lazu-tmp" || true)
{
  printf 'GEMINI_API_KEY=%s\n' "$KEY"
  printf 'GOOGLE_GEMINI_BASE_URL=%s\n' "$LAZU_BASE_URL"
  if [ -n "$MODEL" ]; then printf 'GEMINI_MODEL=%s\n' "$(dotenv_value "$MODEL")"; fi
} >> "$ENV_FILE.lazu-tmp"
mv "$ENV_FILE.lazu-tmp" "$ENV_FILE"
chmod 600 "$ENV_FILE"
say "Wrote the Lazu variables into $ENV_FILE"

# Without an explicit auth choice Gemini CLI opens the Google sign-in flow and
# ignores the base URL — so a failed merge is a failed install, not a warning.
merge_json "$GEMINI_DIR/settings.json" '{"security": {"auth": {"selectedType": "gemini-api-key"}}}' || merge_status=$?
case "${merge_status:-0}" in
  0) ;;
  2) say "Add that block to $GEMINI_DIR/settings.json, or Gemini CLI will ask you to sign in with Google instead." ;;
  *) die "could not write $GEMINI_DIR/settings.json; Gemini CLI would ignore the Lazu base URL" ;;
esac

if [ -n "$MODEL" ]; then
  check_request "$MODEL" "$LAZU_BASE_URL/v1beta/models/$(url_seg "$MODEL"):generateContent" \
    '{"contents": [{"parts": [{"text": "ping"}]}], "generationConfig": {"maxOutputTokens": 1}}' \
    "x-goog-api-key: $KEY" || exit $?
fi
say "Done. Start a new terminal and run: gemini"
