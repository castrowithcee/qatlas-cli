# Qatlas installer for Windows (Windows PowerShell 5.1 and PowerShell 7, 64-bit).
#
#   irm https://github.com/castrowithcee/qatlas-cli/releases/latest/download/install.ps1 | iex
#   $env:QATLAS_VERSION = "v1.2.3-rc.1"; irm https://github.com/castrowithcee/qatlas-cli/releases/latest/download/install.ps1 | iex
#
# Installs the newest stable release (or the version in QATLAS_VERSION, including pre-release tags such as
# v1.2.3-rc.1) into %LOCALAPPDATA%\Programs\qatlas; running it again updates the installation.
# The archive is installed only after the SHA-256 listed in checksums.txt matches and, when ssh-keygen is
# available (the OpenSSH client of Windows 10/11), checksums.txt carries a valid signature of the release key
# embedded below. Any mismatch aborts before the installation is touched. The script needs no administrator
# rights, never touches ~\.qatlas\cli, and adds <prefix>\bin to the user PATH only if it is missing.
#
# Environment (all optional; the last three exist for tests and mirrors):
#   QATLAS_VERSION                       release tag to install instead of the latest stable release
#   QATLAS_INSTALL_BASE_URL              releases URL; only changes where files are downloaded from
#                                        (default https://github.com/castrowithcee/qatlas-cli/releases)
#   QATLAS_INSTALL_PREFIX                installation prefix (default %LOCALAPPDATA%\Programs\qatlas)
#   QATLAS_INSTALL_ALLOWED_SIGNERS_FILE  TEST ONLY: allowed-signers file used instead of the embedded key.
#                                        It replaces only the key; the signature is still required.
#   QATLAS_INSTALL_USER_PATH_FILE        TEST ONLY: file holding the raw (unexpanded) user PATH instead of the
#                                        registry value HKCU\Environment\Path, so tests never change the real one.

function Install-Qatlas {
    $ErrorActionPreference = 'Stop'
    $ProgressPreference = 'SilentlyContinue'

    $repoUrl = 'https://github.com/castrowithcee/qatlas-cli/releases'
    $signerLine = 'qatlas-release ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIJ6euNyMyw+0rV5TVSa24o374+wDn75ueHvdYAGZnEv5'
    $tmp = $null
    $stage = $null

    try {
        if ($env:OS -ne 'Windows_NT') {
            throw 'this installer supports Windows only (use install.sh on Linux, macOS and WSL)'
        }
        $arch = $env:PROCESSOR_ARCHITEW6432
        if (-not $arch) { $arch = $env:PROCESSOR_ARCHITECTURE }
        if ($arch -ne 'AMD64') {
            throw "unsupported architecture $arch; supported: amd64"
        }
        try {
            [Net.ServicePointManager]::SecurityProtocol = [Net.ServicePointManager]::SecurityProtocol -bor [Net.SecurityProtocolType]::Tls12
        } catch {
            # Not fatal: newer runtimes negotiate TLS themselves.
        }

        $base = $env:QATLAS_INSTALL_BASE_URL
        if (-not $base) { $base = $repoUrl }
        $base = $base.TrimEnd('/')
        $prefix = $env:QATLAS_INSTALL_PREFIX
        if (-not $prefix) {
            if (-not $env:LOCALAPPDATA) { throw 'LOCALAPPDATA is not set' }
            $prefix = Join-Path $env:LOCALAPPDATA 'Programs\qatlas'
        }
        $prefix = $prefix.TrimEnd('\', '/')
        $version = $env:QATLAS_VERSION

        if (-not $version) {
            try {
                $request = [Net.WebRequest]::Create("$base/latest")
                $request.UserAgent = 'qatlas-install'
                $response = $request.GetResponse()
                $latest = $response.ResponseUri.AbsoluteUri
                $response.Close()
            } catch {
                throw 'could not determine the latest release'
            }
            $version = $latest.TrimEnd('/').Split('/')[-1]
        }
        if ($version -cnotmatch '^v(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)(-[0-9A-Za-z-]+(\.[0-9A-Za-z-]+)*)?$') {
            throw "invalid version '$version'; expected a tag such as v1.2.3 or v1.2.3-rc.1"
        }

        $archive = "qatlas_${version}_windows_amd64.zip"
        $tmp = Join-Path ([IO.Path]::GetTempPath()) ('qatlas-install.' + [Guid]::NewGuid().ToString('N'))
        [void](New-Item -ItemType Directory -Path $tmp)

        Write-Host "Downloading Qatlas $version for windows/amd64"
        foreach ($name in @($archive, 'checksums.txt', 'checksums.txt.sig')) {
            try {
                Invoke-WebRequest -UseBasicParsing -Uri "$base/download/$version/$name" -OutFile (Join-Path $tmp $name)
            } catch {
                throw "download of $name failed"
            }
        }

        $sshKeygen = Get-Command ssh-keygen -CommandType Application -ErrorAction SilentlyContinue | Select-Object -First 1
        if ($sshKeygen) {
            $signers = $env:QATLAS_INSTALL_ALLOWED_SIGNERS_FILE
            if (-not $signers) {
                $signers = Join-Path $tmp 'allowed-signers'
                [IO.File]::WriteAllText($signers, $signerLine + "`n", (New-Object Text.UTF8Encoding($false)))
            }
            $verifyArgs = '-Y verify -f "{0}" -I qatlas-release -n qatlas-release -s "{1}"' -f $signers, (Join-Path $tmp 'checksums.txt.sig')
            $verify = Start-Process -FilePath $sshKeygen.Source -ArgumentList $verifyArgs -NoNewWindow -Wait -PassThru `
                -RedirectStandardInput (Join-Path $tmp 'checksums.txt') `
                -RedirectStandardOutput (Join-Path $tmp 'verify.out') -RedirectStandardError (Join-Path $tmp 'verify.err')
            if ($verify.ExitCode -ne 0) {
                throw 'signature of checksums.txt is invalid; nothing was installed'
            }
        } else {
            Write-Warning 'qatlas-install: ssh-keygen not found: the release signature was NOT verified, only the SHA-256 checksum'
        }

        $want = $null
        foreach ($line in [IO.File]::ReadAllLines((Join-Path $tmp 'checksums.txt'))) {
            $fields = $line.Trim() -split '\s+'
            if ($fields.Count -ge 2 -and $fields[1].TrimStart('*') -ceq $archive) {
                $want = $fields[0].ToLowerInvariant()
                break
            }
        }
        if (-not $want) { throw "checksums.txt does not list $archive; nothing was installed" }
        $got = (Get-FileHash -Algorithm SHA256 -LiteralPath (Join-Path $tmp $archive)).Hash.ToLowerInvariant()
        if ($got -ne $want) { throw "SHA-256 mismatch for $archive; nothing was installed" }

        [void](New-Item -ItemType Directory -Force -Path $prefix)
        $stage = Join-Path $prefix ('.qatlas-stage.' + [Guid]::NewGuid().ToString('N'))
        [void](New-Item -ItemType Directory -Path $stage)
        try {
            Add-Type -AssemblyName System.IO.Compression.FileSystem
            [IO.Compression.ZipFile]::ExtractToDirectory((Join-Path $tmp $archive), $stage)
        } catch {
            throw 'could not extract the archive'
        }
        $newExe = Join-Path $stage 'bin\qatlas.exe'
        $item = Get-Item -LiteralPath $newExe -Force -ErrorAction SilentlyContinue
        if (-not $item -or $item.PSIsContainer -or ($item.Attributes -band [IO.FileAttributes]::ReparsePoint)) {
            throw 'archive does not contain bin\qatlas.exe'
        }

        $bindir = Join-Path $prefix 'bin'
        $docdir = Join-Path $prefix 'share\doc\qatlas'
        [void](New-Item -ItemType Directory -Force -Path $bindir, $docdir)
        $stagedLicense = Join-Path $stage 'share\doc\qatlas\LICENSE'
        if (Test-Path -LiteralPath $stagedLicense -PathType Leaf) {
            Move-Item -LiteralPath $stagedLicense -Destination (Join-Path $docdir 'LICENSE') -Force
        }

        # A running qatlas.exe cannot be overwritten but can be renamed; the program is replaced last.
        $exe = Join-Path $bindir 'qatlas.exe'
        $old = $exe + '.old'
        $renamed = $false
        if (Test-Path -LiteralPath $exe) {
            if (Test-Path -LiteralPath $old) {
                try { Remove-Item -LiteralPath $old -Force } catch { }
            }
            Move-Item -LiteralPath $exe -Destination $old -Force
            $renamed = $true
        }
        try {
            Move-Item -LiteralPath $newExe -Destination $exe
        } catch {
            if ($renamed) { Move-Item -LiteralPath $old -Destination $exe -Force }
            throw "could not install $exe"
        }

        # User PATH: add <prefix>\bin only if missing. The raw (unexpanded) value is read and written back as
        # REG_EXPAND_SZ, so existing %VAR% entries survive; the session gets the directory too.
        $pathChanged = $false
        $pathFile = $env:QATLAS_INSTALL_USER_PATH_FILE
        if ($pathFile) {
            $userPath = ''
            if (Test-Path -LiteralPath $pathFile) { $userPath = [IO.File]::ReadAllText($pathFile).Trim() }
        } else {
            $userPath = [string](Get-Item -LiteralPath 'HKCU:\Environment').GetValue('Path', '', 'DoNotExpandEnvironmentNames')
        }
        $hasDir = {
            param([string]$list, [string]$dir)
            foreach ($entry in ([string]$list -split ';')) {
                $expanded = [Environment]::ExpandEnvironmentVariables($entry.Trim())
                if ($expanded.TrimEnd('\') -ieq $dir.TrimEnd('\')) { return $true }
            }
            return $false
        }
        if (-not (& $hasDir $userPath $bindir)) {
            if ($userPath) { $newPath = $userPath.TrimEnd(';') + ';' + $bindir } else { $newPath = $bindir }
            if ($pathFile) {
                [IO.File]::WriteAllText($pathFile, $newPath, (New-Object Text.UTF8Encoding($false)))
            } else {
                [void](New-ItemProperty -LiteralPath 'HKCU:\Environment' -Name 'Path' -Value $newPath -PropertyType ExpandString -Force)
                # Tell running programs (Explorer) about the change without rewriting PATH through
                # SetEnvironmentVariable, which would store REG_SZ. A failure here is not fatal.
                try {
                    if (-not ('QatlasInstall.NativeMethods' -as [type])) {
                        Add-Type -Namespace QatlasInstall -Name NativeMethods -MemberDefinition '[DllImport("user32.dll", SetLastError = true, CharSet = CharSet.Unicode)] public static extern IntPtr SendMessageTimeout(IntPtr hWnd, uint Msg, UIntPtr wParam, string lParam, uint fuFlags, uint uTimeout, out UIntPtr lpdwResult);'
                    }
                    $result = [UIntPtr]::Zero
                    [void][QatlasInstall.NativeMethods]::SendMessageTimeout([IntPtr]0xffff, 0x1A, [UIntPtr]::Zero, 'Environment', 2, 5000, [ref]$result)
                } catch {
                    Write-Warning 'qatlas-install: could not notify running programs about the PATH change; open a new terminal or sign in again'
                }
            }
            $pathChanged = $true
        }
        if (-not (& $hasDir $env:Path $bindir)) {
            $env:Path = $bindir + ';' + $env:Path
        }

        Write-Host ''
        Write-Host "Installed Qatlas $version"
        Write-Host "  program: $exe"
        if ($pathChanged) {
            Write-Host "  PATH:    $bindir was added to your user PATH; open a new terminal to use ``qatlas``."
        }
        Write-Host ''
        Write-Host 'Next: run `qatlas tui` (press c to connect a provider) or `qatlas web`.'
    } catch {
        Write-Error ("qatlas-install: " + $_.Exception.Message)
        return
    } finally {
        if ($tmp -and (Test-Path -LiteralPath $tmp)) { Remove-Item -LiteralPath $tmp -Recurse -Force -ErrorAction SilentlyContinue }
        if ($stage -and (Test-Path -LiteralPath $stage)) { Remove-Item -LiteralPath $stage -Recurse -Force -ErrorAction SilentlyContinue }
    }
}

Install-Qatlas
