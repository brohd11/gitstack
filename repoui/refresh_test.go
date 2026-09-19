package repoui

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"charm.land/bubbles/v2/list"
	"github.com/brohd11/bubblestack/components"
	"github.com/brohd11/bubblestack/core"
	"github.com/brohd11/gitstack/repo"
)

func TestRepoRefreshPaths(t *testing.T) {
	base := t.TempDir()
	target := filepath.Join(base, "foo")
	m := RepoRefreshMsg{Dir: target + string(filepath.Separator) + "."}
	for _, tc := range []struct {
		dir              string
		target, affected bool
	}{
		{target, true, true},
		{base, false, true},
		{filepath.Join(base, "foobar"), false, false},
		{filepath.Join(target, "child"), false, false},
		{"", false, false},
	} {
		if m.Targets(tc.dir) != tc.target || m.Affects(tc.dir) != tc.affected {
			t.Errorf("unexpected match for %q", tc.dir)
		}
	}
	if (RepoRefreshMsg{}).Affects(base) || (RepoRefreshMsg{}).Targets(base) {
		t.Fatal("empty messages must not resolve to the working directory")
	}
}

type refreshRecorder struct{ messages []any }

func (r *refreshRecorder) Receive(_ *core.Shared, payload any) core.Action {
	r.messages = append(r.messages, payload)
	return core.Action{}
}

func TestTaskRefreshRouting(t *testing.T) {
	dir := t.TempDir()
	for _, tc := range []struct {
		name  string
		op    Op
		err   error
		batch bool
	}{
		{"fetch", opFetch, nil, false},
		{"status", opStatus, nil, false},
		{"pull", opPull, nil, false},
		{"commit", opCommit, nil, false},
		{"push", opPush, nil, false},
		{"tag", opTag, nil, false},
		{"delete", opDelete, nil, false},
		{"failure", opPull, errors.New("failed"), false},
		{"batch", opFetch, nil, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			recorder := &refreshRecorder{}
			sh := core.NewShared(recorder)
			sh.Chrome = &core.Chrome{Status: components.NewStatusLine()}
			router := core.NewRouter(sh, []core.TabEntry{{Title: "test", New: func(*core.Shared) core.Screen {
				return components.NewPicker(nil, components.PickerOpts{})
			}}})
			op := func(context.Context, string, repo.Reporter) error { return nil }
			task := Task("test", tc.op, dir, op)
			if tc.batch {
				task = newBatchTask(nil, 0, RootOption{}, false, tc.op, "test", op)
			}
			_, act := task.Update(sh, core.TaskEvent{Done: true, Err: tc.err})
			router.Update(act)
			if tc.err != nil {
				if len(recorder.messages) != 0 {
					t.Fatal("failed task unexpectedly refreshed")
				}
				return
			}
			if len(recorder.messages) != 1 {
				t.Fatalf("messages = %#v", recorder.messages)
			}
			if tc.batch {
				if _, ok := recorder.messages[0].(RefreshMsg); !ok {
					t.Fatalf("batch emitted %T", recorder.messages[0])
				}
			} else if got, ok := recorder.messages[0].(RepoRefreshMsg); !ok || got.Dir != dir {
				t.Fatalf("single task emitted %#v", recorder.messages[0])
			}
		})
	}
}

func TestRepoScreensFilterRefresh(t *testing.T) {
	dir := t.TempDir()
	r := repo.Repo{Name: "test", Dir: dir}
	sh := core.NewShared(nil)
	for name, screen := range map[string]*components.PickerScreen{
		"git": RepoMenu(sh, r), "diff": DiffMenu(sh, r),
	} {
		t.Run(name, func(t *testing.T) {
			screen.SetItems([]list.Item{components.Item{Name: "sentinel"}})
			screen.Receive(sh, RepoRefreshMsg{Dir: filepath.Join(filepath.Dir(dir), "unrelated")})
			if screen.List().Items()[0].FilterValue() != "sentinel" {
				t.Fatal("unrelated refresh handled")
			}
			for _, payload := range []any{RepoRefreshMsg{Dir: dir}, RepoRefreshMsg{Dir: filepath.Join(dir, "child")}, RefreshMsg{}} {
				screen.SetItems([]list.Item{components.Item{Name: "sentinel"}})
				screen.Receive(sh, payload)
				if screen.List().Items()[0].FilterValue() == "sentinel" {
					t.Fatal("matching refresh ignored")
				}
			}
		})
	}
}

func TestTagsScreenTargetedRefresh(t *testing.T) {
	dir := t.TempDir()
	git := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
		cmd.Env = append(os.Environ(), "GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t", "GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	git("init", "-q", "-b", "main")
	git("commit", "--allow-empty", "-qm", "initial")
	sh := core.NewShared(nil)
	screen := TagsScreen(sh, repo.Repo{Name: "tags", Dir: dir})
	screen.SetSize(sh, 100, 30)
	git("tag", "v1.2.3")
	screen.Receive(sh, RepoRefreshMsg{Dir: filepath.Join(dir, "..", "other")})
	if strings.Contains(screen.View(sh), "v1.2.3") {
		t.Fatal("unrelated refresh updated local tags")
	}
	screen.Receive(sh, RepoRefreshMsg{Dir: dir})
	if !strings.Contains(screen.View(sh), "v1.2.3") {
		t.Fatal("targeted refresh left stale local tags")
	}
	git("tag", "v1.2.4")
	screen.Receive(sh, RefreshMsg{})
	if !strings.Contains(screen.View(sh), "v1.2.4") {
		t.Fatal("full refresh left stale local tags")
	}
}
