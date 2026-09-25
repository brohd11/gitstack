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

// FetchDoneMsg carries FetchAllCmd's results as a PropagateAll broadcast, so it reaches the
// consumer from any tab.
type FetchDoneMsg struct{ Results []repo.FetchResult }

// FetchAllCmd fetches the repos gather returns concurrently (repo.FetchAll, bounded by
// FetchTimeout) off the UI thread, then broadcasts FetchDoneMsg. gather runs in the
// goroutine, so it may do disk work but must not touch Shared.
func FetchAllCmd(gather func() []repo.Repo) tea.Cmd {
	return func() tea.Msg {
		repos := gather()
		ctx, cancel := context.WithTimeout(context.Background(), FetchTimeout)
		defer cancel()
		return core.PropagateAll(FetchDoneMsg{Results: repo.FetchAll(ctx, repos)})
	}
}

// LogFetchResults logs one line per result and returns the summary status (forcing the log
// open only on failure), or emptyMsg for no results. noun labels the count.
func LogFetchResults(sh *core.Shared, results []repo.FetchResult, noun, emptyMsg string) core.Action {
	if len(results) == 0 {
		return core.SetStatus(emptyMsg)
	}
	for _, r := range results {
		sh.Log(repo.FetchLine(r))
	}
	line, failed := repo.FetchSummary(results, noun)
	return core.SetStatusAndLog(line, failed)
}
