package repo

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"
)

// Keep the filesystem cases portable: Windows rejects '>', control bytes, quotes and
// edge spaces in filenames, so the parser tests cover those bytes there instead.
func unusualNames() []string {
	names := []string{"has space.txt", "雪.txt"}
	if runtime.GOOS != "windows" {
		names = append(names, "literal -> arrow.txt", " leading and trailing ", "tab\tname", "line\nname", `quote"name`)
	}
	return names
}

func TestGitChangesUnusualPaths(t *testing.T) {
	dir := gutterRepo(t)
	git(t, dir, "checkout", "--", "tracked.txt")
	for _, name := range unusualNames() {
		write(t, dir, name, "before\n")
	}
	git(t, dir, "add", ".")
	git(t, dir, "commit", "-qm", "unusual paths")
	for _, name := range unusualNames() {
		write(t, dir, name, "after\n")
		write(t, dir, "new "+name, "untracked content\n")
	}
	write(t, dir, "ignored.txt", "ignored\n")
	changes, err := GitChanges(dir)
	if err != nil {
		t.Fatal(err)
	}
	want := make(map[string]string)
	for _, name := range unusualNames() {
		want[name] = " M"
		want["new "+name] = "??"
	}
	got := make(map[string]string)
	for _, c := range changes {
		got[c.Path] = c.Code
		out, err := Diff(dir, c.Path, c.Untracked())
		if err != nil || (!strings.Contains(out, "+after") && !strings.Contains(out, "+untracked content")) {
			t.Errorf("Diff(%q) = %q, %v", c.Path, out, err)
		}
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("changes = %#v, want %#v", got, want)
	}
	for _, c := range []bool{false, true} {
		out, err := Diff(dir, "", c)
		if err != nil || strings.Count(out, "+after\n") != len(unusualNames()) {
			t.Fatalf("aggregate diff (untracked=%v) = %q, %v", c, out, err)
		}
		wantNew := 0
		if c {
			wantNew = len(unusualNames())
		}
		if got := strings.Count(out, "+untracked content\n"); got != wantNew {
			t.Errorf("aggregate additions = %d, want %d", got, wantNew)
		}
	}
}

func TestDiffStatsUnusualPathsAndRename(t *testing.T) {
	dir := gutterRepo(t)
	git(t, dir, "checkout", "--", "tracked.txt")
	for _, name := range unusualNames() {
		write(t, dir, name, "before\n")
	}
	write(t, dir, "old name", strings.Repeat("unchanged\n", 20))
	git(t, dir, "add", ".")
	git(t, dir, "commit", "-qm", "paths and rename source")
	for _, name := range unusualNames() {
		write(t, dir, name, "after\nextra\n")
	}
	git(t, dir, "mv", "old name", "new name")
	write(t, dir, "new name", strings.Repeat("unchanged\n", 20)+"added\n")
	write(t, dir, "untracked", "absent from stats\n")
	stats, err := DiffStats(dir)
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]DiffStat{"new name": {Added: 1}}
	for _, name := range unusualNames() {
		want[name] = DiffStat{Added: 2, Deleted: 1}
	}
	if !reflect.DeepEqual(stats, want) {
		t.Fatalf("stats = %#v, want %#v", stats, want)
	}
}

func TestGitChangesDiscoveryAndErrors(t *testing.T) {
	dir := gutterRepo(t)
	sub := filepath.Join(dir, "sub")
	if err := os.Mkdir(sub, 0755); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{"", t.TempDir(), sub} {
		if got, err := GitChanges(path); got != nil || err != nil {
			t.Errorf("GitChanges(%q) = %+v, %v; want nil, nil", path, got, err)
		}
	}
	wt := filepath.Join(t.TempDir(), "worktree")
	git(t, dir, "worktree", "add", "-q", "-b", "changes", wt)
	write(t, wt, "tracked.txt", "changed\n")
	if got, err := GitChanges(wt); err != nil || len(got) != 1 || got[0].Code != " M" {
		t.Fatalf("worktree changes = %+v, %v", got, err)
	}
	git(t, dir, "checkout", "--", "tracked.txt")
	if err := os.Remove(filepath.Join(dir, "new.txt")); err != nil {
		t.Fatal(err)
	}
	if got, err := GitChanges(dir); err != nil || got != nil {
		t.Fatalf("clean changes = %+v, %v", got, err)
	}
	fresh := t.TempDir()
	git(t, fresh, "init", "-q")
	write(t, fresh, "new file", "new\n")
	git(t, fresh, "add", ".")
	if got, err := GitChanges(fresh); err != nil || len(got) != 1 || got[0].Code != "A " || got[0].Path != "new file" {
		t.Fatalf("unborn changes = %+v, %v", got, err)
	}
	t.Setenv("PATH", t.TempDir())
	if _, err := GitChanges(dir); err == nil {
		t.Fatal("missing Git should report an error")
	}
}

func TestGitChangesSubmoduleConfiguration(t *testing.T) {
	parent, source := gutterRepo(t), gutterRepo(t)
	git(t, parent, "checkout", "--", "tracked.txt")
	if err := os.Remove(filepath.Join(parent, "new.txt")); err != nil {
		t.Fatal(err)
	}
	git(t, parent, "-c", "protocol.file.allow=always", "submodule", "add", "-q", source, "module")
	git(t, parent, "commit", "-qm", "submodule")
	module := filepath.Join(parent, "module")
	write(t, module, "tracked.txt", "changed\n")
	changes, err := GitChanges(module)
	if err != nil || len(changes) != 1 || changes[0].Path != "tracked.txt" || changes[0].Code != " M" {
		t.Fatalf("submodule checkout changes = %+v, %v", changes, err)
	}
	changes, err = GitChanges(parent)
	if err != nil || len(changes) != 1 || changes[0].Path != "module" || changes[0].Code != " M" {
		t.Fatalf("parent changes = %+v, %v", changes, err)
	}
	git(t, parent, "config", "submodule.module.ignore", "all")
	if changes, err := GitChanges(parent); err != nil || len(changes) != 0 {
		t.Fatalf("ignored submodule changes = %+v, %v", changes, err)
	}
	snapshot, err := ReadWorktreeStatus(context.Background(), parent)
	if err != nil || snapshot.Files["module"].Worktree != 'M' {
		t.Fatalf("explicit submodule inspection = %+v, %v", snapshot, err)
	}
}

func TestGitChangesRenameAndOrder(t *testing.T) {
	dir := gutterRepo(t)
	origin, destination := "a old name", "b new name"
	if runtime.GOOS != "windows" {
		origin, destination = "a old\t雪\nname", "b new\t雪\nname "
	}
	write(t, dir, origin, strings.Repeat("retained\n", 20))
	write(t, dir, "z mixed", "before\n")
	git(t, dir, "add", ".")
	git(t, dir, "commit", "-qm", "baseline")
	git(t, dir, "mv", origin, destination)
	write(t, dir, destination, strings.Repeat("retained\n", 20)+"rename edit\n")
	write(t, dir, "z mixed", "staged\n")
	git(t, dir, "add", "z mixed")
	write(t, dir, "z mixed", "unstaged\n")
	write(t, dir, "0 untracked", "new\n")
	write(t, dir, "zz untracked", "new\n")
	want := []GitChange{
		{Code: "RM", Path: destination, OriginalPath: origin},
		{Code: "MM", Path: "z mixed"},
		{Code: "??", Path: "0 untracked"},
		{Code: "??", Path: "zz untracked"},
	}
	got, err := GitChanges(dir)
	if err != nil || !reflect.DeepEqual(got, want) {
		t.Fatalf("changes = %+v, %v; want %+v", got, err, want)
	}
	out, err := Diff(dir, got[0].Path, false)
	if err != nil || !strings.Contains(out, "+rename edit") {
		t.Fatalf("rename destination diff = %q, %v", out, err)
	}
	stats, err := DiffStats(dir)
	if err != nil || stats[destination] != (DiffStat{Added: 1}) {
		t.Fatalf("rename destination stats = %+v, %v", stats, err)
	}
}

func TestGitChangesConflict(t *testing.T) {
	dir := gutterRepo(t)
	git(t, dir, "add", ".")
	git(t, dir, "commit", "-qm", "baseline")
	git(t, dir, "checkout", "-qb", "other")
	write(t, dir, "tracked.txt", "other\n")
	git(t, dir, "commit", "-qam", "other edit")
	git(t, dir, "checkout", "-q", "main")
	write(t, dir, "tracked.txt", "main\n")
	git(t, dir, "commit", "-qam", "main edit")
	if err := exec.Command("git", "-C", dir, "merge", "--no-edit", "other").Run(); err == nil {
		t.Fatal("expected a merge conflict")
	}
	got, err := GitChanges(dir)
	want := []GitChange{{Code: "UU", Path: "tracked.txt"}}
	if err != nil || !reflect.DeepEqual(got, want) {
		t.Fatalf("conflict changes = %+v, %v; want %+v", got, err, want)
	}
	snapshot, err := ReadWorktreeStatus(context.Background(), dir)
	if err != nil || !snapshot.Files["tracked.txt"].Conflict {
		t.Fatalf("conflict snapshot = %+v, %v", snapshot, err)
	}
}
