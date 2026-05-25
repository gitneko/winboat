# WinBoat Guest Server
# Optimizes powershell assemblies and thus
# makes running PS Commands faster (up to 10 times)
# This script is run automatically at guest server boot

# Original script from
# https://simeononsecurity.com/guides/boost-powershell-performance-with-ngen/#how-to-use-ngen-with-powershell
function Optimize-PowershellAssemblies {
    $old_path = $env:path

    try {
        $env:path = [Runtime.InteropServices.RuntimeEnvironment]::GetRuntimeDirectory()
        [AppDomain]::CurrentDomain.GetAssemblies() | % {
            if (! $_.location) {continue}

            $Name = Split-Path $_.location -leaf
            if ($Name.startswith("Microsoft.PowerShell.")) {
                Write-Progress -Activity "Native Image Installation" -Status "$name"
                ngen install $_.location | % {"`t$_"}
            }
        }
    } finally {
        $env:path = $old_path
    }
}

Optimize-PowershellAssemblies
