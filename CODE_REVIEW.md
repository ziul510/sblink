# Production review and cleanup

Scope: all Go source, tests, build tooling, documentation, fixtures, and the Python reference.
No Git repository was present. Review started from an exact source snapshot with SHA-256 scope
`2211972DB1A610035905784979E1C3B9460A07AE39A47CB98DE6E9B17D43DC96`.
The Python reference was retained unchanged. No dependency was added and no application setting was added.

## Verified issues and fixes

| Finding | Fix | Regression coverage |
| --- | --- | --- |
| Malformed Unicode aircraft input could panic after uppercasing shortened its byte length | Normalize first and count characters before slicing | `TestMalformedAircraftDoesNotPanic`, `FuzzAircraftTerms` |
| Lost or unreadable libraries during a scan could mark the plan complete | Return directory resolution and enumeration failures | `TestFailedScanDoesNotMarkPlan` |
| Cancellation after the last search operation could still persist completion | Check cancellation after search and before persistence | `TestFailedScanDoesNotMarkPlan` |
| Polling could wait indefinitely for config, mapping, or viewer file locks | Retry nonblocking locks with context cancellation | `TestCanceledLockWait` |
| Invalid mappings were replaced with examples, losing user overrides | Create defaults only for an absent file and preserve damaged files | `TestDamagedMappingsStayUntouched` |
| Malformed primary identity fields suppressed valid alternate fields | Choose the first supported scalar while retaining null and empty distinctions | `TestIdentityFallsBackFromWrongType` and existing identity matrix |
| HTTP size limit could disguise trailing data as clean EOF | Read one byte beyond the bound and reject overflow | `TestOversizedResponseRejected` |
| Tray setup saved settings that the daemon never adopted | Reload before cycles, reprocess after account/path changes, and recalculate polling delay | `TestDaemonReloadsSettings` |
| Setup claimed a duplicate daemon had started | Detect the existing daemon and explain when settings and visibility apply | Scripted executable setup tests and code audit |
| Console allocation reset Go's native control handler | Register a handler after allocation, cancel pending input, and wait for cleanup on close | Real Ctrl+Break tests for setup and daemon |
| Tray did not handle Windows session end | Cancel and join the worker inside the session-end handler | Isolated session-end message test |
| Canceling tray setup could also interrupt a visible daemon sharing its console | Give tray setup its own console | Setup interruption followed by daemon tray actions |
| Classic-window visibility checks rejected delegated pseudoconsole terminals | Accept native pseudoconsole HWND semantics and remove forced ShowWindow calls | Console integration probe |
| Missing APPDATA could produce a relative Start Menu path | Resolve user folders through standard library APIs and fail safely when unavailable | `TestMissingAppDataFailsSafely` |
| Reused tray logs could mask failures in a later test case | Clear isolated test logs for each daemon instance and assert clean shutdown | Hidden and visible tray integration cases |
| Restarting before a settings reload could skip the current plan on changed paths | Clear both processed identifiers atomically when matching settings change | `TestMatchingSettingsInvalidateSavedPlan` |
| Cancellation during prompt output could miss a later console read | Recheck before input and retry input cancellation until cleanup completes | `TestSetupCanceledAtPromptDoesNotRead` and native Ctrl+Break tests |
| Setup during an active cycle could let that cycle restore obsolete processed identifiers | Verify matching settings under the config lock before recording completion | `TestSettingsChangedDuringCycleStayRetryable` |

## Safe removals and cleanup

- Removed unreachable BFS depth guard; only eligible parents enter the queue.
- Removed the first-truthy helper after identity fallback stopped using it.
- Removed the per-request regular expression and its import; the same ASCII identifier grammar is checked directly.
- Removed the unreachable HTTP retry fallthrough error.
- Removed forced ShowWindow calls that interfered with Windows terminal presentation.
- Consolidated repeated wait/clear/poll branches into one daemon loop.
- Named fixed polling and HTTP limits in code and retained existing configuration keys and file formats.
- Checked source comments for single-line form and absence of periods and dashes; build directives were retained.

Native Windows structure fields that appear unused were retained because their layout is required by the ABI.
The legacy Python source, example mappings, persisted identity fields, and compatibility aliases were retained.
Raw Win32 long-path changes were rejected after a 317-character path resolved successfully on the tested toolchain.

## Terminal behavior

Visible sessions launch a Windows command processor console and attach the same executable to it.
This corrects the direct GUI AllocConsole path that still opened classic conhost on this workstation.
Windows chooses the registered default terminal;
the app does not hardcode Windows Terminal, a user profile path, a terminal executable, or delegation GUIDs.
Existing terminal sessions and redirected streams are preserved for console commands.
Tray setup and Show use independent console sessions. Hidden daemons allocate no console.
Background visibility changes require a restart; other settings apply before the next cycle.
Wizard appearance now matches the reference Rich panels across every step and completion, with
18 reference layout fixtures, native VT colors, responsive width, and screen clearing.
Launcher and log-viewer process ownership keep nested terminal sessions from being orphaned.
Follow-up native tests caught write-only console handles preventing mode and width queries.
Output handles now include read access and terminal children explicitly bind their console streams.
Interactive colors are enabled even with an inherited NO_COLOR environment variable, while
redirected scripts remain plain. Required wizard inputs reject blanks when no saved default exists.

Native behavior references:
[Windows default terminal setting](https://learn.microsoft.com/en-us/windows/terminal/install),
[console handler resets](https://learn.microsoft.com/en-us/windows/console/setconsolectrlhandler),
[pseudoconsole HWND behavior](https://learn.microsoft.com/en-us/windows/console/getconsolewindow).

## Review process and limits

Initial review used seven detection perspectives across three independent reviewers and the main review:
two convention checks, direct bug detection, full caller tracing, concurrency/state, error handling, and test coverage.
Each reported issue was cross-checked against its callers and a reachable failure before modification.
Changes were followed by tests. A separate full-source second review and final audit check the resulting code.

Validation commands and environmental limits are documented in [VERIFICATION.md](VERIFICATION.md).
Live Community folders and existing user terminal preferences are not modified by the tests.
Windows startup tests restore the previous registry value and isolate shortcut writes.
No clean Windows VM, actual simulator flight session, real user sign-out, or power-loss injection is available.

## Follow-up gauntlet review on 2026-11-15

This audit starts from a separate complete filesystem snapshot with SHA-256 scope
`E2C6C0701CDE8CCD980FEABB1005E81E451FC24B8C74B4E7BE4F38A7DCC146FE`.
No Git history exists, so findings describe the supplied source rather than attributing them to a commit.
Seven detection roles covered the complete source and supporting files. Independent second-pass reviewers
found two additional defects after the initial six fixes, which triggered another improvement pass.

| Finding | Production change | Regression coverage |
| --- | --- | --- |
| Failed child metadata checks silently completed an incomplete addon search | Propagate child inspection errors and keep the plan retryable | `TestUnavailableAddonStaysRetryable` |
| A competing log lock could block shutdown indefinitely | Bound log lock acquisition with a fixed one-second timeout | `TestLogLockDoesNotBlockShutdown`, concurrent rotation tests |
| Reset ignored cancellation while waiting for config, mappings, and log locks | Pass the command context through lock acquisition and backup boundaries | `TestResetLockWaitCancels` |
| Partial redirection and NUL handles could be replaced by a new console | Detect valid non-console streams and preserve them individually | `TestConsoleRedirection` |
| Nested relative mapping patterns failed below the library root | Match relative patterns recursively, including wildcard components | `TestNestedManualMapping`, `TestMappingPatterns` |
| Mapped folders below junctions were unreachable | Follow directory targets with ancestor cycle detection and preserve aliases | `TestManualMappingThroughJunction` |
| Repeated validation logs could exceed the Windows cleanup window after cancellation | Carry context through validation and cycle logging, while retaining a bounded shutdown record | `TestCanceledValidationStopsLogging`, `TestCanceledLogContextKeepsCleanExit` |
| Python character classes could select the opposite addon | Translate Python class syntax before standard-library component matching | `TestPythonMappingClasses`, `TestMappingPatterns` |

The old mapping WalkDir callback, stop sentinel, and unused `io/fs` import were removed when the
mapping traversal was replaced. Repeated console stream classification now shares one helper.
No feature, user setting, persisted key, external dependency, or architecture layer was added.
The release executable is rebuilt from the reviewed Go source.

The Python directory is retained byte-for-byte as an archival compatibility reference. Its old
uncoordinated tray operations and permissive file handling are not production-certified. The Go
release does not execute Python. Running the reference itself is outside the release readiness score.

### Final validation and evaluation

Four full review passes completed. The final independent source snapshot is
`177FA8643DC6D5ABCCA39BE45DFF9DC4523A08B4ADDB6D9A2E94D8D6513BDB49`.
All seven detection perspectives completed; all eight confirmed root defects were cross-checked,
fixed, and covered by regressions. No unresolved high-confidence production defect remains.
The final self-audit checked every modification, removal, affected caller, and Go comment.

Fresh verification on Windows amd64 with Go 1.27.0:

- `go test -count=1 -coverprofile=... ./...`: passed, 58.4% instrumented statement coverage.
- `go test -count=1 -race ./...`: passed.
- `go vet ./...`, `gofmt -l .`, and snapshot whitespace checks: passed.
- `build.ps1`: passed; the actual rebuilt `dist/sblink.exe` passed an isolated status smoke test.
- Linux amd64 and macOS amd64 builds, vet, and test compilation: passed.
- Aircraft fuzzing: 891,822 executed cases without failure.
- Mapping normalization fuzzing: 281,796 executed cases without failure; valid UTF-8 inputs must produce valid patterns.
- The actual production matcher passed 33,122,320 comparisons against Python over 39,620 patterns and 836 candidate names.
- All 25 Go comments across 28 source and test files passed the requested comment rules.
- All eight Python reference and batch files retain their original SHA-256 hashes.

The package coverage percentage excludes work performed inside the separately built release executable
and native helper processes. Their command, tray, startup, console, and junction behaviors are exercised
by integration tests; no combined coverage percentage is claimed.

| Dimension | Final score | Evidence |
| --- | --- | --- |
| Correctness | 9.5 | Identity, selection, persistence, HTTP, executable, and regression tests |
| Stability | 9.5 | Fresh race suite and native lifecycle checks |
| Reliability | 9.5 | Retry state, bounded logging, cancellation, and config consistency |
| Robustness | 9.5 | Malformed input, unavailable directories, Unicode, and lock contention |
| Behavior preservation | 9.5 | Python layout fixtures, compatibility matrices, and differential mapping checks |
| Maintainability | 9.5 | Standard library only, existing layers retained, callers updated together |
| Readability | 9.5 | Small named helpers, formatted source, and concise checked comments |
| Consistency | 9.5 | Shared stream classification, context propagation, and error handling |
| Performance | 9.5 | Bounded polling, cycle detection, and matching without recursive backtracking |
| Error handling | 9.5 | Failed operations propagate without falsely completing plans |
| Edge case handling | 9.5 | Targeted regressions, differential tests, and fuzzing |
| Resource management | 9.5 | File/process ownership review, bounded waits, and shutdown tests |
| Feature integration | 9.5 | Release-style command, setup, tray, console, startup, and junction tests |

Overall gauntlet assessment: **9.5/10 for the supported Go release**. This is a source-review
assessment supported by the checks above, not a measured production failure rate.

Remaining limits: no clean Windows VM, actual simulator flight session, physical sign-in/sign-out,
power-loss injection, formal performance benchmark, or separate Linux/macOS execution. OS filesystem
and console calls remain synchronous. A log lock timeout can drop the affected line while reporting
the error. Broken library entries now intentionally prevent completion until repaired. Git ancestry
cannot be reviewed because no repository metadata was supplied. The archival Python app is not
included in the production score, as confirmed by the user.

Release SHA-256: `AF6E7DBEBA71DFF4001C83025CA3221DAE49D42A949883596D6F99506E1FA2F9`.
