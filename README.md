# Codex Session Doctor

Windows-only repair tool for the Codex Desktop session bug where old sessions fail to render because saved text contains `::git-*{cwd="C:\..."}` markers.

## Use

Close Codex Desktop first, then double-click `Codex Session Doctor.exe`.

## Menu

- `1. Scan only`: checks affected Codex session files without writing changes.
- `2. Repair affected sessions`: recommended first; repairs high-confidence Windows-path git markers.
- `3. Repair all standalone git markers`: broader repair; use only if option 2 is not enough.
- `4. Switch language`: toggles English and Chinese.
- `5. Exit`: closes the tool.

After scanning or repairing, the tool returns to the menu.

## Safety

- The tool scans the default local Codex session folders.
- It parses `.jsonl` files before modifying them.
- Every changed file is backed up before writing.
- Invalid JSONL files are skipped instead of stopping the whole repair.

## Backups

Backups are saved next to repaired files with names like:

```text
session.jsonl.bak-2026-05-16T12-34-56-789Z
```

To restore, close Codex Desktop and copy the backup over the repaired `.jsonl` file.

## Links

- https://linux.do
