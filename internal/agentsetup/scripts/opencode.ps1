$configHome = if ($env:XDG_CONFIG_HOME) { $env:XDG_CONFIG_HOME } else { Join-Path $HOME '.config' }
$config = Join-Path (Join-Path $configHome 'opencode') 'opencode.json'
$models = [ordered]@{}
if ($Model) { $models[$Model] = [pscustomobject]@{ name = $Model } }
if ($Light -and $Light -ne $Model) { $models[$Light] = [pscustomobject]@{ name = $Light } }
# Every -Also model joins the provider's list, which is what OpenCode's own
# model switcher offers.
foreach ($id in (Get-AlsoModels)) { $models[$id] = [pscustomobject]@{ name = $id } }
$provider = [pscustomobject]@{
  npm = '@ai-sdk/openai-compatible'
  name = 'Lazu'
  options = [pscustomobject]@{ baseURL = "$BaseUrl/v1"; apiKey = $Key }
  models = [pscustomobject]$models
}
$patchTable = [ordered]@{ '$schema' = 'https://opencode.ai/config.json'; provider = [pscustomobject]@{ lazu = $provider } }
if ($Model) { $patchTable.model = "lazu/$Model" }
if ($Light) { $patchTable.small_model = "lazu/$Light" }
Write-JsonFile $config ([pscustomobject]$patchTable) @('provider.lazu')
Write-Host "Wrote the Lazu provider into $config"

if ($Model) {
  $body = @{ model = $Model; max_tokens = 1; messages = @(@{ role = 'user'; content = 'ping' }) } | ConvertTo-Json -Depth 5
  Test-LazuRequest $Model "$BaseUrl/v1/chat/completions" $body @{ Authorization = "Bearer $Key" }
}
Write-Host 'Done. Open a new terminal and run: opencode'
