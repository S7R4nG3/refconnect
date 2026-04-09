# RefConnect installer for Windows
# Usage: iwr -useb https://raw.githubusercontent.com/S7R4nG3/refconnect/main/configs/setup.ps1 | iex

$ErrorActionPreference = "Stop"

$Repo = "S7R4nG3/refconnect"
$Api  = "https://api.github.com/repos/$Repo/releases/latest"

# Detect architecture
$Arch = switch ($env:PROCESSOR_ARCHITECTURE) {
    "AMD64" { "amd64" }
    "ARM64" { "arm64" }
    default {
        Write-Error "Unsupported architecture: $($env:PROCESSOR_ARCHITECTURE)"
        exit 1
    }
}

# Fetch latest release metadata
Write-Host "Fetching latest release info..."
$Release = Invoke-RestMethod -Uri $Api -UseBasicParsing
$Tag     = $Release.tag_name
Write-Host "Latest release: $Tag"

# Asset naming convention: refconnect_windows_<arch>.zip
$Asset    = "refconnect_windows_$Arch.zip"
$AssetUrl = ($Release.assets | Where-Object { $_.name -eq $Asset } | Select-Object -First 1).browser_download_url

if (-not $AssetUrl) {
    Write-Error "No asset found for $Asset in release $Tag."
    exit 1
}

# Download
$Dest = Join-Path (Get-Location) $Asset
Write-Host "Downloading $Asset..."
Invoke-WebRequest -Uri $AssetUrl -OutFile $Dest -UseBasicParsing

# Extract to current directory
Write-Host "Extracting..."
Expand-Archive -Path $Dest -DestinationPath (Get-Location) -Force
Remove-Item $Dest

Write-Host "Done. RefConnect $Tag installed in the current directory."
