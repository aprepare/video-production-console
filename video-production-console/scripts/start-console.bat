@echo off
chcp 65001 >nul
cd /d "%~dp0"
set "PATH=%~dp0;%PATH%"
if not defined VIDEO_CONSOLE_INITIAL_PASSWORD set "VIDEO_CONSOLE_INITIAL_PASSWORD=123456"

echo.
echo 正在检查并启动视频生产控制台...
echo 请不要关闭这个黑窗口。
echo 浏览器地址: http://127.0.0.1:2030
echo 首次登录密码: 123456 （登录后请立刻改掉）
echo.

if exist "%~dp0repair-startup.exe" (
  "%~dp0repair-startup.exe"
)

start "" cmd /c "timeout /t 2 /nobreak >nul && start http://127.0.0.1:2030"
"%~dp0video-production-console.exe" >> "%~dp0console-startup.log" 2>&1
set "ERR=%ERRORLEVEL%"
if not "%ERR%"=="0" (
  echo.
  echo 启动失败。请把同目录的 console-startup.log 发给开发者。
  if exist "%~dp0console-startup.log" type "%~dp0console-startup.log"
  pause
)
