package repoui

import (
	"context"
	"time"

	"github.com/brohd11/bubblestack/core"
	"github.com/brohd11/gitstack/repo"

	tea "github.com/charmbracelet/bubbletea"
)

// FetchTimeout caps a whole fetch-all fan-out, so an unreachable remote can't leave the pass
// pending (and the consumer stuck marked as fetching) forever.
const FetchTimeout = 90 * time.Second

// FetchDoneMsg carries a FetchAllCmd pass's per-repo results back to the consumer via a
// PropagateAll broadcast, where LogFetchResults renders them and the list rebuilds from the
// freshly updated refs. It reaches every Receiver, so it lands on the cached root even from
// another tab.
type FetchDoneMsg struct{ Results []repo.FetchResult }

// FetchAllCmd runs `git fetch` concurrently (repo.FetchAll) across the repos gather returns,
// off the UI thread and capped by FetchTimeout, then broadcasts FetchDoneMsg with the
// results. gather is called inside the goroutine so a consumer can do its disk work there
// (e.g. inspect a manifest, scan a directory) without blocking the UI; it must not touch
// Shared, which belongs to the UI thread. The repo set gather returns is fetched verbatim —
// callers include only the checkouts worth fetching (and any root repo they want along).
func FetchAllCmd(gather func() []repo.Repo) tea.Cmd {
	return func() tea.Msg {
		repos := gather()
		ctx, cancel := context.WithTimeout(context.Background(), FetchTimeout)
		defer cancel()
		return core.PropagateAll(FetchDoneMsg{Results: repo.FetchAll(ctx, repos)})
	}
}

// LogFetchResults writes one FetchLine per result to the log and returns the summary status
// Action (SetStatusAndLog forces the log open only when something failed). An empty result
// set logs nothing and returns emptyMsg on the status line. noun labels the count in the
// summary ("repo(s)", "git checkout(s)"). Callers combine the returned Action with their own
// rescan (e.g. via core.Seq).
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
