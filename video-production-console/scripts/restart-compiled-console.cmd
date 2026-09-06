@echo off
setlocal
set "CONSOLE_PWSH=C:\Users\prepare\.cache\codex-runtimes\codex-primary-runtime\dependencies\native\powershell\pwsh.exe"
if not exist "%CONSOLE_PWSH%" (
  echo PowerShell 7 runtime is missing. No changes were made.
  pause
  exit /b 1
)
echo Restarting the local console using the newly compiled program...
"%CONSOLE_PWSH%" -NoProfile -File "%~dp0restart-compiled-console.ps1"
set "CONSOLE_RESULT=%ERRORLEVEL%"
pause
exit /b %CONSOLE_RESULT%
