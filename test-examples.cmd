@echo off
rem Builds link2epub.exe, then converts a set of real articles into
rem test-examples\ for checking on the e-readers. Books from a previous run
rem are replaced (-FORCE). A failure on one URL doesn't stop the others.
rem
rem Run it from Command Prompt or PowerShell:
rem     test-examples.cmd          (Command Prompt)
rem     .\test-examples.cmd        (PowerShell)
setlocal

rem Work from the folder this script is in, wherever it was started from.
cd /d "%~dp0"

call build.cmd || exit /b 1
echo.

if not exist test-examples mkdir test-examples

set /a ok=0, failed=0
for %%U in (
    "https://craphound.com/news/2026/09/27/llms-are-real-ai-is-fake/"
    "https://arstechnica.com/gadgets/2026/10/kde-turns-30-with-plasma-6-8-remote-upgrades-and-an-x11-farewell/"
    "https://www.bbc.com/news/articles/c5ygyww0zk3o"
    "https://en.wikipedia.org/wiki/BattleTech"
    "https://www.wired.com/story/breadfruit-caribbean-pacific-climate-change-super-food/"
) do call :convert %%U

echo %ok% converted, %failed% failed. Books are in %CD%\test-examples\
if %failed% gtr 0 exit /b 1
exit /b 0

rem Converts one URL and counts the result. The -SAVE folder is left
rem unquoted: a quoted path ending in a backslash would swallow the quote.
:convert
echo === %~1
link2epub.exe -URL "%~1" -SAVE test-examples\ -FORCE
if errorlevel 1 (
    set /a failed+=1
) else (
    set /a ok+=1
)
echo.
exit /b 0
