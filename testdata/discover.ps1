<#
.SYNOPSIS
  PowerShell test client for MiniMedia UDP discovery (port 7360).

.EXAMPLE
  .\testdata\discover.ps1                 # broadcast to 255.255.255.255
  .\testdata\discover.ps1 -Target 127.0.0.1
#>
[CmdletBinding()]
param(
    [string]$Target = '255.255.255.255',
    [int]$TimeoutMs = 2000
)

$ErrorActionPreference = 'Stop'
$udp = New-Object System.Net.Sockets.UdpClient
try {
    $udp.EnableBroadcast = $true
    $udp.Client.ReceiveTimeout = $TimeoutMs
    $payload = [System.Text.Encoding]::ASCII.GetBytes('MINIMEDIA_DISCOVER_V1')
    [void]$udp.Send($payload, $payload.Length, $Target, 7360)

    $replies = 0
    $deadline = [DateTime]::UtcNow.AddMilliseconds($TimeoutMs)
    while ([DateTime]::UtcNow -lt $deadline) {
        $from = New-Object System.Net.IPEndPoint([System.Net.IPAddress]::Any, 0)
        try {
            $bytes = $udp.Receive([ref]$from)
        } catch [System.Net.Sockets.SocketException] {
            break   # timeout
        }
        $json = [System.Text.Encoding]::UTF8.GetString($bytes)
        $reply = $json | ConvertFrom-Json
        Write-Host ("reply from {0}: {1} at {2} (id {3}, version {4})" -f $from, $reply.name, $reply.url, $reply.serverId, $reply.version)
        $replies++
    }
    if ($replies -eq 0) { Write-Host 'no replies'; exit 1 }
} finally {
    $udp.Close()
}
