# Backs up the database and proves the backup can be restored. Keeps the newest 14 in backups\ (git-ignored).
# The documents in it are encrypted: keep DOCUMENT_ENCRYPTION_KEY somewhere else, safe. Without it a backup is useless.
# Run from anywhere: powershell -ExecutionPolicy Bypass -File scripts\backup.ps1
#
# To restore a backup over the current data (this replaces what is there):
#   docker compose cp backups\<file> postgres:/tmp/restore.dump
#   docker compose exec -T postgres sh -c 'pg_restore -U "$POSTGRES_USER" -d braillebar --clean --if-exists /tmp/restore.dump'

Set-Location (Join-Path $PSScriptRoot '..')
New-Item -ItemType Directory -Force backups | Out-Null
$file = "backups\braillebar-$(Get-Date -Format 'yyyyMMdd-HHmmss').dump"

# Restores into a throwaway database and compares a row count with the real one.
$script = @'
set -e
export U="$POSTGRES_USER"
pg_dump -U "$U" -Fc braillebar > /tmp/backup.dump
dropdb -U "$U" --if-exists restore_check
createdb -U "$U" restore_check
pg_restore -U "$U" -d restore_check /tmp/backup.dump
a=$(psql -U "$U" -Atc 'select count(*) from document.document' braillebar)
b=$(psql -U "$U" -Atc 'select count(*) from document.document' restore_check)
dropdb -U "$U" restore_check
echo "documents: live $a, restored $b"
[ "$a" = "$b" ]
'@

# Sent as base64: piping it in makes Windows PowerShell put a BOM before "set -e", which breaks that line.
$encoded = [Convert]::ToBase64String([Text.Encoding]::ASCII.GetBytes(($script -replace "`r", '')))
docker compose exec -T postgres sh -c "echo $encoded | base64 -d | sh"
if ($LASTEXITCODE -ne 0) { Write-Host 'Backup FAILED, the restore check did not pass'; exit 1 }

docker compose cp postgres:/tmp/backup.dump $file 2>&1 | Out-Null
if ($LASTEXITCODE -ne 0) { Write-Host 'Backup FAILED, could not copy the file'; exit 1 }
docker compose exec -T postgres rm /tmp/backup.dump

Get-ChildItem backups\*.dump | Sort-Object Name -Descending | Select-Object -Skip 14 | Remove-Item
Write-Host "Backup ok: $file ($([math]::Round((Get-Item $file).Length / 1KB)) KB)"
