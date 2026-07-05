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

- `j`/`k` or arrow keys: move highlight
- `Tab` or right arrow: cycle focus
- left arrow: cycle focus backward
- `space`: toggle selection on highlighted repo
- `a`: select visible repos
- `A`: deselect all repos
- `o`: add one repo by path (supports drag-and-drop path paste)
- `s`: scan/search a root directory for repos
- `p`: pull selected repos
- `h`: fetch selected repos, or the highlighted repo if none are selected
- `z`: launch lazygit on highlighted repo
- `v`: launch VS Code on highlighted repo
- `Z`: launch Zed on highlighted repo
- `f`: toggle favorites-only filter
- `F`: toggle favorite on highlighted repo
- `l`: open favorites lists
- `/`: search current surface (repos, theme selector, or command output)
- `,` or `S`: open settings
- `+`: toggle repo info panel
- `0`: focus repositories
- `1`: focus command output
- `j`/`k`: scroll command output when output is focused
- `Enter`: toggle command output maximized/normal from repos or output focus
- `T`: open the theme selector; `j`/`k` previews, `Enter` selects, `Esc` cancels
- `?`: show/hide help dialog
- `Enter` or `Esc`: close help dialog
- `q` or `Ctrl+C`: quit

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
- Pull uses `git pull --ff-only` for safer batch updates.
- Fetch uses `git fetch --all --prune`.
- `lazygit` must be installed and in `PATH` for `z` to work.
