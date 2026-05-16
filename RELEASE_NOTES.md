# Codex Session Doctor v1.0.0

Initial release.

- Standalone Windows executable, no runtime dependencies.
- English and Chinese interactive menu.
- Auto-detects Windows UI language.
- Scans Codex Desktop session JSONL files.
- Repairs persisted `::git-*{cwd="C:\..."}` marker lines that can crash session rendering.
- Creates a backup before every write.
- Provides a broader repair mode for all standalone git markers if the default repair is not enough.
