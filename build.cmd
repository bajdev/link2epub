@echo off
rem Builds link2epub.exe in the repository root.
rem
rem Run it from Command Prompt or PowerShell:
rem     build.cmd          (Command Prompt)
rem     .\build.cmd        (PowerShell)
rem
rem Needs Go 1.24 or newer: https://go.dev/dl/
setlocal

rem Work from the folder this script is in, wherever it was started from.
cd /d "%~dp0"

where go >nul 2>nul
if errorlevel 1 (
    echo error: Go was not found. Install Go 1.24 or newer from https://go.dev/dl/
    echo and open a new terminal so the installer's PATH change takes effect.
    exit /b 1
)

rem A pure-Go Windows build for this machine's processor. -trimpath and
rem -s -w leave out local paths and debug symbols, making the file smaller.
set CGO_ENABLED=0
set GOOS=windows

echo Building link2epub.exe...
go build -trimpath -ldflags="-s -w" -o link2epub.exe .\cmd\link2epub
if errorlevel 1 (
    echo error: the build failed; see the messages above.
    exit /b 1
)

echo Built %CD%\link2epub.exe
echo.
echo Example:
echo     link2epub.exe -URL https://example.com/article -SAVE %%USERPROFILE%%\Books\
endlocal
