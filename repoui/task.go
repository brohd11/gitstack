// Package repoui holds the domain-neutral git-viewing screens built on bubblestack and the
// sibling repo engine: a single-repo streaming task, the per-repo command submenu (status /
// fetch / pull / commit / push), and the all-repos batch menu. It names no manifest, addon,
// or app type — a consumer hands it repo.Repo values (and, for the batch menu, scope
// providers) and reacts to the RefreshMsg it broadcasts. gdaddon composes these behind a
// thin adapter; a plain repo viewer would wire them to a directory scan.
//
// The contract mirrors the engine's: pull is fast-forward-only, and any repo that would need
// a decision (a divergence, a conflict) fails with git's own words in the log and nothing is
// changed. Nothing here merges, rebases, or resolves — that belongs in a real terminal.
package repoui

import (
	"context"
	"strings"

	"github.com/brohd11/bubblestack/components"
	"github.com/brohd11/bubblestack/core"
	"github.com/brohd11/gitstack/repo"
)

// RefreshMsg is broadcast (via core.PropagateAll) after a git operation changes a checkout,
// so any screen showing git state can settle. repoui's own menus rebuild their rows on it;
// a consumer that caches git state (like gdaddon's project list) handles it to recompute
// dirty / ahead / behind. It carries no payload — it's a pure "reload yourself" marker.
type RefreshMsg struct{}

// Op is one git operation's vocabulary — present, past, and failure forms — declared once,
// side by side. The screens used to pass bare verb strings and bridge the forms with lookup
// functions (pastTense, failureLabel), where a typo silently degraded to verb+"ed"; now a
// screen passes an Op, so a misspelled verb is a compile error and every user-visible form
// of an operation lives next to its siblings.
type Op struct {
	present string // "pull" — mid-flow phrasing ("no repos to pull", "Pull 3 repo(s)")
	past    string // "pulled" — the success status; "" when the op only reports (status)
	failure string // "pull failed" — the failure status line
}

// The operations the screens run. opStatus's empty past is the marker Task reads as "only
// reports — no success status line of its own".
var (
	opStatus = Op{present: "status", failure: "git status failed"}
	opFetch  = Op{present: "fetch", past: "fetched", failure: "fetch failed"}
	opPull   = Op{present: "pull", past: "pulled", failure: "pull failed"}
	opPush   = Op{present: "push", past: "pushed", failure: "push failed"}
	opCommit = Op{present: "commit", past: "committed", failure: "commit failed"}
	opTag    = Op{present: "tag", past: "tagged", failure: "tag failed"}
	opDelete = Op{present: "delete", past: "deleted", failure: "delete failed"}
)

// Task runs one git operation on one repo, streaming its output into the shared log (report's
// lines land there and the pane reveals itself). It's a *stay* task: the whole point is to
// read what git said — especially when it refused — so the screen holds until esc rather than
// yanking the output away on completion.
//
// o supplies the status vocabulary: o.past is the success status ("pulled"), o.failure the
// failure one; an empty o.past means the operation only reports (status) and gets no success
// status line of its own. On success it broadcasts RefreshMsg so any list's markers settle.
func Task(label string, o Op, dir string, op func(context.Context, string, repo.Reporter) error) *components.TaskScreen {
	run := func(ctx context.Context, sh *core.Shared, report func(string, ...any), done chan<- core.TaskEvent) {
		done <- core.TaskEvent{Done: true, Err: op(ctx, dir, report)}
	}
	onDone := func(sh *core.Shared, ev core.TaskEvent) core.Action {
		if ev.Err != nil {
			// git's own words are already in the log above; the status line only has to say
			// where to go next. Nothing was changed — --ff-only and a failed push guarantee it.
			return core.SetStatusAndLog(o.failure + " — resolve it in a terminal (t)")
		}
		if o.past == "" {
			return core.PropagateAll(RefreshMsg{})
		}
		return core.Seq(
			core.SetStatus(o.past),
			core.PropagateAll(RefreshMsg{}),
		)
	}
	onDismiss := func(*core.Shared) core.Action { return core.PopTo() } // back to the hub
	ts := components.NewStayTask(label, "done — esc to go back", run, onDone, onDismiss)
	ts.Dir = dir // "t" opens a terminal at this repo — where a failed op says to resolve it (DirLocator)
	return ts
}

// titleWord capitalizes the first letter of an ASCII verb ("pull" → "Pull").
func titleWord(s string) string {
	if s == "" {
		return s
	}
	return strings.ToUpper(s[:1]) + s[1:]
}
