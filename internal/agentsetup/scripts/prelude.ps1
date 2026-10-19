# Lazu agent setup (Windows PowerShell 5.1+ / PowerShell 7) — writes this
# machine's config for one coding agent so it talks to Lazu, then sends one
# tiny request to prove the Key works.
#
#   & ([scriptblock]::Create((irm __ORIGIN__/setup/__TOOL__.ps1))) [-Model ID]
#
# The Key is asked for at the prompt, so no copied command line carries it and
# it stays out of the PowerShell history. Unattended installs can set
# LAZU_API_KEY instead.
#
# Existing config files are backed up next to themselves before any change.
param(
  [string]$Key = '',
  [string]$Model = '',
  [Alias('Haiku', 'Small')][string]$Light = '',
  # Claude Code's /model opus and /model sonnet slots.
  [string]$Opus = '',
  [string]$Sonnet = '',
  # Extra models a list tool (OpenCode, OpenClaw) imports beside the default
  # and light ones: -Also 'a','b'.
  [string[]]$Also = @(),
  [string]$BaseUrl = '__BASE_URL__',
  [switch]$NoCheck
)
$ErrorActionPreference = 'Stop'
Set-StrictMode -Version 2

function Fail([string]$Message) { Write-Error "lazu setup: $Message"; exit 1 }

if (-not $Key) { $Key = [Environment]::GetEnvironmentVariable('LAZU_API_KEY') }
if (-not $Key) {
  # Read-Host takes the value straight from the console, never through a
  # command line PSReadLine would write to its history file.
  $secure = Read-Host -Prompt 'Paste your Lazu API Key' -AsSecureString
  $Key = [System.Net.NetworkCredential]::new('', $secure).Password
}
$Key = $Key.Trim()
if ($Key -notmatch '^[A-Za-z0-9_-]+$') { Fail 'the Key is missing or contains unexpected characters' }
# A model id is whatever the deployment's catalog holds. It is serialized for
# each config below (ConvertTo-Json, or the helpers here) rather than narrowed
# to an allowlist; only a control character, which no config format can carry,
# is refused.
if ($Model -match '\p{C}') { Fail 'the model id contains a control character' }
if ($Light -match '\p{C}') { Fail 'the light model id contains a control character' }
if ($Opus -match '\p{C}') { Fail 'the opus model id contains a control character' }
if ($Sonnet -match '\p{C}') { Fail 'the sonnet model id contains a control character' }
foreach ($id in $Also) { if ($id -match '\p{C}') { Fail 'an -Also model id contains a control character' } }

# Get-AlsoModels — the -Also ids without the default and light ones or any
# repeat, in the order given.
function Get-AlsoModels() {
  $seen = @{}
  if ($Model) { $seen[$Model] = $true }
  if ($Light) { $seen[$Light] = $true }
  $out = @()
  foreach ($id in $Also) {
    if (-not $id -or $seen.ContainsKey($id)) { continue }
    $seen[$id] = $true
    $out += $id
  }
  return ,$out
}
# Brackets are here for IPv6 literals such as https://[2001:db8::1]:8443.
if ($BaseUrl -notmatch '^https?://[][A-Za-z0-9.:/_-]+$') { Fail '-BaseUrl must be an http(s) URL' }
$BaseUrl = $BaseUrl.TrimEnd('/')

# Json-String VALUE — VALUE as a JSON string literal, quotes included. TOML
# basic strings escape the same way, so the Codex config uses it too.
function Json-String([string]$Value) { return (ConvertTo-Json -InputObject $Value -Compress) }

# Dotenv-Value VALUE — the right-hand side of a .env assignment: a plain id
# bare, anything else double-quoted and escaped the way dotenv readers unwrap.
function Dotenv-Value([string]$Value) {
  if ($Value -match '^[A-Za-z0-9._:/@-]+$') { return $Value }
  return (Json-String $Value)
}

function Backup-File([string]$Path) {
  if (Test-Path -LiteralPath $Path) {
    Copy-Item -LiteralPath $Path -Destination ("$Path.lazu-backup-" + (Get-Date -Format 'yyyyMMddHHmmss'))
  }
}

function Read-JsonFile([string]$Path) {
  if (-not (Test-Path -LiteralPath $Path)) { return [pscustomobject]@{} }
  $raw = [System.IO.File]::ReadAllText($Path).Trim()
  if (-not $raw) { return [pscustomobject]@{} }
  $doc = $null
  try { $doc = ($raw | ConvertFrom-Json) } catch { Fail "$Path is not valid JSON; fix it or move it away, then run again." }
  # Properties added to an array or a scalar are dropped when it is written
  # back out, so the merge would report success and change nothing.
  if ($doc -isnot [pscustomobject]) { Fail "$Path is not a JSON object; move it away, then run again." }
  return $doc
}

# Merge-Json OBJECT PATCH — objects merge, everything else replaces.
function Merge-Json($Target, $Patch) {
  foreach ($prop in $Patch.PSObject.Properties) {
    $existing = $Target.PSObject.Properties[$prop.Name]
    if ($prop.Value -is [pscustomobject] -and $existing -and $existing.Value -is [pscustomobject]) {
      Merge-Json $existing.Value $prop.Value | Out-Null
    } else {
      $Target | Add-Member -NotePropertyName $prop.Name -NotePropertyValue $prop.Value -Force
    }
  }
  return $Target
}

function Remove-JsonPath($Doc, [string]$Dotted) {
  $parts = $Dotted.Split('.')
  $node = $Doc
  foreach ($part in $parts[0..($parts.Length - 2)]) {
    if ($null -eq $node) { return }
    $p = $node.PSObject.Properties[$part]
    if (-not $p) { return }
    $node = $p.Value
  }
  if ($node -is [pscustomobject]) { $node.PSObject.Properties.Remove($parts[-1]) }
}

function Write-JsonFile([string]$Path, $Patch, [string[]]$Drop = @()) {
  # A relative override such as OPENCLAW_CONFIG_PATH=openclaw.json has no
  # parent to create; Split-Path hands back an empty string for it.
  $dir = Split-Path -Parent $Path
  if (-not $dir) { $dir = '.' }
  if (-not (Test-Path -LiteralPath $dir)) { New-Item -ItemType Directory -Force -Path $dir | Out-Null }
  $doc = Read-JsonFile $Path
  Backup-File $Path
  foreach ($d in $Drop) { Remove-JsonPath $doc $d }
  $doc = Merge-Json $doc $Patch
  $json = $doc | ConvertTo-Json -Depth 32
  [System.IO.File]::WriteAllText($Path, $json + "`n", (New-Object System.Text.UTF8Encoding($false)))
}

function Test-LazuRequest([string]$Label, [string]$Url, [string]$Body, [hashtable]$Headers) {
  if ($NoCheck) { Write-Host 'Skipped the test request (-NoCheck).'; return }
  try {
    Invoke-RestMethod -Method Post -Uri $Url -Headers $Headers -ContentType 'application/json' -Body $Body | Out-Null
    Write-Host "Test request OK: $Label answered through $BaseUrl."
  } catch {
    Write-Host ''
    Write-Host "The config was written, but the test request failed: $($_.Exception.Message)"
    if ($_.ErrorDetails) { Write-Host $_.ErrorDetails.Message }
    exit 4
  }
}

