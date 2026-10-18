@echo off
echo [Hikkari] Unblocking files...
powershell -NoProfile -Command "Get-ChildItem -Path '%~dp0*' -Recurse | Unblock-File"
start "" "%~dp0nekkobox.exe"
