# gitstack

A domain-neutral git engine plus reusable git-viewing TUI screens — the shared git layer behind
[gdaddon](https://github.com/brohd11/gdaddon) and [repoview](https://github.com/brohd11/repoview).

- **`repo/`** — the engine (standard library only): discover checkouts under a directory
  (`FindGitRepos`, `Scan`), read status (`CurrentBranch`, `GitSyncStatus`, `HasUncommittedChanges`,
  `GitChanges`), read history (`Log`), and run operations (`GitFetch`, `FetchAll`, `GitPull`/`GitPush`/`GitCommit`/
  `GitStatus` over a streaming `Reporter`). Its central value is `Repo{Name, Dir, Branch, Sync, Dirty}`.
- **`repoui/`** — [bubblestack](https://github.com/brohd11/bubblestack) screens over the engine:
  `RepoMenu` (a per-repo status/diff/log/fetch/pull/push/commit submenu), `LogScreen` (the
  commit history, one-line or git's full form), `AllReposMenu` (a batch fetch/pull/push menu
  whose scopes the consumer supplies), and the `Task`/`RefreshMsg` plumbing.
  These name no application type, so any tool can drive them with `repo.Repo` values.

**Not a git client:** `pull` is fast-forward-only and anything needing a decision (a divergence,
a conflict, a rebase) fails having changed nothing, routing the user to a real terminal.

```go
import (
    "github.com/brohd11/gitstack/repo"
    "github.com/brohd11/gitstack/repoui"
)
```
