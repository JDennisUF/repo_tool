# Gerrit Implementation Progress

This file tracks the Gerrit work so the implementation can resume cleanly.

## Checklist

- [x] Create a resumable implementation checklist in-repo
- [x] Extend persisted settings for Gerrit username, Gerrit server, and base git directory
- [x] Extend tracked repo state to support Gerrit-backed repos that are not cloned yet
- [x] Add Gerrit project discovery commands and parsing
- [x] Add clone command support for tracked Gerrit repos
- [x] Update repo/favorites identity handling to avoid assuming local path is the only key
- [x] Expand the settings dialog to edit Gerrit fields
- [x] Add a Gerrit project picker and selection flow
- [x] Add clone actions in the main UI
- [x] Update help text and README
- [x] Add focused tests for state migration, Gerrit parsing, and clone behavior

## Notes

- Current app behavior is path-centric. Gerrit support needs repo identity to survive before and after a local clone exists.
- Favorites and repo metadata caches currently key off local path; they will need to move to a stable repo key.
- The app now stores Gerrit project identity separately from local checkout path and can preserve tracked repos that are not cloned yet.
- The Gerrit picker now renders the full `ls-projects` result in a scrollable checkbox list and tracks checked projects on `Enter`.
