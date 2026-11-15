import os
import shutil
from pathlib import Path


def main():
    base_dir = Path(__file__).parent.resolve()
    backup_dir = base_dir / "backup"

    print("==================================================")
    print("              SBLINK PREPARE SETUP                ")
    print("==================================================")

    backup_dir.mkdir(exist_ok=True)

    # move old logs and config to backup, which also resets preferences
    # so sblink starts fresh on next run
    files_moved = 0
    for item in base_dir.iterdir():
        if item.is_file() and item.name != Path(__file__).name:
            if item.suffix in [".log", ".json"]:
                dest = backup_dir / item.name
                if dest.exists():
                    dest.unlink()
                shutil.move(str(item), str(dest))
                print(f"Backed up: {item.name}")
                files_moved += 1

    if files_moved == 0:
        print("No existing configuration or log files found to backup")

    print("[+] Preferences reset")
    print()

    bat_path = base_dir / "sblink.bat"
    bat_content = """@echo off
title sblink setup
color 0B
echo ===============================================================================
echo                           SBLINK AUTOMATED SETUP
echo ===============================================================================
echo.
echo Installing required Python libraries...
python -m pip install requests rich pystray pillow
echo.
python "%~dp0sblink.py" setup
echo.
echo ===============================================================================
echo                              SETUP COMPLETE
echo ===============================================================================
echo You can now close this window, or run the app manually
echo.
pause
"""
    bat_path.write_text(bat_content, encoding="utf-8")
    print(f"[+] Created {bat_path.name}")
    print("==================================================")
    print("Preparation complete! Please run sblink.bat to start the interactive setup")
    print("==================================================")


if __name__ == "__main__":
    main()
