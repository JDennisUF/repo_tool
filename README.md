# rt

Terminal UI utility for tracking many Git repositories and running actions across selected repos.

## Current MVP

- Persistent list of tracked repositories
- Add one repository by path (`o`)
- Scan a directory recursively for repositories (`s`)
- Multi-select repositories with keyboard
- Select visible repositories (`a`) and deselect all (`A`)
- Pull all selected repositories (`p`)
- Fetch selected repositories, or the highlighted repository if none are selected (`h`)
- Open lazygit in highlighted repository (`z`)
- Open highlighted repository in VS Code (`v`) or Zed (`Z`)
- Favorites lists, filtering, and batch actions
- Search in repos, themes, and command output (`/`)
- Settings dialog (`,` or `S`)
- Gerrit settings for username, server, and base git directory
- Gerrit project browser with a scrollable checkbox list for large `ls-projects` results
- Clone tracked Gerrit repos on demand after tracking them
- Lightweight remote branch tracking from local `origin/*` refs after refresh/fetch/pull/clone
- Toggle repo info panel (`+`)
- Two focusable sections (`0`, `1`)
- JSON-backed color themes
- Theme selector (`T`)
- Help screen (`?`)

## Build and Run

```bash
go build -o rt ./cmd/rt
./rt
```

## Keymap

- `j` | `k` or arrow keys: move highlight
- `Tab` or right arrow: cycle focus
- left arrow: cycle focus backward
- `space`: toggle selection on highlighted repo
- `a`: select visible repos
- `A`: deselect all repos
- `o`: add one repo by path (supports drag-and-drop path paste)
- `s`: scan/search a root directory for repos
- `g`: load Gerrit projects and open the project picker
- `p`: pull selected repos, or the highlighted repo if none are selected
- `c`: clone selected uncloned Gerrit repos, or the highlighted repo if none are selected
- `h`: fetch selected repos, or the highlighted repo if none are selected
- `z`: launch lazygit on highlighted repo
- `v`: launch VS Code on highlighted repo
- `Z`: launch Zed on highlighted repo
- `f`: toggle favorites-only filter
- `F`: toggle favorite on highlighted repo
- `l`: open favorites lists
- `/`: search current surface (repos, theme selector, or command output)
- `,` | `S`: open settings
- `+`: toggle repo info panel
- `0`: focus repositories
- `1`: focus command output
- `j` | `k`: scroll command output when output is focused
- `Enter`: maximize command output from repos or output focus; press again to restore
- `T`: open the theme selector; `j` | `k` previews, `Enter` selects, `Esc` cancels
- `?`: show/hide help dialog
- `Enter` or `Esc`: close help dialog
- `q` or `Ctrl+C`: quit from anywhere

## Themes

Default themes live in `internal/ui/themes.json`. To override or add themes without rebuilding, create:

```text
~/.config/rt/themes.json
```

Use the same JSON shape:

```json
{
  "activeTheme": "graphite",
  "themes": {
    "graphite": {
      "background": "#0B0F14",
      "foreground": "#F3F4F6",
      "muted": "#A7B0BE",
      "border": "#4B5563",
      "borderFocus": "#60A5FA",
      "header": "#FCD34D",
      "accent": "#38BDF8",
      "selection": "#FBBF24",
      "success": "#34D399",
      "error": "#FB7185",
      "warning": "#F59E0B",
      "status": "#111827",
      "statusText": "#FFFFFF",
      "cursor": "#60A5FA",
      "input": "#C084FC",
      "rowFocusBg": "#374151"
    }
  }
}
```

Set `RT_THEME=cobalt` to choose a named theme at launch.

The `background` value controls the full app canvas, including otherwise empty terminal space.

## Notes

- Repositories are persisted at:
  - Linux: `~/.config/rt/repos.json`
- Gerrit settings are stored in the same config file and are now editable from the settings dialog.
- Pull uses `git pull --ff-only` for safer batch updates.
- Fetch uses `git fetch --all --prune`.
- Clone uses `git clone <remote> <path>`.
- `lazygit` must be installed and in `PATH` for `z` to work.
