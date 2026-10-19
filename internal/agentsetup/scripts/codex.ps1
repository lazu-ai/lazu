if (-not $Model) { Fail 'Codex needs -Model' }
$codexDir = if ($env:CODEX_HOME) { $env:CODEX_HOME } else { Join-Path $HOME '.codex' }
if (-not (Test-Path -LiteralPath $codexDir)) { New-Item -ItemType Directory -Force -Path $codexDir | Out-Null }
$config = Join-Path $codexDir 'config.toml'
# With -Also models, Codex gets a model catalog of its own: the file behind
# /model, so the picker lists exactly the models this Key connects, each with
# the reasoning levels it takes. Without -Also the bundled catalog stays and
# the default model rides on the model key alone, as before.
#
# The entries are cloned from the installed Codex's own bundled catalog
# (`codex debug models --bundled`), so the file carries whatever fields — and
# whatever base prompt — this Codex version requires; a hand-written entry
# would fail to parse on the next schema change and stop Codex from starting.
$catalog = Join-Path $codexDir 'lazu-models.json'
$also = Get-AlsoModels
$catalogWritten = $false
if ($also.Count -gt 0) {
  $codexCmd = Get-Command codex -ErrorAction SilentlyContinue
  $bundled = $null
  if ($codexCmd) {
    try { $bundled = (& codex debug models --bundled 2>$null | Out-String | ConvertFrom-Json) } catch { $bundled = $null }
  }
  $template = $null
  if ($bundled -and $bundled.models) {
    $template = @($bundled.models | Where-Object { $_.visibility -eq 'list' })[0]
    if (-not $template) { $template = @($bundled.models)[0] }
  }
  if (-not $codexCmd) {
    Write-Host 'codex is not on PATH, so the extra models were not written; install Codex and run this again.'
  } elseif (-not $template) {
    Write-Host 'codex debug models --bundled gave nothing, so the extra models were not written.'
  } else {
    $deep = @(
      [pscustomobject]@{ effort = 'minimal'; description = 'Fastest, little reasoning' },
      [pscustomobject]@{ effort = 'low'; description = 'Fast responses with lighter reasoning' },
      [pscustomobject]@{ effort = 'medium'; description = 'Balances speed and reasoning depth' },
      [pscustomobject]@{ effort = 'high'; description = 'Greater reasoning depth for complex problems' },
      [pscustomobject]@{ effort = 'xhigh'; description = 'Extra high reasoning depth for the hardest problems' }
    )
    $plain = $deep[1..3]
    $entries = @()
    $n = 0
    foreach ($id in (@($Model) + $also)) {
      $n += 1
      $openai = $id -match '^(gpt|o[0-9]|chatgpt|codex)'
      $vision = $id -match '^(gpt|o[0-9]|chatgpt|codex|claude|gemini)'
      $entry = $template.PSObject.Copy()
      $set = @{
        slug = $id; display_name = $id; description = 'via Lazu'
        default_reasoning_level = 'medium'
        supported_reasoning_levels = $(if ($openai) { $deep } else { $plain })
        default_reasoning_summary = $(if ($openai) { 'auto' } else { 'none' })
        visibility = 'list'; supported_in_api = $true; priority = $n
        input_modalities = $(if ($vision) { @('text', 'image') } else { @('text') })
        upgrade = $null; availability_nux = $null
        # OpenAI-only transports and tiers stay off for a gateway model.
        service_tiers = @(); additional_speed_tiers = @(); default_service_tier = $null
        prefer_websockets = $false; use_responses_lite = $false; supports_experimental_context = $false
      }
      foreach ($k in $set.Keys) { $entry | Add-Member -NotePropertyName $k -NotePropertyValue $set[$k] -Force }
      foreach ($k in @('tool_mode', 'multi_agent_version', 'multi_agent_reasoning_effort')) { $entry.PSObject.Properties.Remove($k) }
      $entries += $entry
    }
    Backup-File $catalog
    $json = [pscustomobject]@{ models = $entries } | ConvertTo-Json -Depth 32
    [System.IO.File]::WriteAllText($catalog, $json + "`n", (New-Object System.Text.UTF8Encoding($false)))
    $catalogWritten = $true
  }
}
$kept = New-Object System.Collections.Generic.List[string]
if (Test-Path -LiteralPath $config) {
  Backup-File $config
  $inTable = $false; $skip = $false
  foreach ($line in [System.IO.File]::ReadAllLines($config)) {
    # Ours is [model_providers.lazu] and every table nested under it; a
    # surviving [model_providers.lazu.http_headers] would attach a stale
    # Authorization header to the provider written below.
    if ($line -match '^\s*\[') { $inTable = $true; $skip = ($line -match '^\s*\[\[?model_providers\.lazu[\].]') }
    if (-not $inTable -and $line -match '^\s*(model|model_provider)\s*=') { continue }
    # A model_catalog_json line goes when it points at our own catalog or when
    # this run writes a new one; a catalog the user keeps elsewhere stays.
    if (-not $inTable -and $line -match '^\s*model_catalog_json\s*=' -and ($catalogWritten -or $line -match 'lazu-models\.json')) { continue }
    if ($skip) { continue }
    $kept.Add($line)
  }
}
$head = @('model_provider = "lazu"', "model = $(Json-String $Model)")
if ($catalogWritten) { $head += "model_catalog_json = $(Json-String $catalog)" }
$out = $head + @('') + $kept + @('', '[model_providers.lazu]', 'name = "Lazu"', "base_url = `"$BaseUrl/v1`"", 'env_key = "LAZU_API_KEY"', 'wire_api = "responses"')
[System.IO.File]::WriteAllText($config, ($out -join "`n") + "`n", (New-Object System.Text.UTF8Encoding($false)))
Write-Host "Wrote the Lazu provider into $config"
if ($catalogWritten) {
  Write-Host "Wrote the model catalog into $catalog (Codex lists these models under /model)"
} elseif ($also.Count -eq 0 -and (Test-Path -LiteralPath $catalog)) {
  Remove-Item -LiteralPath $catalog
}

# Codex reads the Key only from the environment variable named by env_key.
[Environment]::SetEnvironmentVariable('LAZU_API_KEY', $Key, 'User')
$env:LAZU_API_KEY = $Key
Write-Host 'Saved LAZU_API_KEY as a user environment variable.'

$body = @{ model = $Model; input = 'ping'; max_output_tokens = 16 } | ConvertTo-Json
Test-LazuRequest $Model "$BaseUrl/v1/responses" $body @{ Authorization = "Bearer $Key" }
Write-Host 'Done. Open a new terminal and run: codex'
