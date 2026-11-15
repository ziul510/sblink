# Publishing

Build with `build.ps1`, then prepare a local `Publish/` folder containing the Windows x64
executable from `dist/`, a user guide, `LICENSE` renamed to `LICENSE.txt`, and checksums.
ZIP the contents as `sblink-windows-x64.zip`. Exclude source, personal config, mappings,
logs, and generated shortcuts. Release folders and archives are not tracked in Git.

Publish this as a standalone utility. Users extract it to a writable folder;
it is not a Community-folder package and does not support Community auto-install.
Before submitting, complete the antivirus verification required by Flightsim.to:
https://flightsim.to/support/article/general-rules-for-user-uploads

Source is published at https://github.com/zipz-z/sblink-go under MIT with Commons
Clause License Condition v1.0. This combination is source-available and should
not be described as OSI-approved open source. See LICENSE and
https://commonsclause.com/ for the condition text.

The public source tree excludes the historical Python implementation, tests, fixtures,
internal audit reports, distribution binaries, and user runtime files.
