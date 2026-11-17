# Speed up Go builds and cache restore on Windows runners.
#
# Windows restores the Go caches into the default locations under the user
# profile on the C: volume, and the C: volume carries the OS and the page
# file. A Dev Drive (ReFS, tuned for developer workloads) gives the caches
# their own volume: on standard windows-2025 runners the image already
# ships D: as an ReFS Dev Drive, and the larger shared runners (no D:
# volume) get one provisioned as a VHDX. Whether the placement lowers
# job time is measured, not assumed: the windows-cache-experiment lane
# compares default and redirected paths on cold builds.
#
# Environment contract, all provided by the GitHub Actions runner unless
# noted:
#   RUNNER_OS          - "Windows" gates the whole script; any other value,
#                        including unset, exits successfully doing nothing.
#   DEV_DRIVE_REDIRECT - optional override (the windows-dev-drive action
#                        passes its `redirect` input): comma-separated cache
#                        names to move onto the Dev Drive, gocache and
#                        gomodcache. Empty or unset redirects both.
#   DEV_DRIVE_ROOT     - optional override (tests pin it to a fake drive):
#                        use this drive instead of discovering or creating
#                        one. An existing Dev Drive wins over it.
#   GITHUB_ENV, GITHUB_OUTPUT - runner files receiving the exports.
#   RUNNER_TEMP        - where the backing VHDX is created when a Dev Drive
#                        must be provisioned.
#
# The script is best effort: when no Dev Drive can be provisioned the Go
# caches stay on the default volume and the job continues. The
# cache-observation record reports `go env` cache paths, so the fallback
# stays visible in telemetry.

Set-StrictMode -Version Latest
$ErrorActionPreference = "Stop"

# Stage trace: each provisioning step prints one line, so a Windows failure
# points at the failing cmdlet even when the error itself is lost.
function Write-Stage {
    param([string]$Message)
    Write-Host "[dev-drive] $Message"
}

# Cache names redirectable onto the Dev Drive, mapped to the environment
# variable Go reads for each.
$script:CacheNames = @{
    gocache    = "GOCACHE"
    gomodcache = "GOMODCACHE"
}

function Test-DevDrive {
    param([string]$Drive)
    & fsutil devdrv query $Drive *> $null
    return ($LASTEXITCODE -eq 0)
}

function New-DevDriveVhd {
    $vhdx = Join-Path $env:RUNNER_TEMP "ddtg-dev-drive.vhdx"
    Write-Stage "creating VHDX at $vhdx"
    if (Test-Path $vhdx) {
        Remove-Item -Path $vhdx -Force
    }
    New-VHD -Path $vhdx -SizeBytes 64GB -Dynamic -ErrorAction Stop | Out-Null
    Write-Stage "VHDX created; mounting"
    $disk = Mount-VHD -Path $vhdx -Passthru -ErrorAction Stop | Get-Disk -ErrorAction Stop
    Write-Stage "mounted disk $($disk.Number); initializing"
    $disk | Initialize-Disk -PartitionStyle GPT -ErrorAction Stop
    $partition = $disk | New-Partition -AssignDriveLetter -UseMaximumSize -ErrorAction Stop
    Write-Stage "partition created; formatting ReFS Dev Drive"
    $volume = $partition | Format-Volume -FileSystem ReFS -NewFileSystemLabel "DDTGDevDrive" -DevDrive -Confirm:$false -Force -ErrorAction Stop
    $drive = "$($volume.DriveLetter):"
    Write-Stage "formatted $drive; verifying Dev Drive flag"
    if (-not (Test-DevDrive $drive)) {
        throw "provisioned volume at $drive did not pass Dev Drive verification"
    }
    return $drive
}

function Get-DevDrive {
    # windows-2025 runner images already ship D: as an ReFS Dev Drive
    # (verified on 2026-11-17; see also actions/runner-images#15315), so the
    # fast path reuses it. The VHDX path below stays for images without one
    # (for example older labels) and for any future image change.
    if ((Test-Path "D:\") -and (Test-DevDrive "D:")) {
        return "D:"
    }
    return New-DevDriveVhd
}

# Compute the environment-variable redirection for the caches. Unknown
# names fail loudly: a typo would silently keep one cache on the default
# volume and defeat the point. The separator comes from the platform, so
# tests can exercise the full flow off Windows with a directory root.
function Get-RedirectEntries {
    param([string]$Drive, [string]$Redirect)
    $root = $Drive.TrimEnd('\', '/')
    $separator = [System.IO.Path]::DirectorySeparatorChar
    $entries = [ordered]@{}
    foreach ($token in ($Redirect -split ",")) {
        $name = $token.Trim()
        if ($name -eq "") {
            continue
        }
        if (-not $script:CacheNames.ContainsKey($name)) {
            throw "unknown cache name '$name' in DEV_DRIVE_REDIRECT; valid names: $(($script:CacheNames.Keys | Sort-Object) -join ', ')"
        }
        $entries[$script:CacheNames[$name]] = "$root$separator$name"
    }
    return $entries
}

Write-Stage "script start (RUNNER_OS=$($env:RUNNER_OS))"

# The runner invokes pwsh steps with -command and exits with $LASTEXITCODE
# when a native command has run. fsutil leaves a non-zero code behind when
# the probe rejects a non-Dev-Drive volume, so every completed path below
# must exit 0 explicitly; otherwise a successful run fails the step.
if ($env:RUNNER_OS -ne "Windows") {
    exit 0
}

$redirect = "gocache,gomodcache"
if (-not [string]::IsNullOrWhiteSpace($env:DEV_DRIVE_REDIRECT)) {
    $redirect = $env:DEV_DRIVE_REDIRECT
}

$drive = $null
try {
    if ($env:DEV_DRIVE_ROOT) {
        $drive = $env:DEV_DRIVE_ROOT
    } else {
        $drive = Get-DevDrive
    }
} catch {
    # Full detail first, the annotation for the job summary second: the
    # exception type and the script stack point straight at the failing
    # cmdlet when a Windows run needs diagnosis.
    Write-Host "[dev-drive] provisioning failed: $($_.Exception.GetType().FullName): $($_.Exception.Message)"
    Write-Host ($_.ScriptStackTrace)
    Write-Host "::warning::Dev Drive provisioning failed ($($_.Exception.Message)); Go caches stay on the default volume"
    $drive = $null
}

if (-not $drive) {
    exit 0
}

$entries = Get-RedirectEntries $drive $redirect
foreach ($path in $entries.Values) {
    New-Item -ItemType Directory -Path $path -Force | Out-Null
}
Write-Stage "redirected $($entries.Keys -join ', ') onto $drive"

foreach ($variable in $entries.Keys) {
    Add-Content -Path $env:GITHUB_ENV -Value "$variable=$($entries[$variable])`n" -NoNewline -Encoding utf8
}
Add-Content -Path $env:GITHUB_OUTPUT -Value "dev-drive=$drive`n" -NoNewline -Encoding utf8
Write-Stage "done"
exit 0
