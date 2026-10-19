if (-not $Model) { Fail 'OpenClaw needs -Model' }
$config = if ($env:OPENCLAW_CONFIG_PATH) { $env:OPENCLAW_CONFIG_PATH } else { Join-Path (Join-Path $HOME '.openclaw') 'openclaw.json' }
$provider = [pscustomobject]@{
  baseUrl = "$BaseUrl/v1"
  apiKey = $Key
  api = 'openai-completions'
  models = @([pscustomobject]@{ id = $Model; name = $Model })
}
# Every -Also model joins the provider's list, which is what OpenClaw's own
# model switcher offers.
foreach ($id in (Get-AlsoModels)) { $provider.models += [pscustomobject]@{ id = $id; name = $id } }
$patch = [pscustomobject]@{
  models = [pscustomobject]@{ providers = [pscustomobject]@{ lazu = $provider } }
  agents = [pscustomobject]@{ defaults = [pscustomobject]@{ model = "lazu/$Model" } }
}
Write-JsonFile $config $patch @('models.providers.lazu')
Write-Host "Wrote the Lazu provider into $config"

$body = @{ model = $Model; max_tokens = 1; messages = @(@{ role = 'user'; content = 'ping' }) } | ConvertTo-Json -Depth 5
Test-LazuRequest $Model "$BaseUrl/v1/chat/completions" $body @{ Authorization = "Bearer $Key" }
Write-Host 'Done. Restart OpenClaw to pick up the provider.'
