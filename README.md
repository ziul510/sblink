# sblink

A portable SimBrief linker for MSFS on Windows x64.
The release is one `sblink.exe`.
There are no Go modules to download and no Go installation is needed to use it.

## Use

Download the Windows exe (or zip) from the distribution page, extract it to a folder,
and double-click `sblink.exe`. Developers can build it with Go using the command below.
Without a config file it opens the five-step console setup wizard
With a config file it starts the daemon in the configured hidden or visible mode
Setup creates **sblink Run.lnk** and **sblink Setup.lnk** beside the executable so both
modes are available from File Explorer. Those shortcuts are generated at runtime
If you move the executable later, run setup to recreate its shortcuts and startup entry

From a console:

```powershell
.\sblink.exe setup
.\sblink.exe run
.\sblink.exe status
.\sblink.exe manual
.\sblink.exe link
.\sblink.exe unlink
.\sblink.exe reset
```

- `setup` runs the interactive wizard. Nothing is downloaded or installed
- `run` launches a separate daemon and returns. Only one daemon per executable folder runs
- `status` displays configuration, last processed identifiers, and mappings
- `manual` polls once using the same new-plan decision as the daemon
- `link` fetches and processes the current plan even if already processed
- `unlink` removes only links with a regular `.sblink` marker in their target
- `reset` backs up app config, mappings, and all three logs to `backup/` and resets preferences

The tray menu contains **Run setup**, **Show**, **Link Addons**, **Unlink Addons**, and **Quit**.
**Show** opens a native Windows console with the existing log and new lines as they arrive.
Closing that console leaves the daemon running.
`logs` also opens this console directly.
Visible sessions start the built-in Windows command processor with a new console so Windows
delegates to the user's default terminal application
Tray setup opens a separate console so canceling setup does not interrupt a visible daemon.
The interactive wizard always uses its color palette. Redirected output stays free of color
and clearing sequences. Terminal width comes from the live console, including delegated terminals.
Community folder, addon library paths, and SimBrief ID require a nonblank answer unless a saved value exists.

## Data and compatibility

Config, mappings, logs, locks, and portable shortcuts live beside the running executable
Use absolute paths in setup. Missing paths are saved with a warning, as in the reference.
Daemon and manual startup require a Community directory and every configured library directory

Existing keys survive updates. `mappings.json` keeps the same three example entries and uppercase lookup
Existing mappings with invalid JSON, encoding, or values are preserved and reported as errors.
Fix the file or use `reset`, as invalid mappings are not searched.

The daemon reloads settings before each poll or tray action. Changes to the account or addon paths
trigger a fresh link cycle even for an unchanged plan. Poll interval changes affect the following wait.
Changing hidden/visible mode requires quitting and restarting the daemon, as the setup prompt explains.

Logs use `[YYYY-MM-DD HH:MM:SS] [TAG] message` in local time. Rotation occurs before a line
would reach 1 MiB, retaining `sblink.log.1` and `sblink.log.2`. Shared locks serialize writes
between processes. Empty `.lock` files may remain after exit; open handles hold the locks.
Log lock acquisition waits at most one second per line. Polling log waits also honor cancellation.
Lock timeouts are reported rather than blocking shutdown indefinitely. Reset lock waits honor cancellation.

Windows startup uses the existing HKCU `Software\Microsoft\Windows\CurrentVersion\Run`
value named `sblink`, with `"<full path>\sblink.exe" run`. The Start Menu shortcut is
`%APPDATA%\Microsoft\Windows\Start Menu\Programs\sblink.lnk`, targeting the executable
with argument `run` and working directory `%USERPROFILE%`.
Windows' built-in PowerShell and WScript.Shell create those entries; no script file is installed.
Directory junctions and tray operations use native Windows APIs directly.

Aircraft expansion, per-category shallowest BFS matches, and substring matching intentionally
follow the Python version. For example, A321 also searches A320, 321, and 320; DAL can match
“Dallas”. A manual mapping returns only its mapped folder from that library, suppressing other
categories there. A missing mapped folder falls back to BFS. This is not a livery-only filter.
Mappings accept nested relative patterns, `*`, `?`, recursive `**` path components.
Searches follow directory junctions, prevent ancestor cycles,
and retain distinct aliases. An unavailable library entry fails the scan and leaves the plan retryable.

## Build and verify

Developers need Go 1.24 or newer. Only the standard library is used, so vendoring is unnecessary.

```powershell
go build -trimpath -ldflags "-H=windowsgui -s -w" -o sblink.exe .
go vet ./...
gofmt -l .
```

Run the build command on Windows x64 with `CGO_ENABLED=0`. The GUI PE subsystem
prevents hidden launches from flashing a console. The application allocates native
consoles explicitly for console modes. A normal `go build` is useful for development but
does not provide the release build's hidden launch behavior.
The public repository contains production Go source and user documentation.
Development tests, recorded fixtures, and the historical Python implementation are kept outside
the published source tree.

## Automatic builds and releases

GitHub Actions builds Windows x64 when a release is published. Each build runs vet and formatting
checks, then attaches the executable, a versioned ZIP, and its SHA-256 checksum to the release.
Pushing source changes does not start a build.

## License

MIT with Commons Clause License Condition v1.0; see [LICENSE](LICENSE).
The source is available for review and modification subject to those terms.
