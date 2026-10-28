# gitstack

A domain-neutral git engine plus reusable git-viewing TUI screens — the shared git layer behind
[gdaddon](https://github.com/brohd11/gdaddon) and [repoview](https://github.com/brohd11/repoview).

- **`repo/`** — the engine: discover checkouts under a directory
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

### Cached file status consumers

`repo.ReadWorktreeStatus(ctx, dir)` returns the enclosing checkout's root, per-file
index/worktree states, rename origins, conflicts, untracked files, and ignored-path
coverage. It uses NUL-delimited porcelain v2 and supports worktrees and submodules.
Run it in a background task with a deadline and cache the snapshot for rendering.
`repo.RepoRootContext` provides cancellable enclosing-repository discovery.
The existing `GitChanges` API retains its display-oriented behavior.

### Git action hooks

`repo.WithHookApp(ctx, "repoview")` enables user commands from
`~/.repoview/hooks/config.yml` for operations on that context. gdaddon selects
`"gdaddon"` and reads `~/.gdaddon/hooks/config.yml`. Other callers remain opted out.
Both apps call `repo.EnsureHooks(appName)` at startup to create a missing directory
and config file with all events empty and a commented command example. Existing files
are left untouched; deleting the file restores the empty defaults on the next startup.
The engine tolerates a missing config and re-reads it for every action;
invalid config aborts before Git runs.

```yaml
hooks:
  pre_tag:
    - command: python
      args: ["~/.gdaddon/hooks/check-plugin-version.py"]
  post_push:
    - command: python
      args: ["~/.gdaddon/hooks/notify-push.py"]
```

Supported events are `pre_`/`post_` for `fetch`, `pull`, `push`, `commit`, `tag`,
and `delete_tag`. A tag push uses the push events with `RM_TAG` populated.
Commands run sequentially at the checkout root with literal argv, inherited environment,
and `RM_REPO_ROOT`, `RM_REPO_NAME`, `RM_OPERATION`, `RM_HOOK`, `RM_TAG`, and
`RM_VERSION` (tag minus one leading `v`). `~/` and `~\` paths expand in commands
and arguments. Both output streams go to the reporter. No Godot checks are built in.

Pre-hook failures prevent Git from running. Post-hooks run only after Git succeeds;
their failures return `*repo.HookError` with `Completed: true`, preserving Git's changes.
Cancellation stops a running command. Commit pre-hooks run before staging. Read-only
probes and raw `GitStream` do not dispatch hooks; arbitrary `repos -- ...` commands
also bypass them. Git's native hooks are unaffected.

The TUI opts in through the optional `repoui.GitHookApp` capability on `Shared.App`.
`repoui.FetchAllCmd(gather, appName)` also enables hooks for quick concurrent fetches.
Fetch results carry captured output for per-repo logging. Batch actions run hooks for
each target separately and continue to the next repo after a failure.
