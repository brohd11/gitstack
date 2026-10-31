package repoui

import (
	"sync"
	"testing"
	"time"

	"github.com/brohd11/bubblestack/core"
	"github.com/brohd11/gitstack/repo"

	tea "charm.land/bubbletea/v2"
)

func fetchPayload(t *testing.T, cmd tea.Cmd) FetchDoneMsg {
	t.Helper()
	var done FetchDoneMsg
	count := 0
	core.DrainBroadcasts(core.Async(cmd), func(payload any) core.Action {
		var ok bool
		done, ok = payload.(FetchDoneMsg)
		if !ok {
			t.Fatalf("fetch returned %T", payload)
		}
		count++
		return core.Action{}
	})
	if count != 1 {
		t.Fatalf("got %d completions, want 1", count)
	}
	return done
}

func TestFetchStateOwnership(t *testing.T) {
	var state, foreign FetchState
	if state.Running() || state.Complete(FetchDoneMsg{}) {
		t.Fatal("zero state must be idle and reject bare completion")
	}
	gatherCalls := 0
	gather := func() []repo.Repo { gatherCalls++; return nil }
	firstCmd, ok := state.Start(gather)
	if !ok || firstCmd == nil || !state.Running() || gatherCalls != 0 {
		t.Fatal("Start must claim synchronously without gathering")
	}
	if cmd, ok := state.Start(gather); ok || cmd != nil || gatherCalls != 0 {
		t.Fatal("duplicate Start scheduled work")
	}
	otherCmd, _ := foreign.Start(func() []repo.Repo { return nil })
	other := fetchPayload(t, otherCmd)
	if state.Complete(other) || !state.Running() || !foreign.Running() {
		t.Fatal("foreign completion released a run with the same generation")
	}
	first := fetchPayload(t, firstCmd)
	if gatherCalls != 1 || !state.Running() {
		t.Fatal("worker must gather once and leave ownership for UI completion")
	}
	if !state.Complete(first) || state.Running() || state.Complete(first) {
		t.Fatal("matching completion must release ownership exactly once")
	}
	secondCmd, ok := state.Start(gather)
	if !ok {
		t.Fatal("completion must allow the next run")
	}
	legacy := fetchPayload(t, FetchAllCmd(func() []repo.Repo { return nil }))
	for _, rejected := range []FetchDoneMsg{{}, first, other, legacy} {
		if state.Complete(rejected) || !state.Running() {
			t.Fatal("unowned, stale or foreign completion released the new run")
		}
	}
	second := fetchPayload(t, secondCmd)
	if second.gen == first.gen || !state.Complete(second) || !foreign.Complete(other) {
		t.Fatal("successive runs must carry distinct generations and retain their owners")
	}
}

func TestFetchStateDelayedGather(t *testing.T) {
	var state FetchState
	started, release := make(chan struct{}), make(chan struct{})
	var once sync.Once
	unblock := func() { once.Do(func() { close(release) }) }
	t.Cleanup(unblock)
	cmd, _ := state.Start(func() []repo.Repo {
		close(started)
		<-release
		return nil
	})
	result := make(chan tea.Msg, 1)
	go func() { result <- cmd() }()
	select {
	case <-started:
	case <-time.After(3 * time.Second):
		t.Fatal("gather did not start")
	}
	if duplicate, ok := state.Start(func() []repo.Repo {
		t.Error("duplicate gather ran")
		return nil
	}); ok || duplicate != nil || !state.Running() || state.Complete(FetchDoneMsg{}) {
		t.Fatal("blocked gather lost ownership")
	}
	unblock()
	var msg tea.Msg
	select {
	case msg = <-result:
	case <-time.After(3 * time.Second):
		t.Fatal("fetch did not finish after gather was released")
	}
	done := fetchPayload(t, func() tea.Msg { return msg })
	if !state.Complete(done) || state.Running() {
		t.Fatal("delayed completion did not release ownership")
	}
}

func TestFetchStateEmptyAndFailedResults(t *testing.T) {
	for _, fail := range []bool{false, true} {
		name := "empty"
		if fail {
			name = "failed"
		}
		t.Run(name, func(t *testing.T) {
			var state FetchState
			missing := t.TempDir() + "/missing"
			cmd, _ := state.Start(func() []repo.Repo {
				if fail {
					return []repo.Repo{{Name: "missing", Dir: missing}}
				}
				return nil
			})
			done := fetchPayload(t, cmd)
			if fail && (len(done.Results) != 1 || done.Results[0].Err == nil) {
				t.Fatalf("expected failed local fetch: %+v", done.Results)
			}
			if !fail && len(done.Results) != 0 {
				t.Fatal("empty gather returned results")
			}
			if !state.Complete(done) || state.Running() {
				t.Fatal("empty or failed completion left the guard set")
			}
		})
	}
}
