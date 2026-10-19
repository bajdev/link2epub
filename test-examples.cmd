@echo off
rem Builds link2epub.exe, then converts the articles listed in
rem test-examples.txt into test-examples\ for checking on the e-readers: one
rem book per article, then all of them as one reading-list book. Books from
rem a previous run are replaced (-FORCE). A failure on one URL doesn't stop
rem the others.
rem
rem Run it from Command Prompt or PowerShell:
rem     test-examples.cmd          (Command Prompt)
rem     .\test-examples.cmd        (PowerShell)
rem Add "kepub" to make Kobo KEPUBs (.kepub.epub) instead of plain EPUBs:
rem     test-examples.cmd kepub
setlocal

set KEPUB=
if /i "%~1"=="kepub" set KEPUB=-KEPUB

rem Work from the folder this script is in, wherever it was started from.
cd /d "%~dp0"

call build.cmd || exit /b 1
echo.

if not exist test-examples mkdir test-examples

set /a ok=0, failed=0
for /f "usebackq eol=# delims=" %%U in ("test-examples.txt") do call :convert "%%U"

rem All five again, as one reading-list book.
echo === Reading list of all five
link2epub.exe -URLS test-examples.txt -TITLE "link2epub test examples" -SAVE test-examples\ -FORCE %KEPUB%
if errorlevel 1 (
    set /a failed+=1
) else (
    set /a ok+=1
)
echo.

echo %ok% books made, %failed% failed. Books are in %CD%\test-examples\
if %failed% gtr 0 exit /b 1
exit /b 0

rem Converts one URL and counts the result. The -SAVE folder is left
rem unquoted: a quoted path ending in a backslash would swallow the quote.
:convert
echo === %~1
link2epub.exe -URL "%~1" -SAVE test-examples\ -FORCE %KEPUB%
if errorlevel 1 (
    set /a failed+=1
) else (
    set /a ok+=1
)
echo.
exit /b 0
