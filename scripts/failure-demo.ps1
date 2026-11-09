# Shows that no text is lost when part of the system goes down. Needs the stack running (docker compose up -d).
# Run from anywhere: powershell -ExecutionPolicy Bypass -File scripts\failure-demo.ps1
# Every outage uses a throwaway device id, so the app's own jobs are never touched.

param([string]$Gateway = 'http://localhost:8080')

Set-Location (Join-Path $PSScriptRoot '..')
$script:failures = 0

function Compose { docker compose @args 2>&1 | Out-Null }

function Headers($device) { @{ 'X-Device-Id' = $device } }

# Number of open jobs of the device, -1 while the system can't answer.
function Get-JobCount($device) {
    try { @((Invoke-RestMethod "$Gateway/jobs" -Headers (Headers $device)).jobs).Count } catch { -1 }
}

function Wait-Until([string]$what, [scriptblock]$check, [int]$seconds = 90) {
    $clock = [Diagnostics.Stopwatch]::StartNew()
    while ($clock.Elapsed.TotalSeconds -lt $seconds) {
        if (& $check) {
            Write-Host ("   ok   {0} ({1:n0}s)" -f $what, $clock.Elapsed.TotalSeconds)
            return $true
        }
        Start-Sleep -Milliseconds 500
    }
    Write-Host "   FAIL $what (gave up after ${seconds}s)"
    $script:failures++
    return $false
}

function Test-Outage([string]$title, [string[]]$kill, [scriptblock]$during) {
    Write-Host "`n== $title"
    $device = 'demo-' + [guid]::NewGuid().ToString('N').Substring(0, 12)

    Compose kill @kill
    Write-Host "   killed: $($kill -join ', ')"

    $body = '{"text":"nothing gets lost"}'
    $accepted = Wait-Until 'text accepted during the outage' {
        try {
            Invoke-RestMethod -Method Post "$Gateway/documents/text" -Headers (Headers $device) -ContentType 'application/json' -Body $body | Out-Null
            $true
        } catch { $false }
    }
    if (-not $accepted) { Compose start @kill; return }

    Start-Sleep -Seconds 3 # long enough to reach the broker
    if ((Get-JobCount $device) -gt 0) {
        Write-Host '   FAIL a job exists although the service that makes it is down'
        $script:failures++
    } else {
        Write-Host '   ok   no job yet, the message is waiting'
    }
    if ($during) { & $during }

    Compose start @kill
    Write-Host "   started: $($kill -join ', ')"
    Wait-Until 'the job appeared after recovery' { (Get-JobCount $device) -eq 1 } | Out-Null

    Start-Sleep -Seconds 3
    $count = Get-JobCount $device
    if ($count -eq 1) { Write-Host '   ok   exactly one job, no duplicates' }
    else { Write-Host "   FAIL expected 1 job, found $count"; $script:failures++ }

    # Close the demo job so it doesn't stay open.
    try {
        $job = @((Invoke-RestMethod "$Gateway/jobs" -Headers (Headers $device)).jobs)[0]
        Invoke-RestMethod -Method Post "$Gateway/jobs/$($job.jobId)/status" -Headers (Headers $device) -ContentType 'application/json' -Body '{"state":"cancelled","page":0}' | Out-Null
    } catch { Write-Host "   note could not close the demo job: $_" }
}

Test-Outage 'Layout service crashes' @('layout-service') $null
Test-Outage 'Print job service crashes' @('print-job-service') $null
Test-Outage 'Layout service down and RabbitMQ restarts (messages must survive the broker)' @('layout-service') {
    Compose restart rabbitmq
    Write-Host '   restarted: rabbitmq'
    Wait-Until 'the system answers again' { (Get-JobCount 'demo-probe') -ge 0 } | Out-Null
}

Compose start # leaves everything running, also after a failure
Write-Host ''
if ($script:failures -gt 0) { Write-Host "$script:failures check(s) failed"; exit 1 }
Write-Host 'All outages recovered: every text became exactly one job.'
