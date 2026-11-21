# sblink

sblink links add-ons for your SimBrief flight plan into the Microsoft Flight Simulator Community folder. It looks for folder names that match the plan's aircraft, airline, departure airport, or arrival airport. It uses Windows directory junctions, so your add-on files stay in their libraries.

## Getting started

1. Download `sblink.exe` from [GitHub Releases](https://github.com/ziul510/sblink/releases/latest) and put it in a folder where you can save files. The Windows x64 release runs without an installer or Go.
2. Double-click `sblink.exe` to open setup. Enter your numeric SimBrief Pilot ID and the full paths to your Community folder and add-on libraries.
3. Start sblink at the end of setup or use the **sblink Run** shortcut. To change settings later, use **sblink Setup**.

sblink checks for new SimBrief flight plans while the simulator is closed. In the system tray, use **Show** to view the live log, **Link Addons** to process the current plan again, or **Unlink Addons** to remove links marked by sblink.

Settings, mappings, and logs live beside the executable. If you move sblink, run setup again to update its shortcuts and Windows startup settings.

For a custom aircraft match, enable manual mapping in setup and edit `mappings.json`. If sblink finds the mapped folder in a library, it uses it as the only match for that library.

## Command line

In PowerShell, run `.\sblink.exe` followed by a command.

| Command | Action |
| --- | --- |
| `setup` | Open setup. |
| `run` | Start sblink. |
| `status` | Show settings, mappings, and the last processed plan. |
| `manual` | Check once for a new plan. |
| `link` | Process the current plan even if it was already processed. |
| `unlink` | Remove links marked by sblink. |
| `logs` | Open the live log. |
| `reset` | Move settings, mappings, and logs to `backup/` after you quit sblink. |

## Build from source

On Windows x64, use Go 1.24 or later.

```powershell
$env:CGO_ENABLED = '0'
go build -trimpath -ldflags '-H=windowsgui -s -w' -o sblink.exe .
```

## License

[Apache License 2.0](LICENSE).
