# Publishing

`Publish/` contains the end-user release: the Windows x64 executable, user guide,
license, and checksums. ZIP the contents of this folder, without adding source,
personal config, mappings, logs, or generated shortcuts. A ready-made archive is
created as `sblink-windows-x64.zip` at the repository root.

Publish this as a standalone utility. Users extract it to a writable folder;
it is not a Community-folder package and does not support Community auto-install.
Before submitting, complete the antivirus verification required by Flightsim.to:
https://flightsim.to/support/article/general-rules-for-user-uploads

Source is published at https://github.com/zipz-z/sblink-go under MIT with Commons
Clause License Condition v1.0. This combination is source-available and should
not be described as OSI-approved open source. See LICENSE and
https://commonsclause.com/ for the condition text.

The Python directory remains an archival reference. The Go release does not
execute it. Distribution binaries and user runtime files are excluded from Git.
