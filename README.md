# repotui

Terminal UI utility for tracking many Git repositories and running actions across selected repos.

## Current MVP

- Persistent list of tracked repositories
- Add one repository by path (`o`)
- Scan a directory recursively for repositories (`s`)
- Multi-select repositories with keyboard
- Select all (`a`) and deselect all (`A`)
- Pull all selected repositories (`p`)
- Open lazygit in highlighted repository (`z`)
- Help screen (`?`)

## Build and Run

```bash
go build -o repotui ./cmd/repotui
./repotui
```

## Keymap

- `j`/`k` or arrow keys: move highlight
- `space`: toggle selection on highlighted repo
- `a`: select all repos
- `A`: deselect all repos
- `o`: add one repo by path (supports drag-and-drop path paste)
- `s`: scan/search a root directory for repos
- `p`: pull selected repos
- `z`: launch lazygit on highlighted repo
- `?`: show/hide help screen
- `q` or `Ctrl+C`: quit

## Notes

- Repositories are persisted at:
  - Linux: `~/.config/repotui/repos.json`
- Pull uses `git pull --ff-only` for safer batch updates.
- `lazygit` must be installed and in `PATH` for `z` to work.
