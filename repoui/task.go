// Package repoui holds domain-neutral git screens on bubblestack and repo: the streaming
// task, the per-repo Git menu and the batch menu. Consumers pass repo.Repo values (and
// scopes) and react to its refresh broadcasts. Pull is fast-forward only; anything needing
// a decision fails with git's message and changes nothing.
package repoui

import (
	"context"
	"strings"

	"github.com/brohd11/bubblestack/components"
	"github.com/brohd11/bubblestack/core"
	"github.com/brohd11/gitstack/repo"
)

// RefreshMsg is broadcast after a batch git operation so screens showing git state can
// reload. It carries no payload.
type RefreshMsg struct{}

// Op is one git operation's present, past and failure forms, declared together; screens
// pass an Op, so a misspelled verb fails to compile.
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

// Task runs one git operation on one repo, streaming into the log, and stays on screen
// until esc so git's output can be read. o.past is the success status (empty for
// report-only operations), o.failure the failure one. Success broadcasts RepoRefreshMsg.
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
			return core.PropagateAll(RepoRefreshMsg{Dir: dir})
		}
		return core.Seq(
			core.SetStatus(o.past),
			core.PropagateAll(RepoRefreshMsg{Dir: dir}),
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
