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

// FetchSummary is the status line for a finished fetch-all. noun is the plural label (e.g.
// "repo(s)"). failed reports any error, so a caller can open the log pane (per-repo
// reasons are there).
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
