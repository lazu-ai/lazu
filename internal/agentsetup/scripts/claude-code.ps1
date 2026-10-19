$configDir = if ($env:CLAUDE_CONFIG_DIR) { $env:CLAUDE_CONFIG_DIR } else { Join-Path $HOME '.claude' }
$settings = Join-Path $configDir 'settings.json'
$envBlock = [ordered]@{
  ANTHROPIC_BASE_URL = $BaseUrl
  ANTHROPIC_AUTH_TOKEN = $Key
  CLAUDE_CODE_DISABLE_EXPERIMENTAL_BETAS = '1'
  CLAUDE_CODE_ATTRIBUTION_HEADER = '0'
}
if ($Model) { $envBlock.ANTHROPIC_MODEL = $Model }
# The three aliases behind /model opus, sonnet and haiku, so the agent can
# switch between them without editing this file.
if ($Opus) { $envBlock.ANTHROPIC_DEFAULT_OPUS_MODEL = $Opus }
if ($Sonnet) { $envBlock.ANTHROPIC_DEFAULT_SONNET_MODEL = $Sonnet }
if ($Light) { $envBlock.ANTHROPIC_DEFAULT_HAIKU_MODEL = $Light }
$patch = [pscustomobject]@{ env = [pscustomobject]$envBlock }
Write-JsonFile $settings $patch @('env.ANTHROPIC_API_KEY')
Write-Host "Wrote the Lazu settings into $settings"

if ($Model) {
  $body = @{ model = $Model; max_tokens = 1; messages = @(@{ role = 'user'; content = 'ping' }) } | ConvertTo-Json -Depth 5
  Test-LazuRequest $Model "$BaseUrl/v1/messages" $body @{ 'x-api-key' = $Key; 'anthropic-version' = '2023-06-01' }
}
Write-Host 'Done. Open a new terminal and run: claude'
