@echo off
cd /d "%~dp0"
echo === MelodyV3 Go Build ===
set PATH=%PATH%;C:\Program Files\Go\bin
set GOPROXY=https://goproxy.cn,direct
echo === Generate Icon Resource ===
rsrc -ico icon.ico -o rsrc_windows.syso
go build -ldflags="-H windowsgui -s -w" -o dist\MelodyV3.exe .
if %errorlevel% equ 0 (
  copy /Y dist\MelodyV3.exe MelodyV3.exe
  echo === Build OK ===
) else (
  echo === Build FAILED ===
)
