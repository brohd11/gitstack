package repoui

import (
	"context"
	"time"

	"github.com/brohd11/bubblestack/core"
	"github.com/brohd11/gitstack/repo"

	tea "charm.land/bubbletea/v2"
)

// FetchTimeout caps a whole fetch-all fan-out, so an unreachable remote can't leave the pass
// pending (and the consumer stuck marked as fetching) forever.
const FetchTimeout = 90 * time.Second

// FetchDoneMsg carries fetch results as a PropagateAll broadcast, so it reaches
// the consumer from any tab. FetchState stamps its own runs with private ownership.
type FetchDoneMsg struct {
	Results []repo.FetchResult
	owner   *FetchState
	gen     uint64
}

// FetchState owns one background fetch at a time. Keep it on the app context so
// root rebuilds do not lose ownership. The zero value is ready to use; do not copy
// it after first use. All methods run on the UI thread, never in a worker.
type FetchState struct {
	gen     uint64
	running bool
}

// Running reports whether the current run is waiting for its completion message.
func (f *FetchState) Running() bool { return f.running }

// Start claims a new run before returning its command. An active run returns
// nil, false without calling gather or scheduling more work. Like FetchAllCmd,
// gather runs in the worker and must not touch Shared or mutable app state.
func (f *FetchState) Start(gather func() []repo.Repo, app ...string) (tea.Cmd, bool) {
	if f.running {
		return nil, false
	}
	f.gen++
	f.running = true
	return fetchAllCmd(gather, FetchDoneMsg{owner: f, gen: f.gen}, app...), true
}

// Complete releases ownership once for the matching active run. Unowned,
// foreign, stale and duplicate messages have no effect. Call this before logging
// results or scheduling a local refresh, even when Results is empty or has errors.
func (f *FetchState) Complete(msg FetchDoneMsg) bool {
	if !f.running || msg.owner != f || msg.gen != f.gen {
		return false
	}
	f.running = false
	return true
}

// FetchAllCmd fetches the repos gather returns concurrently (repo.FetchAll, bounded by
// FetchTimeout) off the UI thread, then broadcasts FetchDoneMsg. gather runs in the
// goroutine, so it may do disk work but must not touch Shared. An optional app
// name enables that app's Git hooks, just as GitHookApp does for task screens.
func FetchAllCmd(gather func() []repo.Repo, app ...string) tea.Cmd {
	return fetchAllCmd(gather, FetchDoneMsg{}, app...)
}

func fetchAllCmd(gather func() []repo.Repo, done FetchDoneMsg, app ...string) tea.Cmd {
	// Capture hook selection now, alongside ownership, rather than reading caller
	// arguments or any FetchState fields when the command eventually runs.
	hookApp := core.GetOptional("", app...)
	return func() tea.Msg {
		repos := gather()
		ctx, cancel := context.WithTimeout(context.Background(), FetchTimeout)
		defer cancel()
		if hookApp != "" {
			ctx = repo.WithHookApp(ctx, hookApp)
		}
		result := done
		result.Results = repo.FetchAll(ctx, repos)
		return core.PropagateAll(result)
	}
}

// LogFetchResults logs one line per result and returns the summary status (forcing the log
// open only on failure), or emptyMsg for no results. noun labels the count.
func LogFetchResults(sh *core.Shared, results []repo.FetchResult, noun, emptyMsg string) core.Action {
	if len(results) == 0 {
		return core.SetStatus(emptyMsg)
	}
	for _, r := range results {
		for _, line := range r.Output {
			sh.Log("[" + r.Name + "] " + line)
		}
		sh.Log(repo.FetchLine(r))
	}
	line, failed := repo.FetchSummary(results, noun)
	return core.SetStatusAndLog(line, failed)
}
