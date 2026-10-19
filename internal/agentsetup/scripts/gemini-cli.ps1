$geminiDir = Join-Path $HOME '.gemini'
if (-not (Test-Path -LiteralPath $geminiDir)) { New-Item -ItemType Directory -Force -Path $geminiDir | Out-Null }
$envFile = Join-Path $geminiDir '.env'
$lines = New-Object System.Collections.Generic.List[string]
if (Test-Path -LiteralPath $envFile) {
  Backup-File $envFile
  foreach ($line in [System.IO.File]::ReadAllLines($envFile)) {
    if ($line -match '^\s*(export\s+)?(GEMINI_API_KEY|GOOGLE_GEMINI_BASE_URL|GEMINI_MODEL)=') { continue }
    $lines.Add($line)
  }
}
$lines.Add("GEMINI_API_KEY=$Key")
$lines.Add("GOOGLE_GEMINI_BASE_URL=$BaseUrl")
if ($Model) { $lines.Add("GEMINI_MODEL=$(Dotenv-Value $Model)") }
[System.IO.File]::WriteAllText($envFile, ($lines -join "`n") + "`n", (New-Object System.Text.UTF8Encoding($false)))
Write-Host "Wrote the Lazu variables into $envFile"

# Without an explicit auth choice Gemini CLI opens the Google sign-in flow and
# ignores the base URL.
$patch = [pscustomobject]@{ security = [pscustomobject]@{ auth = [pscustomobject]@{ selectedType = 'gemini-api-key' } } }
Write-JsonFile (Join-Path $geminiDir 'settings.json') $patch

if ($Model) {
  $body = @{ contents = @(@{ parts = @(@{ text = 'ping' }) }); generationConfig = @{ maxOutputTokens = 1 } } | ConvertTo-Json -Depth 6
  $segment = [System.Uri]::EscapeDataString($Model)
  Test-LazuRequest $Model "$BaseUrl/v1beta/models/${segment}:generateContent" $body @{ 'x-goog-api-key' = $Key }
}
Write-Host 'Done. Open a new terminal and run: gemini'
