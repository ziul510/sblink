@echo off
title sblink setup
cls

echo Installing required Python libraries...
python -m pip install requests rich pystray pillow
echo.

python "%~dp0sblink.py" setup

if errorlevel 1 (
    echo.
    echo   [!] Setup failed. See output above for details.
    echo.
    pause
    exit /b 1
)

pause
