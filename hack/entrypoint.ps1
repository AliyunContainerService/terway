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

function ConvertTo-DecimalIP
{
  param(
    [parameter(Mandatory = $true)] [Net.IPAddress] $Address
  )

  $addr = 0; $i = 3
  $ip.GetAddressBytes() | % {
    $addr += $_ * [Math]::Pow(256, $i); $i--
  }
  return [UInt32]$addr
}

function Format-CIDR
{
  param(
    [parameter(Mandatory = $true)] [string]$Raw
  )

  $raw_part = $Raw -split "/"
  if ($raw_part.Length -ne 2) {
    throw "$Raw is an invalid CIDR address"
  }

  $ip = [Net.IPAddress]($raw_part[0])
  $mask = [UInt32]'0xffffffff'
  $mask = $mask -shl (32 - [int]($raw_part[1]))
  $subnet_address = $(ConvertTo-DecimalIP -Address $ip) -band $mask
  $subnet_address_dotted = $(for ($i = 3; $i -gt -1; $i--) {
    $remanider = $subnet_address % [Math]::Pow(256, $i)
    ($subnet_address - $remanider) / [Math]::Pow(256, $i)
    $subnet_address = $remanider
  })
  return "{0}/{1}" -f ($subnet_address_dotted -join "."),$raw_part[1]
}

function Get-Metadata
{
  param(
    [parameter(Mandatory = $true, ValueFromPipeline = $true)] [string]$Key
  )

  $resp = @()
  $maximum_retry_count = 3
  $maximum_redirection = 2
  $retry_interval_sec = 2
  if ($PSVersionTable.PSVersion.Major -ge 7) {
    $resp = Invoke-WebRequest -Uri "100.100.100.200/latest/meta-data/$Key" -UseBasicParsing -MaximumRedirection $maximum_redirection -MaximumRetryCount $maximum_retry_count -RetryIntervalSec $retry_interval_sec -ErrorAction Ignore
  } else {
    while ($maximum_retry_count -gt 0) {
      $resp =  @{}
      try {
        $resp = Invoke-WebRequest -Uri "100.100.100.200/latest/meta-data/$Key" -UseBasicParsing -MaximumRedirection $maximum_redirection -ErrorAction Ignore
      } catch {}
      if ($resp.StatusCode -and ($resp.StatusCode -lt 400)) {
        break
      }
      Start-Sleep -Seconds $retry_interval_sec
      $maximum_retry_count = $maximum_retry_count - 1
    }
    if ($maximum_retry_count -le 0) {
      throw "Failed to get metadata $Key as timeout"
    }
  }
  if ($resp.StatusCode -ne 200) {
    throw "Failed to get metadata $Key as response `n$($resp.RawContent)"
  }
  return $resp.Content
}

# transfer artifacts to host
Log-Debug "Transferring eni artifacts"
Transfer-File -Src "c:\opt\bin\terwayd.exe" -Dst "c:\host\opt\bin\terwayd.exe"
Transfer-File -Src "c:\opt\cni\bin\terway.exe" -Dst "c:\host\opt\cni\bin\terway.exe"
Transfer-File -Src "c:\opt\cni\bin\host-local.exe" -Dst "c:\host\opt\cni\bin\host-local.exe"
Log-Info "Transferred eni artifacts"

# transfer eni config to host
Log-Debug "Transferring eni configuration"
$eni_config = Get-Content -Path "c:\etc\eni\eni.json" | ConvertFrom-Json | ConvertTo-Hashtable
# NB(thxCode): mutate the credential path to the dynamic volume redirecting destination.
$eni_config["credential_path"] = "/var/addon-terway/token-config"
$eni_config_conf = $eni_config | ConvertTo-Json -Compress -Depth 32
$eni_config_conf | Out-File -NoNewline -Encoding ascii -Force -FilePath "c:\host\etc\eni\eni.json"
Transfer-File -Src "c:\etc\eni\10-terway.conf" -Dst "c:\host\etc\eni\10-terway.conf"
Log-Info "Transferred eni configuration"

# generated cni config to host
Log-Debug "Generating cni configuration"
$cni_config = @{
  cniVersion = "0.4.0"
  name = "terway"
  type = "terway"
}
if (Test-File -Path "c:\etc\eni\10-terway.conf") {
  $cni_config = Get-Content -Path "c:\etc\eni\10-terway.conf" | ConvertFrom-Json | ConvertTo-Hashtable
}
# version
if (-not $cni_config["cniVersion"]) {
  $cni_config["cniVersion"] = "0.4.0"
}
# name
if (-not $cni_config["name"]) {
  $cni_config["name"] = "terway"
}
# type
if (-not $cni_config["type"]) {
  $cni_config["type"] = "terway"
}
# capabilities
if (-not $cni_config["capabilities"]) {
  $cni_config["capabilities"] = @{
    dns = $true
    portMappings = $true
  }
}
$cni_config_conf = $cni_config | ConvertTo-Json -Compress -Depth 32
$cni_config_conf | Out-File -NoNewline -Encoding ascii -Force -FilePath "c:\host\etc\cni\net.d\10-terway.conf"
Log-Info "Generated cni configuration: $cni_config_conf"

# mount
Log-Debug "Mounting critical resources"
$env_node_name = $(Get-Env -Key "NODE_NAME")
$env_pod_namespace = $(Get-Env -Key "POD_NAMESPACE")
$env_pod_name = $(Get-Env -Key "POD_NAME")
$env_container_name = $(Get-Env -Key "CONTAINER_NAME")
$vol_selectors = @(
  "io.kubernetes.pod.namespace=${env_pod_namespace}"
  "io.kubernetes.pod.name=${env_pod_name}"
  "io.kubernetes.container.name=${env_container_name}"
)
$vol_paths = @(
  "c:\var\run\secrets\kubernetes.io\serviceaccount:c:\opt\bin\eni\serviceaccount"
  "c:\var\addon:c:\var\addon-terway"
)
try {
  wins cli vol mount --selectors="$($vol_selectors -join ' ')" --paths="$($vol_paths -join ' ')"
} catch {
  Log-Fatal "Failed to mount volume: $($_.Exception.Message)"
}
Log-Info "Mounted critical resources"

# generate kubeconfig
Log-Debug "Generating kubernetes configuration"
Create-Directory -Path "c:\host\opt\bin\eni"
$env_cluster_server = Get-Env -Key "CLUSTER_SERVER"
if (-not $env_cluster_server) {
  Log-Fatal "CLUSTER_SERVER environment variable is blank"
}
"apiVersion: v1
kind: Config
clusters:
- cluster:
    certificate-authority: c:/opt/bin/eni/serviceaccount/ca.crt
    server: ${env_cluster_server}
  name: default
contexts:
- context:
    cluster: default
    namespace: default
    user: default
  name: default
current-context: default
users:
- name: default
  user:
    tokenFile: c:/opt/bin/eni/serviceaccount/token" | Out-File -NoNewline -Encoding ascii -Force -FilePath "c:\host\opt\bin\eni\kubeconfig.conf"
Log-Info "Generated kubernetes configuration"

# run
$env_log_level = $(Get-Env -Key "LOG_LEVEL" -DefaultValue "info")
$env_backend_type = $(Get-Env -Key "BACKEND_TYPE" -DefaultValue "VPC")
$env_readonly_listen = $(Get-Env -Key "DEBUG_SOCKET" -DefaultValue "unix:///var/run/eni/eni_debug.socket")
$prc_path = "c:\opt\bin\terwayd.exe"
$prc_args = @(
  "--kubeconfig=c:\opt\bin\eni\kubeconfig.conf"
  "--log-level=${env_log_level}"
  "--daemon-mode=${env_backend_type}"
  "--readonly-listen=${env_readonly_listen}"
)
$prc_envs = @(
  "NODE_NAME=${env_node_name}"
  "POD_NAMESPACE=${env_pod_namespace}"
)
try {
  wins cli prc run --path="$prc_path" --args="$($prc_args -join ' ')" --envs="$($prc_envs -join ' ')"
} catch {
  Log-Fatal "Failed to execute terway: $($_.Exception.Message)"
} finally {
  Remove-Item -Path "c:\host\opt\bin\eni\kubeconfig.conf" -Force -ErrorAction Ignore | Out-Null
  Remove-Item -Path "c:\host\etc\cni\net.d\10-terway.conf" -Force -ErrorAction Ignore | Out-Null
}
