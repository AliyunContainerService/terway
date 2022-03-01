$ErrorActionPreference = 'Stop'

function Log-Debug
{
  $level = Get-Env -Key "LOG_LEVEL" -DefaultValue "debug"
  if ($level -ne "debug") {
    return
  }

  Write-Host -NoNewline -ForegroundColor White "DEBU: "
  $args | ForEach-Object {
    $arg = $_
    Write-Host -ForegroundColor Gray ("{0,-44}" -f $arg)
  }
}

function Log-Info
{
  $level = Get-Env -Key "LOG_LEVEL" -DefaultValue "debug"
  if (($level -ne "debug") -and ($level -ne "info")) {
    return
  }

  Write-Host -NoNewline -ForegroundColor Blue "INFO: "
  $args |ForEach-Object {
    $arg = $_
    Write-Host -ForegroundColor Gray ("{0,-44}" -f $arg)
  }
}

function Log-Warn
{
  Write-Host -NoNewline -ForegroundColor DarkYellow "WARN: "
  $args | ForEach-Object {
    $arg = $_
    Write-Host -ForegroundColor Gray ("{0,-44}" -f $arg)
  }
}

function Log-Error
{
  Write-Host -NoNewline -ForegroundColor DarkRed "ERRO: "
  $args | ForEach-Object {
    $arg = $_
    Write-Host -ForegroundColor Gray ("{0,-44}" -f $arg)
  }
}

function Log-Fatal
{
  Write-Host -NoNewline -ForegroundColor DarkRed "FATA: "
  $args | ForEach-Object {
    $arg = $_
    Write-Host -ForegroundColor Gray ("{0,-44}" -f $arg)
  }
  throw "PANIC"
}

function Get-Env
{
  param(
    [parameter(Mandatory = $true, ValueFromPipeline = $true)] [string]$Key,
    [parameter(Mandatory = $false)] [string]$DefaultValue = ""
  )

  try {
    $val = [Environment]::GetEnvironmentVariable($Key, [EnvironmentVariableTarget]::Process)
    if ($val) {
      return $val
    }
  } catch {}
  try {
    $val = [Environment]::GetEnvironmentVariable($Key, [EnvironmentVariableTarget]::User)
    if ($val) {
      return $val
    }
  } catch {}
  try {
    $val = [Environment]::GetEnvironmentVariable($Key, [EnvironmentVariableTarget]::Machine)
    if ($val) {
      return $val
    }
  } catch {}
  return $DefaultValue
}

function ConvertTo-Hashtable
{
  param (
    [parameter(Mandatory = $true, ValueFromPipeline = $true)] [PSCustomObject]$InputObject
  )

  if ($InputObject -is [array]) {
    foreach ($item in $value) {
      $item | ConvertTo-Hashtable
    }
  }

  if ($InputObject -is [hashtable] -or $InputObject -is [System.Collections.Specialized.OrderedDictionary]) {
    return $InputObject
  }

  $hash = [ordered]@{}
  if ($InputObject -is [System.Management.Automation.PSCustomObject]) {
    foreach ($prop in $InputObject.psobject.Properties) {
      $name = $prop.Name
      $value = $prop.Value

      if ($value -is [System.Management.Automation.PSCustomObject]) {
        $value = $value | ConvertTo-Hashtable
      }

      if ($value -is [array]) {
        $hashValue = @()
        if ($value[0] -is [hashtable] -or $value[0] -is [System.Collections.Specialized.OrderedDictionary] -or $value[0] -is [PSCustomObject]) {
          foreach ($item in $value) {
              $hashValue += ($item | ConvertTo-Hashtable)
          }
        } else {
          $hashValue = $value
        }
        $value = $hashValue
      }
      $hash.Add($name,$value)
    }
  }
  return $hash
}

function Test-File
{
  param (
    [parameter(Mandatory = $true, ValueFromPipeline = $true)] [string]$Path
  )
  return Test-Path -Path $Path -PathType Leaf
}

function Test-Directory
{
  param (
    [parameter(Mandatory = $true, ValueFromPipeline = $true)] [string]$Path
  )
  return Test-Path -Path $Path -PathType Container
}

function Create-Directory
{
  param (
    [parameter(Mandatory = $true, ValueFromPipeline = $true)] [string]$Path
  )

  if (Test-Path -Path $Path) {
    if (Test-Directory -Path $Path) {
      return
    } else {
      Remove-Item -Force -Path $Path -ErrorAction Ignore | Out-Null
    }
  }
  New-Item -Force -ItemType Directory -Path $Path -ErrorAction Ignore | Out-Null
}

function Create-ParentDirectory
{
  param (
    [parameter(Mandatory = $true, ValueFromPipeline = $true)] [string]$Path
  )

  Create-Directory -Path (Split-Path -Path $Path) | Out-Null
}

function Transfer-File
{
  param (
    [parameter(Mandatory = $true, ValueFromPipeline = $true)] [string]$Src,
    [parameter(Mandatory = $true)] [string]$Dst
  )

  if (-not (Test-File -Path $Src)) {
    return
  }
  if (Test-File -Path $Dst) {
    $dstHasher = Get-FileHash -Path $Dst
    $srcHasher = Get-FileHash -Path $Src
    if ($dstHasher.Hash -eq $srcHasher.Hash) {
      return
    }
  }
  try {
    Create-ParentDirectory -Path $Dst | Out-Null
    Copy-Item -Force -Path $Src -Destination $Dst | Out-Null
  } catch {
    throw "Could not transfer file $Src to $Dst : $($_.Exception.Message)"
  }
}

function Judge
{
  param(
    [parameter(Mandatory = $true, ValueFromPipeline = $true)] [scriptBlock]$Block,
    [parameter(Mandatory = $false)] [int]$Timeout = 30,
    [parameter(Mandatory = $false)] [switch]$Reverse,
    [parameter(Mandatory = $false)] [switch]$Throw
  )
  $count = $Timeout
  while ($count -gt 0) {
    Start-Sleep -s 1
    if (&$Block) {
      if (-not $Reverse) {
        Start-Sleep -s 5
        break
      }
    } elseif ($Reverse) {
      Start-Sleep -s 5
      break
    }
    Start-Sleep -s 1
    $count -= 1
  }
  if ($count -le 0) {
    if ($Throw) {
      throw "Timeout"
    }
    Log-Fatal "Timeout"
  }
}

function Wait-Ready
{
  param(
    [parameter(Mandatory = $true)] $Path,
    [parameter(Mandatory = $false)] [int]$Timeout = 30,
    [parameter(Mandatory = $false)] [switch]$Throw
  )

  {
    Test-Path -Path $Path -ErrorAction Ignore
  } | Judge -Throw:$Throw -Timeout $Timeout
}

# transfer artifacts to host
Log-Debug "Transferring policy artifacts"
Transfer-File -Src "c:\opt\bin\calico-felix.exe" -Dst "c:\host\opt\bin\calico-felix.exe"
Transfer-File -Src "c:\etc\rules\static-rules.json" -Dst "c:\host\opt\bin\static-rules.json"
Log-Info "Transferred policy artifacts"

# condition
Log-Debug "Waiting for kubernetes configuration"
Wait-Ready -Path "c:\host\opt\bin\eni\kubeconfig.conf" -Timeout 120 -Throw
Log-Info "Waited kubernetes configuration"

# run
$env_node_name = $(Get-Env -Key "NODE_NAME")
if (-not $env_node_name) {
  Log-Fatal "Must specify NODE_NAME environment variable"
}
$env_enable_metrics = $(Get-Env -Key "ENABLE_METRICS" -DefaultValue "false")
$env_network_name_regex = $(Get-Env -Key "NETWORK_NAME_REGEX")
$env_log_level = $(Get-Env -Key "LOG_LEVEL" -DefaultValue "info")
$env_disable_policy = $(Get-Env -Key "DISABLE_POLICY" -DefaultValue "false")
$prc_path = "c:\opt\bin\calico-felix.exe"
$prc_args = @()
if (($env_disable_policy -eq "true") -or ($env_disable_policy -eq "1")) {
  # disable network policy
  $prc_args = @(
    "--cleanup"
  )
}
$prc_envs = @()
Get-ChildItem env:* | Where-Object { $_.Name -like "FELIX_*" } | ForEach-Object {
  $env_name = $_.Name
  $env_value = $_.Value
  $prc_envs += @(
    "${env_name}=${env_value}"
  )
}
$prc_envs += @(
  "USE_POD_CIDR=true"
  "KUBE_NETWORK=${env_network_name_regex}"
  "FELIX_METADATAADDR=none"
  "FELIX_FELIXHOSTNAME=${env_node_name}"
  "FELIX_LOGSEVERITYFILE=ERROR"
  "FELIX_LOGSEVERITYSYS=ERROR"
  "FELIX_LOGSEVERITYSCREEN=${env_log_level}"
  "FELIX_PROMETHEUSMETRICSENABLED=${env_enable_metrics}"
  "FELIX_DATASTORETYPE=kubernetes"
  "KUBECONFIG=c:\opt\bin\eni\kubeconfig.conf"
)
# print environment variables
$prc_envs | ForEach-Object {
  $prc_env = $_
  Log-Info "Environment ${prc_env}"
}
try {
  wins cli prc run --path="$prc_path" --args="$($prc_args -join ' ')" --envs="$($prc_envs -join ' ')"
} catch {
  Log-Fatal "Failed to execute calico-felix: $($_.Exception.Message)"
} finally {
   Remove-Item -Path "c:\host\opt\bin\static-rules.json" -Force -ErrorAction Ignore | Out-Null
}
