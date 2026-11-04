# Requires the Windows SDK MakeAppx tool and already-built target binaries.
param([ValidateSet('x64','arm64')][string]$Architecture='x64')
$ErrorActionPreference = 'Stop'
if (!(Get-Command MakeAppx.exe -ErrorAction SilentlyContinue)) { throw 'Install the Windows SDK packaging tools before running this command.' }
if (!(Test-Path bin/icetudio.exe)) { throw 'Build bin/icetudio.exe for the target Windows architecture first.' }
$previousGOARCH = $env:GOARCH
try {
 $env:GOARCH = if ($Architecture -eq 'x64') { 'amd64' } else { 'arm64' }
 go build -ldflags='-H windowsgui' -o bin/icetudio-launcher.exe ./cmd/icetudio-launcher
 if ($LASTEXITCODE -ne 0) { throw 'Launcher build failed.' }
} finally { $env:GOARCH = $previousGOARCH }
$stage = Join-Path ([IO.Path]::GetTempPath()) ([guid]::NewGuid().ToString())
New-Item -ItemType Directory -Path $stage | Out-Null
try {
 Copy-Item bin/icetudio.exe,bin/icetudio-launcher.exe,LICENSE $stage
 Copy-Item licenses (Join-Path $stage 'licenses') -Recurse
 New-Item -ItemType Directory -Path (Join-Path $stage 'soundfont-notices'),(Join-Path $stage 'third-party/gridsound/gs-utils') -Force | Out-Null
 Copy-Item assets/soundfonts/*.md,assets/soundfonts/*.csv (Join-Path $stage 'soundfont-notices')
 Copy-Item frontend/public/vendor/gridsound/LICENSE,frontend/public/vendor/gridsound/NOTICE.md (Join-Path $stage 'third-party/gridsound')
 Copy-Item frontend/public/vendor/gridsound/gs-utils/LICENSE (Join-Path $stage 'third-party/gridsound/gs-utils')
 if (Test-Path assets/soundfonts/MuseScore_General.sf3) {
  if ((Get-FileHash assets/soundfonts/MuseScore_General.sf3 -Algorithm SHA256).Hash.ToLower() -ne '5b85b6c2c61d10b2b91cddd41efcce7b25cd31c8271d511c73afafbef20b6fa3') { throw 'Soundfont checksum mismatch.' }
  Copy-Item assets/soundfonts/MuseScore_General.sf3 $stage
 }
 Copy-Item build/windows/msix/app_manifest.xml (Join-Path $stage 'AppxManifest.xml')
 Copy-Item build/windows/msix/Assets (Join-Path $stage 'Assets') -Recurse
 [xml]$manifest = Get-Content (Join-Path $stage 'AppxManifest.xml')
 $manifest.Package.Identity.SetAttribute('ProcessorArchitecture',$Architecture)
 $manifest.Save((Join-Path $stage 'AppxManifest.xml'))
 MakeAppx.exe pack /d $stage /p bin/IceTudio.msix /o
 if ($LASTEXITCODE -ne 0) { throw 'MSIX packaging failed.' }
} finally { Remove-Item $stage -Recurse -Force }
