# Verification and behavior contract

The specification is the seven Python modules and setup batch file in `sblink-V1.2/`.
The source was read before implementing the Go version. The reference files were not changed.

## Automated coverage

| Contract | Evidence |
| --- | --- |
| Aircraft expansion including stripped manufacturer, families, A3 fallback, lowercase, and short values | `TestAircraftTerms` |
| Null versus empty identifiers; same/new ID and timestamp; route fallback with no persisted comparison | `TestNewPlanMatrix`, `TestConfigMalformedAndDefaults` |
| Recorded payload extraction; alternate fields; numbers; missing and wrong-type sections; whitespace identity | `TestExtractRecordedPayloads`, `testdata/*.json` |
| HTTP query, status errors, bad/trailing JSON, invalid users, request cancellation, and timeout | `TestSimBriefHTTP` |
| BFS depth limit, ties at the shallowest depth, independent category watermarks, nested liveries, short terms | `TestBFS` |
| Manual aircraft-family mapping short-circuits search; missing folder falls back; cancellation | `TestManualMapping` |
| Config needs at least one library; path validation needs every configured directory | `TestValidationStrictness` |
| Malformed config is handled; unknown keys and UTF-8 survive; concurrent updates do not overwrite each other | `TestConfigMalformedAndDefaults`, `TestAtomicConcurrentUpdates` |
| Atomic wizard completion; EOF never creates half a config | `TestInterruptedSetupDoesNotSave`, `TestExecutableContract` |
| Actual executable setup with scripted stdin, key order, defaults, minimum interval, invalid interval and library count | `TestExecutableContract` |
| Clean-directory status; incomplete manual config message and exit 1; unknown command | `TestExecutableContract` |
| No argument without config selects setup | `TestExecutableContract` |
| Explicit `run` and no argument with config both return promptly and launch a hidden tray daemon | `TestExecutableContract/tray_and_native_consoles/background_argument_dispatch` |
| Registry value name, path, quoted exe target, and explicit `run` | `TestExecutableContract`, `verifyStartup` |
| Start Menu and portable Run/Setup shortcuts point to the exe with correct arguments | `TestExecutableContract`, `verifyShortcut` |
| No external runtime import; actual release-style PE is AMD64 and Windows GUI subsystem; no Go/Python on child PATH | `TestExecutableContract` |
| Real NTFS junction, OS reparse tag 0xA0000003, target marker, safe cleanup, two links to one target, foreign junction and real-folder survival | `TestJunctionOwnership` |
| Partial linking does not mark plan processed; next poll retries and succeeds | `TestPartialCycleRetries` |
| Duplicate addon basenames across libraries finish successfully | `TestDuplicateLibraryNamesFinish`, `TestUniqueAddons` |
| MSFS pauses polling, then resumes; no-match plans are marked | `TestPauseResumeAndMark` |
| Native MSFS detection by process name | `TestMSFSNativeProcessDetection` with a temporary renamed Windows ping process |
| Failed config persistence does not update in-memory state | `TestSaveFailureDoesNotMark` |
| Poll errors are returned; daemon cancels active requests and joins its worker | `TestPollErrors`, `TestDaemonCancellation` |
| Native tray popup opens; dispatch exercises setup, Show, Link, Unlink, and Quit; shutdown completes | `TestExecutableContract/tray_and_native_consoles` |
| `run_hidden=y` has no attached console; `n` has a visible native console; Show and setup create visible native consoles | Same test using a separate process with AttachConsole and IsWindowVisible |
| Log format; rotation beyond 1 MiB; exactly two backups; concurrent log writers | `TestRotationAndConcurrentLogging` |
| Show follows log replacement and releases file handles before console output | `TestLogViewerFollowsRotation` |
| Unicode aircraft input cannot panic; incomplete and canceled searches stay retryable | `TestMalformedAircraftDoesNotPanic`, `TestFailedScanDoesNotMarkPlan`, `FuzzAircraftTerms` |
| Damaged mappings are preserved; invalid UTF-8 is rejected; config lock waits cancel | `TestDamagedMappingsStayUntouched`, `TestCanceledLockWait` |
| Wrong-type primary identities fall back to valid alternate fields; oversized HTTP responses fail | `TestIdentityFallsBackFromWrongType`, `TestOversizedResponseRejected` |
| Active daemon reloads account, paths, and interval without forgetting retry state | `TestDaemonReloadsSettings` |
| Real Ctrl+Break cancels setup and daemon cleanly; setup has an independent console | `TestExecutableContract/tray_and_native_consoles` |
| Session-end messages cancel and join worker before returning; pseudoconsole handles accepted | Same native integration test |
| All six wizard panel layouts match Python Rich at 80, 120, and 190 columns | `TestSetupPanelsMatchPython`, 18 `testdata/setup-*.txt` fixtures |
| Python palette, clearing, and narrow-window bounds | `TestSetupColorsAndClear`, `TestSetupNarrowPanels` |
| Default-terminal delegation, executable paths with spaces and shell metacharacters, and launcher termination | `TestExecutableContract/terminal_path_and_lifetime` |
| Terminal child rebinds redirected handles and queries actual width and VT color support | `TestTerminalBinding` with a real native console and `NO_COLOR=1` |
| Blank and whitespace-only required inputs retry; existing values remain valid defaults | `TestSetupRejectsEmptyRequiredFields`, `TestSetupKeepsExistingRequiredValues` |
| Reset backs up config, mappings, current log, and rotated logs; unrelated JSON survives | `TestReset` |
| An unavailable child addon junction prevents completion; the unchanged plan retries after recovery | `TestUnavailableAddonStaysRetryable` |
| Log lock contention has a fixed bound and cannot hold daemon shutdown indefinitely | `TestLogLockDoesNotBlockShutdown` |
| Reset cancels independently while waiting for config, mappings, or log locks without moving config | `TestResetLockWaitCancels` |
| Individual redirected stdin/stdout files and NUL handles survive console setup; invalid handles still bind a console | `TestConsoleRedirection` |
| Nested relative mappings, recursive wildcard components, and Python character classes retain manual override behavior | `TestNestedManualMapping`, `TestMappingPatterns`, `TestPythonMappingClasses` |
| Mappings follow directory junctions, preserve alternate aliases, and stop ancestor cycles | `TestManualMappingThroughJunction` |
| Canceled path validation stops repeated warnings; caller cancellation does not become a sticky logging failure | `TestCanceledValidationStopsLogging`, `TestCanceledLogContextKeepsCleanExit` |

The live recorded fixture contains the relevant sections of the actual SimBrief response.
On this response `params.time_generated` is present and no recognized OFPID field exists.
The extracted plan ID is therefore JSON null. The request ID is deliberately not treated as OFPID.

## Verified on this workstation

- Windows amd64 with Go 1.27.0.
- `go test ./...`, `go test -race ./...`, `go vet ./...`, and `gofmt -l .`.
- `CGO_ENABLED=0` release build and Linux amd64/macOS amd64 cross-builds.
- Live SimBrief account `1072997`: KMIA to KLAX, A321, DAL.
- Read-only matching: 4 results in `D:\addonlinker`, 82 in `E:\addonlinker`.
- Supplied real Community path exists and is a directory. No writes were made there.
- Existing Python startup value and original Start Menu shortcut were preserved after tests.

## Intentional fixes and required interface changes

1. Python's tray Show action uses Tkinter. Go opens a native console and follows the rotating log.
   The viewer is a separate instance of the same exe, so closing its console cannot kill the daemon.
2. Python and pip commands disappear. Help and error messages use `sblink.exe`.
   Tray label `Run sblink.bat` becomes `Run setup`; no batch file is required.
   Setup reproduces the Python banner, panels, five steps, labels, hints, choices, and completion screen.
   Native VT sequences supply the original palette and screen clearing without a UI library.
   A built-in command-processor console launch triggers Windows default-terminal delegation;
   direct AllocConsole from the GUI executable did not select the user's terminal on this workstation.
3. No argument chooses setup without config and run with config, as requested.
   Setup generates named Run and Setup shortcuts beside the executable.
4. Creation refuses to replace any unmarked junction or real item. Python resolves an existing
   link before inspecting it and can remove foreign links or attempt to operate on a real target.
5. Marker failures cause rollback and retry. Cleanup removes links before markers and snapshots
   ownership first, so shared targets do not leave the second owned link behind. Markers must be
   regular files, not links. Marker writes replace the file atomically without following a symlink.
6. Searches have deterministic ordering and cycle detection. Aircraft-specific terms precede family
   terms for manual mapping, resolving Python set-order ambiguity. Otherwise BFS depth semantics
   match Python exactly: root children are depth 0 and children of depth-5 parents remain eligible.
7. Duplicate destination basenames use the first configured library in deterministic order, log a
   warning, and are counted once. Python skips duplicate names but compares against the original
   match count and can retry forever.
8. Malformed sections and numeric values cannot panic. Invalid config falls back to defaults for
   display and blocks daemon/manual startup. Saving refuses to overwrite damaged JSON silently;
   use `reset` and then `setup` to recover. Invalid library counts are normalized consistently.
9. Config and mappings use atomic replacements with file sync and process locks. Config updates
   merge against the latest disk snapshot. Both processed identifiers are saved in one operation;
   memory is updated only after persistence succeeds. Interrupted setup does not save partial answers.
10. The 120-second cycle context reaches HTTP, retries, search, and link boundaries. Cancellation
    joins the daemon worker. Python's executor context waits for its worker after reporting timeout.
11. MSFS is checked again after the scan and before each new link. No unfinished cycle is marked
    processed. Native process enumeration replaces spawning tasklist. Explicit unlink keeps the
    original behavior and remains available while MSFS runs.
12. One daemon and one link cycle per executable directory prevent competing writers. Tray actions
    queue to the daemon worker. Log/config/mappings/reset operations have separate process locks.
    Poll-time config, mappings, and log-reader lock waits honor context cancellation.
13. Reset touches only sblink files, includes rotated logs, and refuses to run during a daemon or
    active link cycle. Python moves every JSON/log in its directory and can back up unrelated data.
14. One-shot fetch/link failures return exit 1 instead of silently claiming a successful manual sync.
    Windows startup/shortcut errors also fail setup visibly. Successful one-shot actions return 0.
15. Existing damaged mappings are never overwritten with examples. Incomplete directory scans and
    cancellation at completion return errors without advancing persisted or in-memory plan state.
16. Settings reload before daemon cycles. Account/path changes reprocess the current plan and interval
    changes update the next wait. Setup detects an already-running daemon rather than starting a duplicate.
17. Console control handlers are registered after allocation because Windows resets the runtime handlers.
    Ctrl+Break interrupts pending wizard input. Console-close handlers wait for entry cleanup and the tray
    handles session-end messages through the worker cancellation path.

## Remaining manual checks and limits

- No separate clean Windows VM with Go and Python physically uninstalled was available. The
  tested exe is CGO-free, has only system DLL imports, and runs with Go/Python removed from PATH.
- No actual MSFS launch/close session was performed. Pause/resume is tested through injected
  process state and native detection through a temporary process with the expected image name.
- Actual Windows sign-in autostart was not tested. Tests inspect the real Run registry value and
  resolve generated shortcuts through WScript.Shell, then restore the previous value.
- Tray popup creation and all action handlers are automated through native Windows messages.
  Physical mouse selection, Explorer restart recovery, and multi-monitor/high-DPI icon appearance
  need a manual smoke check. The two-link icon is generated in the exe without an image library.
- Native tests now observe PseudoConsoleWindow for setup, Show, and visible daemon sessions with the
  workstation's registered Windows Terminal. No terminal setting was changed. Other Windows versions
  and physical window appearance across terminal fonts and themes were not separately tested.
- Power loss was not injected during writes. The implementation syncs the temporary file before
  Windows MoveFileEx with replace-existing/write-through; tests cover concurrent writers and malformed JSON.
- Context cancellation is checked between filesystem operations. An OS filesystem call blocked
  by an unavailable drive cannot itself be interrupted through the standard Go filesystem API.
- Junction targets must be local Windows paths. Network-share junction targets are unsupported by NTFS.
- Linux and macOS binaries were cross-built, not executed on separate hosts.

Relevant native API contracts:
[junction reparse buffers](https://learn.microsoft.com/en-us/windows-hardware/drivers/ddi/ntifs/ns-ntifs-_reparse_data_buffer),
[notification icons](https://learn.microsoft.com/en-us/windows/win32/api/shellapi/ns-shellapi-notifyicondataw),
[native console allocation](https://learn.microsoft.com/en-us/windows/console/allocconsole),
[console window handles and pseudoconsoles](https://learn.microsoft.com/en-us/windows/console/getconsolewindow).

The setup text fixtures are exported from the actual Panel expressions in the Python reference using
Rich with `legacy_windows=False` and explicit width and height. Only Python command names in the
completion screen are replaced with `sblink.exe` before rendering. Python and Rich are used only to
generate these committed reference fixtures; Go tests and the release executable do not need them.

## Final follow-up validation on 2026-11-15

Fresh uncached tests, race tests, vet, formatting, and the release build pass. Linux/macOS amd64
builds, vet, and test compilation pass. The actual distribution executable passes an isolated status
smoke test. Instrumented package coverage is 58.4%; separately built executable and native helper
processes are exercised without contributing to that percentage.

The production mapping normalizer matches Python for 39,620 generated and directed patterns over
836 names, totaling 33,122,320 comparisons with zero mismatches or invalid translations. This covers
compound and reversed ranges, negation exposed after range removal, literal punctuation, Unicode,
newlines, and ordinary wildcard syntax. The large generated differential fixture remains outside the
application; permanent tests retain focused regressions. Mapping fuzzing executes 281,796 cases
without failure, in addition to 891,822 aircraft fuzz cases.

The independent final review and all dimension scores are recorded in [CODE_REVIEW.md](CODE_REVIEW.md).
The user confirmed that `sblink-V1.2/` remains an unchanged archival reference.
