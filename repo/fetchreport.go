package repo

import "fmt"

// FetchLine describes one repo's fetch outcome for an output log — the per-repo line a
// caller prints after FetchAll (or a single GitFetch + GitSyncStatus).
func FetchLine(r FetchResult) string {
	switch {
	case r.Err != nil:
		return fmt.Sprintf("[%s] fetch failed: %v", r.Name, r.Err)
	case r.Sync.Behind > 0 && r.Sync.Ahead > 0:
		return fmt.Sprintf("[%s] fetched · %d behind, %d ahead", r.Name, r.Sync.Behind, r.Sync.Ahead)
	case r.Sync.Behind > 0:
		return fmt.Sprintf("[%s] fetched · %d behind", r.Name, r.Sync.Behind)
	case r.Sync.Ahead > 0:
		return fmt.Sprintf("[%s] fetched · %d ahead", r.Name, r.Sync.Ahead)
	default:
		return fmt.Sprintf("[%s] fetched · up to date", r.Name)
	}
}

// FetchSummary is the status line for a finished fetch-all: how many repos were fetched and
// what it turned up. noun is the plural label for what was fetched (e.g. "repo(s)" or
// "git checkout(s)"), so each tool keeps its own wording. failed reports whether anything
// errored — the per-repo reason is in the log (FetchLine), so a caller uses it to decide
// whether to force the log pane open.
func FetchSummary(results []FetchResult, noun string) (line string, failed bool) {
	behind, failedN := 0, 0
	for _, r := range results {
		if r.Err != nil {
			failedN++
			continue
		}
		if r.Sync.Behind > 0 {
			behind++
		}
	}
	line = fmt.Sprintf("fetched %d %s", len(results)-failedN, noun)
	if behind > 0 {
		line += fmt.Sprintf(" · %d behind origin", behind)
	}
	if failedN > 0 {
		line += fmt.Sprintf(" · %d failed", failedN)
	}
	return line, failedN > 0
}
