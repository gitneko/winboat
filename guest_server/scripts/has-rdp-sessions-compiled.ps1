# WinBoat Guest Server
# Instead of querying the locale dependent quser.exe,
# we use directly the Win32 API through a compiled module.
# This is a more robust approach than parsing quser.exe
# and more importantly locale independent!

# The C# code is automatically compiled to a DLL on first use.
# csc.exe (C# Compiler) is the official command-line compiler bundled with the Microsoft .NET Framework.

# The function will query Win32 WTS for sessions using a compiled module.
# A compiled module is the fastest way - instead of using the Win32 WTS API in PowerShell.
# The functions filter by state (active) and type (RDP)
# and returns a boolean if there is at least one session.
# If there are any sessions a simple 1 will be printed,
# otherwise a 0 will be printed.

# Use path where the script resides (scripts folder of the guest server)
# and be independent of the current working directory
$scriptpath = Split-Path $MyInvocation.MyCommand.Path

# Compiled first if not already compiled
if (!(Test-Path "$scriptpath\WtsSessionChecker.dll")) {
    # Find the csc.exe first
    $csc = (Get-ChildItem "C:\Windows\Microsoft.NET\Framework*" -Recurse -Filter csc.exe | Sort-Object { [version]($_.Directory.Name -replace 'v','') } -Descending | Select-Object -First 1).FullName

    # Compile the C# module into a compiled module
    &$csc /target:library /out:"$scriptpath\WtsSessionChecker.dll" "$scriptpath\WtsSessionChecker.cs" | Out-Null
}

Add-Type -Path "$scriptpath\WtsSessionChecker.dll"

if ([WtsSessionChecker]::HasActiveRdpSession()) {'1'} else {'0'}
