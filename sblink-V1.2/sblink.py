import sys
import time
import os
import subprocess
from pathlib import Path
from subprocess import list2cmdline

_SCRIPT_DIR = Path(__file__).parent.resolve()
if str(_SCRIPT_DIR) not in sys.path:
    sys.path.insert(0, str(_SCRIPT_DIR))

from config import Config
from daemon import SimBriefDaemon
from logger import log

# optional rich library for setup UI
_RICH = False
Align = Console = Panel = Prompt = IntPrompt = Rule = Text = None

try:
    from rich.align import Align
    from rich.console import Console
    from rich.panel import Panel
    from rich.prompt import Prompt, IntPrompt
    from rich.rule import Rule
    from rich.text import Text
    _RICH = True
except ImportError:
    pass

# optional tray dependencies: pystray and Pillow
_TRAY_AVAILABLE = False
try:
    import pystray
    from PIL import Image, ImageDraw
    _TRAY_AVAILABLE = True
except ImportError:
    pass


_SETUP_BANNER = r"""
           ░██        ░██ ░██           ░██       
           ░██        ░██               ░██       
 ░███████  ░████████  ░██ ░██░████████  ░██    ░██
░██        ░██    ░██ ░██ ░██░██    ░██ ░██   ░██ 
 ░███████  ░██    ░██ ░██ ░██░██    ░██ ░███████  
       ░██ ░███   ░██ ░██ ░██░██    ░██ ░██   ░██ 
 ░███████  ░██░█████  ░██ ░██░██    ░██ ░██    ░██

"""

SETUP_COLORS = {
    "banner": "bold #5fd7ff",   # sky blue
    "border": "#4080c0",        # medium steel blue
    "step_border": "#b07fff",   # lavender/violet
    "title":  "bold #b07fff",   # lavender/violet
    "prompt": "bold #5fdf87",   # mint green
}

# console window handle for show/hide in tray mode
_console_hwnd = None
_ctrl_handler_ref = None  # held to prevent garbage collection
_wndproc_ref = None       # held to prevent garbage collection
_old_wndproc_ref = None


def _init_tray_console():
    """No longer allocates a console. The 'Show' button now uses a Tkinter GUI."""
    pass


def _make_tray_image():
    """Create the system tray icon - two interlocking chain links on a dark background."""
    from PIL import Image, ImageDraw
    s = 64
    img = Image.new("RGBA", (s, s), (0, 0, 0, 0))
    d = ImageDraw.Draw(img)

    bg = (12, 32, 80)
    fg = (95, 215, 255)

    d.rounded_rectangle([0, 0, s - 1, s - 1], radius=12, fill=bg)

    lw = 5
    d.rounded_rectangle([5, 19, 35, 45], radius=9, outline=fg, width=lw)
    d.rounded_rectangle([29, 19, 59, 45], radius=9, outline=fg, width=lw)
    # paint over the overlap so the links look interlocked
    d.rectangle([27, 25, 37, 39], fill=bg)

    return img


def _run_with_tray(daemon):
    """Show the system tray icon and block until the user quits."""
    import pystray

    bat_path = _SCRIPT_DIR / "sblink.bat"

    def on_run_bat(icon, item):
        if bat_path.exists():
            os.startfile(str(bat_path))
        else:
            log("WARNING", f"sblink.bat not found at {bat_path}")

    def on_show(icon, item):
        # run Tkinter in a separate thread to avoid blocking the tray icon
        import threading
        def show_gui():
            try:
                import tkinter as tk
                root = tk.Tk()
                root.title("sblink Status")
                root.geometry("600x400")
                
                text = tk.Text(root, wrap="word", bg="#1e1e1e", fg="#d4d4d4")
                text.pack(expand=True, fill="both")
                
                import sys
                import logging
                from logger import log_path, logger, formatter
                
                if log_path.exists():
                    with open(log_path, "r", encoding="utf-8") as f:
                        text.insert("1.0", f.read())
                else:
                    text.insert("1.0", "Log file not found.\n")
                text.see("end")
                text.config(state="disabled")

                class IORedirector:
                    def write(self, string):
                        try:
                            text.after(0, self._write, string)
                        except Exception:
                            pass
                    def _write(self, string):
                        try:
                            text.config(state="normal")
                            if "\r" in string:
                                parts = string.split("\r")
                                for i, part in enumerate(parts):
                                    if i > 0:
                                        text.delete("end-1c linestart", "end-1c")
                                    if part:
                                        text.insert("end", part)
                            else:
                                text.insert("end", string)
                            text.see("end")
                            text.config(state="disabled")
                        except Exception:
                            pass
                    def flush(self):
                        pass

                class TkLogHandler(logging.Handler):
                    def emit(self, record):
                        msg = self.format(record)
                        try:
                            text.after(0, self._write, msg + "\n")
                        except Exception:
                            pass
                    def _write(self, string):
                        try:
                            text.config(state="normal")
                            text.insert("end", string)
                            text.see("end")
                            text.config(state="disabled")
                        except Exception:
                            pass

                redirector = IORedirector()
                old_stdout = sys.stdout
                old_stderr = sys.stderr
                sys.stdout = redirector
                sys.stderr = redirector
                
                tk_handler = TkLogHandler()
                tk_handler.setFormatter(formatter)
                logger.addHandler(tk_handler)
                
                # Clicking X naturally destroys the Tkinter window without killing the daemon process!
                def on_closing():
                    sys.stdout = old_stdout
                    sys.stderr = old_stderr
                    logger.removeHandler(tk_handler)
                    root.destroy()
                    
                root.protocol("WM_DELETE_WINDOW", on_closing)
                root.mainloop()
            except ImportError:
                # fallback if tkinter isn't installed
                import os
                from logger import log_path
                if log_path.exists():
                    os.startfile(str(log_path))
                    
        threading.Thread(target=show_gui, daemon=True).start()

    def on_link(icon, item):
        daemon.force_link()

    def on_unlink(icon, item):
        daemon.force_unlink()

    def on_quit(icon, item):
        icon.stop()

    menu = pystray.Menu(
        pystray.MenuItem("Run sblink.bat", on_run_bat),
        pystray.MenuItem("Show", on_show),
        pystray.Menu.SEPARATOR,
        pystray.MenuItem("Link Addons", on_link),
        pystray.MenuItem("Unlink Addons", on_unlink),
        pystray.Menu.SEPARATOR,
        pystray.MenuItem("Quit", on_quit),
    )

    icon = pystray.Icon("sblink", _make_tray_image(), "sblink", menu)

    try:
        icon.run()
    except Exception as e:
        log("ERROR", f"Tray icon failed: {e}")
    finally:
        pass
        daemon.stop()
        sys.exit(0)


def _validate_paths(config):
    all_ok = True
    community = config.get("community_folder_path")
    if not community:
        log("ERROR", "Config field 'community_folder_path' is empty. Run setup")
        all_ok = False
    else:
        p = Path(community)
        if not p.exists():
            log("ERROR", f"Path does not exist: {p}")
            all_ok = False
        elif not p.is_dir():
            log("ERROR", f"Path is not a directory: {p}")
            all_ok = False

    count = config.get("addon_library_count", 1)
    for i in range(1, count + 1):
        key = "addon_library_path" if i == 1 else f"addon_library_path_{i}"
        path_str = config.get(key)
        if not path_str:
            log("ERROR", f"Config field '{key}' is empty. Run setup")
            all_ok = False
            continue
        p = Path(path_str)
        if not p.exists():
            log("ERROR", f"Path does not exist: {p}")
            all_ok = False
        elif not p.is_dir():
            log("ERROR", f"Path is not a directory: {p}")
            all_ok = False
    return all_ok


def cmd_setup():
    global _RICH, _TRAY_AVAILABLE, Align, Console, Panel, Prompt, IntPrompt, Rule, Text

    if not _RICH:
        import importlib
        os.system("cls" if os.name == "nt" else "clear")
        print("  SimBrief --> MSFS Addon Linker\n")
        print("  -------------------------------------------------------")
        print("   Step 1 of 5   Installing Dependencies")
        print("  -------------------------------------------------------\n")
        print("  Installing required Python libraries")
        try:
            subprocess.check_call([
                sys.executable, "-m", "pip", "install",
                "requests", "rich", "pystray", "pillow"
            ])
        except Exception as e:
            print(f"\n  [!] Failed to install dependencies: {e}")
            sys.exit(1)

        importlib.invalidate_caches()

        try:
            from rich.align import Align
            from rich.console import Console
            from rich.panel import Panel
            from rich.prompt import Prompt, IntPrompt
            from rich.rule import Rule
            from rich.text import Text
            _RICH = True
        except ImportError:
            pass

        if not _RICH or not Console:
            print("\n  [!] Installed successfully, but import failed. Please restart setup")
            sys.exit(1)

        try:
            import pystray
            from PIL import Image, ImageDraw
            _TRAY_AVAILABLE = True
        except ImportError:
            pass

    console = Console()
    config = Config()

    def print_header():
        console.clear()
        console.print(Panel(
            Align.center(Text(_SETUP_BANNER, style=SETUP_COLORS["banner"])),
            border_style=SETUP_COLORS["border"],
            subtitle=f"[{SETUP_COLORS['banner']}]SimBrief Linker[/{SETUP_COLORS['banner']}]",
            padding=(0, 2),
        ))
        console.print()

    def ask(label, current, hint=None, choices=None):
        if hint:
            console.print(f"  [dim]{hint}[/dim]")
        kwargs = {}
        if choices:
            kwargs["choices"] = choices
        if current:
            console.print(f"  [dim]Current:[/dim] [yellow]{current}[/yellow]")
            val = Prompt.ask(
                f"  [{SETUP_COLORS['prompt']}]{label}[/{SETUP_COLORS['prompt']}]  [dim](Enter to keep)[/dim]",
                default=current,
                **kwargs
            )
        else:
            val = Prompt.ask(
                f"  [{SETUP_COLORS['prompt']}]{label}[/{SETUP_COLORS['prompt']}]",
                **kwargs
            )
        return val.strip()

    # step 1 of 5 - MSFS paths
    print_header()
    console.print(Panel(
        "  sblink needs to know where your MSFS community folder is,\n"
        "  and the root of your addon library (where your liveries / addons live)\n\n"
        "  [dim]Both paths must already exist on disk[/dim]",
        title=f"[{SETUP_COLORS['title']}]── 1 / 5  MSFS Paths ──[/{SETUP_COLORS['title']}]",
        border_style=SETUP_COLORS["step_border"],
        padding=(0, 2),
    ))
    console.print()

    community = ask(
        "Community folder",
        config.get("community_folder_path"),
        hint=r"e.g.  %AppData%\Microsoft Flight Simulator\Packages\Community",
    )
    config.set("community_folder_path", community)
    console.print()

    num_libs_str = ask(
        "How many addons libraries would you like to use?",
        str(config.get("addon_library_count", 1)),
        hint="Enter a number (e.g. 1, 2, 3)",
    )
    try:
        num_libs = max(1, int(num_libs_str))
    except ValueError:
        num_libs = 1
    config.set("addon_library_count", num_libs)
    console.print()

    addon_libs = []
    for i in range(1, num_libs + 1):
        key = "addon_library_path" if i == 1 else f"addon_library_path_{i}"
        lib_path = ask(
            f"Addon library #{i}",
            config.get(key, ""),
            hint=r"e.g.  D:\MSFS Addons",
        )
        config.set(key, lib_path)
        addon_libs.append((f"Addon library #{i}", lib_path))
        console.print()

    for label, val in [("Community folder", community)] + addon_libs:
        p = Path(val) if val else None
        if p and p.exists() and p.is_dir():
            console.print(f"  [green]OK[/green]  {label}: [dim]{p}[/dim]")
        elif val:
            console.print(f"  [yellow]![/yellow]  {label} path not found - saved anyway, check before running")
        else:
            console.print(f"  [red]X[/red]  {label} is empty")

    # step 2 of 5 - SimBrief account
    print_header()
    console.print(Panel(
        "  Your numeric SimBrief Pilot ID.  Find it at [bold]simbrief.com[/bold] -> Account -> Briefing",
        title=f"[{SETUP_COLORS['title']}]── 2 / 5  SimBrief Account ──[/{SETUP_COLORS['title']}]",
        border_style=SETUP_COLORS["step_border"],
        padding=(0, 2),
    ))
    console.print()

    sb_id = ask(
        "SimBrief Pilot ID",
        config.get("simbrief_user_id"),
        hint="Numeric only - e.g. 123456",
    )
    config.set("simbrief_user_id", sb_id)

    # step 3 of 5 - polling interval
    print_header()
    console.print(Panel(
        "  How often sblink polls SimBrief for a new flight plan.  [dim]Minimum: 10 s[/dim]",
        title=f"[{SETUP_COLORS['title']}]── 3 / 5  Polling Interval ──[/{SETUP_COLORS['title']}]",
        border_style=SETUP_COLORS["step_border"],
        padding=(0, 2),
    ))
    console.print()

    current_interval = str(config.get("poll_interval_seconds", 60))
    interval_str = ask("Poll interval (seconds)", current_interval)
    try:
        clamped = max(10, int(interval_str))
        config.set("poll_interval_seconds", clamped)
        if clamped != int(interval_str):
            console.print("  [yellow]![/yellow]  Value below minimum - set to 10 s")
    except ValueError:
        console.print("  [yellow]![/yellow]  Invalid number - keeping previous value")

    # step 4 of 5 - background mode, Windows startup, and Start Menu shortcut
    print_header()
    console.print(Panel(
        "  Configure background behavior and startup options.\n\n"
        "  [dim]These settings can be changed later by running setup again[/dim]",
        title=f"[{SETUP_COLORS['title']}]── 4 / 5  Startup & Background ──[/{SETUP_COLORS['title']}]",
        border_style=SETUP_COLORS["step_border"],
        padding=(0, 2),
    ))
    console.print()

    current_hidden = config.get("run_hidden", "y")
    hidden = ask("Run in background (no window)?", current_hidden, hint="Should sblink run silently in the background, with no console window?", choices=["y", "n"]).lower()
    config.set("run_hidden", hidden)

    startup = "n"
    create_start_menu = "n"
    if os.name == "nt":
        import winreg
        key_path = r"Software\Microsoft\Windows\CurrentVersion\Run"
        current_startup = "n"
        try:
            key = winreg.OpenKey(winreg.HKEY_CURRENT_USER, key_path, 0, winreg.KEY_READ)
            val, _ = winreg.QueryValueEx(key, "sblink")
            if val:
                current_startup = "y"
            winreg.CloseKey(key)
        except FileNotFoundError:
            pass

        console.print()
        startup = ask("Start with Windows?", current_startup, hint="Should sblink start automatically when Windows boots?", choices=["y", "n"]).lower()
        console.print()
        create_start_menu = ask("Create Start Menu shortcut?", "y", hint="Optionally create a Start Menu shortcut to launch sblink on demand", choices=["y", "n"]).lower()

        if startup == "y":
            if hidden == "y":
                python_dir = Path(sys.executable).parent
                pythonw_path = python_dir / "pythonw.exe"
                if not pythonw_path.exists():
                    pythonw_path = sys.executable
                cmd = list2cmdline([str(pythonw_path), str(Path(__file__).resolve()), "run"])
            else:
                cmd = list2cmdline([
                    "cmd.exe", "/c", "start", "", str(sys.executable), str(Path(__file__).resolve()), "run"
                ])
            try:
                key = winreg.OpenKey(winreg.HKEY_CURRENT_USER, key_path, 0, winreg.KEY_SET_VALUE)
                winreg.SetValueEx(key, "sblink", 0, winreg.REG_SZ, cmd)
                winreg.CloseKey(key)
                console.print("\n  [green]OK[/green]  Startup enabled")
            except Exception as e:
                console.print(f"\n  [red]X[/red]  Failed to configure startup: {e}")
        else:
            try:
                key = winreg.OpenKey(winreg.HKEY_CURRENT_USER, key_path, 0, winreg.KEY_SET_VALUE)
                winreg.DeleteValue(key, "sblink")
                winreg.CloseKey(key)
                console.print("\n  [green]OK[/green]  Startup disabled")
            except FileNotFoundError:
                pass
            except Exception as e:
                console.print(f"\n  [red]X[/red]  Failed to configure startup: {e}")
    else:
        console.print("\n  [yellow]![/yellow]  Windows startup options are unavailable on this OS")

    if os.name == "nt":
        start_menu_dir = Path(os.environ.get("APPDATA", "")) / "Microsoft" / "Windows" / "Start Menu" / "Programs"
        shortcut_lnk = start_menu_dir / "sblink.lnk"
        shortcut_bat = Path(__file__).resolve().parent / "sblink-start-menu-launch.bat"
        shortcut_ps1 = start_menu_dir / "sblink-shortcut.ps1"
        try:
            if create_start_menu == "y":
                start_menu_dir.mkdir(parents=True, exist_ok=True)
                app_path = str(Path(__file__).resolve())
                
                if hidden == "y":
                    python_dir = Path(sys.executable).parent
                    python_path = python_dir / "pythonw.exe"
                    if not python_path.exists():
                        python_path = sys.executable
                else:
                    python_path = sys.executable

                # Clean up legacy bat file if it exists
                shortcut_bat.unlink(missing_ok=True)

                lnk_path = str(shortcut_lnk).replace("'", "''")
                target_path = str(python_path).replace("'", "''")
                icon_path = str(sys.executable).replace("'", "''")
                
                ps_body = (
                    "$w=New-Object -ComObject WScript.Shell;"
                    f"$s=$w.CreateShortcut('{lnk_path}');"
                    f"$s.TargetPath='{target_path}';"
                    f"$s.Arguments='\"{app_path}\" run';"
                    "$s.WorkingDirectory=$env:USERPROFILE;"
                    f"$s.IconLocation='{icon_path},0';"
                    "$s.Save()"
                )
                shortcut_ps1.write_text(ps_body, encoding="utf-8")
                subprocess.run(["powershell", "-NoProfile", "-ExecutionPolicy", "Bypass", "-File", str(shortcut_ps1)], check=True)
                shortcut_ps1.unlink(missing_ok=True)
                console.print("\n  [green]OK[/green]  Start Menu shortcut created")
            else:
                shortcut_lnk.unlink(missing_ok=True)
                shortcut_bat.unlink(missing_ok=True)
                shortcut_ps1.unlink(missing_ok=True)
                console.print("\n  [green]OK[/green]  Start Menu shortcut removed")
        except Exception as e:
            console.print(f"\n  [yellow]![/yellow]  Start Menu shortcut update failed: {e}")

    # step 5 of 5 - manual folder mapping
    print_header()
    console.print(Panel(
        "  Would you like to manually map specific aircraft to folders?\n"
        "  [dim]This creates mappings.json where you can specify overrides[/dim]",
        title=f"[{SETUP_COLORS['title']}]── 5 / 5  Manual Folder Mapping ──[/{SETUP_COLORS['title']}]",
        border_style=SETUP_COLORS["step_border"],
        padding=(0, 2),
    ))
    console.print()

    from config import Mappings
    map_opt = ask("Enable manual mapping?", "n", choices=["y", "n"]).lower()
    if map_opt == "y":
        mappings = Mappings()
        if mappings.is_new:
            console.print("\n  [green]OK[/green]  Created [bold]mappings.json[/bold] with an example")
        else:
            console.print("\n  [green]OK[/green]  [bold]mappings.json[/bold] already exists")
        console.print(f"  [dim]Location:[/dim] [cyan]{mappings._path}[/cyan]")

    # setup complete
    print_header()
    console.print(Panel(
        "  [green]OK[/green]  Configuration saved\n\n"
        "  [dim]To start the daemon:[/dim]        [bold cyan]python sblink.py run[/bold cyan]\n"
        "  [dim]To check your settings:[/dim]     [bold cyan]python sblink.py status[/bold cyan]\n"
        "  [dim]To trigger a manual sync:[/dim]   [bold cyan]python sblink.py manual[/bold cyan]",
        title="[bold green]── Setup Complete ──[/bold green]",
        border_style="green",
        padding=(1, 2),
    ))
    console.print()

    start_now = ask("Start the daemon now?", "y", choices=["y", "n"]).lower()
    if start_now == "y":
        console.print()
        if hidden == "y":
            python_dir = Path(sys.executable).parent
            pythonw_path = python_dir / "pythonw.exe"
            if not pythonw_path.exists():
                pythonw_path = sys.executable
            creationflags = subprocess.CREATE_NO_WINDOW if os.name == "nt" else 0
            subprocess.Popen(
                [str(pythonw_path), str(Path(__file__).resolve()), "run"],
                creationflags=creationflags
            )
            console.print("  [green]OK[/green]  Daemon started in background")
            time.sleep(1)
        else:
            cmd_run()


def cmd_run():
    config = Config()
    if not config.is_valid():
        log("ERROR", "Configuration incomplete. Run 'python sblink.py setup' first")
        sys.exit(1)

    if not _validate_paths(config):
        sys.exit(1)

    if os.name == "nt" and _TRAY_AVAILABLE:
        _init_tray_console()

    if sys.stdout is not None:
        print("sblink running")
        print(f"Community: {config.get('community_folder_path')}")
        print(f"Library: {config.get('addon_library_path')}")
        print(f"User ID: {config.get('simbrief_user_id')}")
        print(f"Interval: {config.get('poll_interval_seconds')} seconds")
        print("")

    daemon = SimBriefDaemon()
    daemon.start()

    if os.name == "nt" and _TRAY_AVAILABLE:
        _run_with_tray(daemon)
    else:
        try:
            while True:
                time.sleep(1)
        except KeyboardInterrupt:
            print("")
            log("INFO", "Stopping daemon")
            daemon.stop()
            sys.exit(0)


def cmd_status():
    config = Config()
    data = config.to_dict()

    print("Configuration")
    print(f"Community Folder: {data.get('community_folder_path') or '-'}")
    print(f"Addon Library: {data.get('addon_library_path') or '-'}")
    print(f"SimBrief User ID: {data.get('simbrief_user_id') or '-'}")
    print(f"Poll Interval: {data.get('poll_interval_seconds', 60)} s")
    print("")
    print("Last Processed")
    print(f"Plan ID: {data.get('last_processed_plan_id') or '-'}")
    print(f"Timestamp: {data.get('last_processed_timestamp') or '-'}")
    print("")

    from config import Mappings
    mappings = Mappings().to_dict()
    if mappings:
        print("Manual Mappings")
        for k, v in mappings.items():
            print(f"  {k} -> {v}")
        print("")


def cmd_manual():
    config = Config()
    if not config.is_valid():
        log("ERROR", "Configuration incomplete. Run 'python sblink.py setup' first")
        sys.exit(1)

    if not _validate_paths(config):
        sys.exit(1)

    log("INFO", "Starting manual poll cycle")
    daemon = SimBriefDaemon()
    daemon.run_manual()
    log("INFO", "Manual cycle complete")


def main():
    if len(sys.argv) < 2:
        print("sblink")
        print("Usage: python sblink.py <command>")
        print("Commands: setup, run, status, manual")
        sys.exit(1)

    command = sys.argv[1].lower()
    commands = {
        "setup":  cmd_setup,
        "run":    cmd_run,
        "status": cmd_status,
        "manual": cmd_manual,
    }

    if command in commands:
        commands[command]()
    else:
        log("ERROR", f"Unknown command: {command}")
        sys.exit(1)


if __name__ == "__main__":
    main()
