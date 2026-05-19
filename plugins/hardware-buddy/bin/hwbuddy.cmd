@echo off
REM Windows launcher for the hwbuddy MCP server / CLI client.
REM Plugin's bin/ directory is on the Bash tool's PATH; this .cmd shim
REM resolves to the right Windows binary alongside it.
setlocal
set "DIR=%~dp0"
set "BIN=%DIR%hwbuddy-windows-amd64.exe"
if not exist "%BIN%" (
    echo hwbuddy: no prebuilt binary at "%BIN%" 1>&2
    echo hwbuddy: rebuild with `go build -o bin\hwbuddy-windows-amd64.exe .\cmd\hwbuddy` 1>&2
    exit /b 127
)
"%BIN%" %*
