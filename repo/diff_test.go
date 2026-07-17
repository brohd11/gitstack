package repo

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// diffRepo is a checkout with one committed file, edited in the working tree, plus a
// staged file and an untracked one — the four states the Diff view has to render.
func diffRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	git(t, dir, "init", "-q", "-b", "main")
	setIdentity(t, dir)

	if err := os.WriteFile(filepath.Join(dir, "tracked.txt"), []byte("one\ntwo\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	git(t, dir, "add", ".")
	git(t, dir, "commit", "-q", "-m", "init")

	// An unstaged edit.
	if err := os.WriteFile(filepath.Join(dir, "tracked.txt"), []byte("one\nTWO\nthree\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	// A staged addition: `diff HEAD` must include it, since `commit -a` would.
	if err := os.WriteFile(filepath.Join(dir, "staged.txt"), []byte("staged\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	git(t, dir, "add", "staged.txt")
	// An untracked file: not in HEAD, so it has no diff at all without --no-index.
	if err := os.WriteFile(filepath.Join(dir, "new.txt"), []byte("brand new\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	return dir
}

// TestDiffIncludesStaged pins the scope choice: HEAD, not a bare `git diff`. The view
// should show what a commit would contain, which includes what's already been staged.
func TestDiffIncludesStaged(t *testing.T) {
	dir := diffRepo(t)

	out, err := Diff(dir, "", false)
	if err != nil {
		t.Fatalf("Diff: %v", err)
	}
	if !strings.Contains(out, "+THREE") && !strings.Contains(out, "+three") {
		t.Errorf("the unstaged edit should be in the diff:\n%s", out)
	}
	if !strings.Contains(out, "staged.txt") {
		t.Errorf("a staged file should be in the diff — `commit -a` would include it:\n%s", out)
	}
}

// TestDiffOnePath checks the per-file rows: a path narrows the diff to that file.
func TestDiffOnePath(t *testing.T) {
	dir := diffRepo(t)

	out, err := Diff(dir, "tracked.txt", false)
	if err != nil {
		t.Fatalf("Diff: %v", err)
	}
	if !strings.Contains(out, "tracked.txt") {
		t.Errorf("the diff should cover the named file:\n%s", out)
	}
	if strings.Contains(out, "staged.txt") {
		t.Errorf("a path-scoped diff should not include other files:\n%s", out)
	}
}

// TestDiffUntracked is a regression test. `git diff --no-index` exits 1 *because* it found
// differences — it has already produced the whole diff. Treating that status as a failure
// (or discarding the stdout that came with it) renders every new file as empty, which is
// exactly the file a reader is most likely to want to look at.
func TestDiffUntracked(t *testing.T) {
	dir := diffRepo(t)

	out, err := Diff(dir, "new.txt", true)
	if err != nil {
		t.Fatalf("an untracked diff must not report the expected exit status 1 as an error: %v", err)
	}
	if out == "" {
		t.Fatal("an untracked file's diff came back empty — the exit-1 stdout was dropped")
	}
	if !strings.Contains(out, "+brand new") {
		t.Errorf("an untracked file's contents should render as additions:\n%s", out)
	}
}

// TestDiffUntrackedNeedsPath: "every untracked file" isn't a thing --no-index can express,
// so asking for it is a caller bug and should say so rather than shell out to nonsense.
func TestDiffUntrackedNeedsPath(t *testing.T) {
	if _, err := Diff(diffRepo(t), "", true); err == nil {
		t.Error("an untracked diff with no path should be an error")
	}
}

// TestDiffStats covers the picker's +n/-n rows, including the two cases that would
// otherwise show a misleading 0/0: a binary file, and an untracked file (absent entirely).
func TestDiffStats(t *testing.T) {
	dir := diffRepo(t)
	// A staged binary file: numstat reports "-\t-\tpath" for it.
	if err := os.WriteFile(filepath.Join(dir, "bin.dat"), []byte{0, 1, 2, 0, 3}, 0o644); err != nil {
		t.Fatal(err)
	}
	git(t, dir, "add", "bin.dat")

	stats, err := DiffStats(dir)
	if err != nil {
		t.Fatalf("DiffStats: %v", err)
	}

	// tracked.txt: "two" -> "TWO" plus an appended "three" = 2 added, 1 deleted.
	if got := stats["tracked.txt"]; got.Added != 2 || got.Deleted != 1 || got.Binary {
		t.Errorf("tracked.txt: got %+v, want {Added:2 Deleted:1 Binary:false}", got)
	}
	if got := stats["bin.dat"]; !got.Binary {
		t.Errorf("bin.dat should be reported as binary, got %+v", got)
	}
	if _, ok := stats["new.txt"]; ok {
		t.Error("an untracked file has no numstat entry — the row falls back to \"new file\"")
	}
}

// TestDiffNoCommits: `git diff HEAD` fails in a repo with no commits, and git's own
// message ("ambiguous argument 'HEAD'") explains nothing to someone who pressed Diff.
func TestDiffNoCommits(t *testing.T) {
	dir := t.TempDir()
	git(t, dir, "init", "-q", "-b", "main")
	if err := os.WriteFile(filepath.Join(dir, "f.txt"), []byte("x\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	_, err := Diff(dir, "", false)
	if err == nil {
		t.Fatal("a repo with no commits should report why it can't diff")
	}
	if !strings.Contains(err.Error(), "no commits") {
		t.Errorf("the error should explain the repo has no commits, got: %v", err)
	}

	// DiffStats takes the same path but is decorative, so it degrades to empty rather
	// than failing the picker.
	if _, err := DiffStats(dir); err != nil {
		t.Errorf("DiffStats should tolerate a repo with no commits, got: %v", err)
	}
}
