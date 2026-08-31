package repo

import (
	"os"
	"path/filepath"
	"testing"
)

// gutterRepo is a checkout covering the baseline cases: a committed file (edited in the
// working tree, so its baseline must differ from disk), an untracked one, and an ignored
// one.
func gutterRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	git(t, dir, "init", "-q", "-b", "main")
	setIdentity(t, dir)

	write(t, dir, ".gitignore", "ignored.txt\n")
	write(t, dir, "tracked.txt", "one\ntwo\n")
	git(t, dir, "add", ".")
	git(t, dir, "commit", "-q", "-m", "init")

	// The working tree moves on; HEAD must not follow it.
	write(t, dir, "tracked.txt", "one\nTWO\nthree\n")
	write(t, dir, "new.txt", "brand new\n")
	write(t, dir, "ignored.txt", "noise\n")
	return dir
}

func write(t *testing.T, dir, name, body string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

// TestHeadBlobIsHEADNotDisk is the point of the whole file: the baseline is what was
// committed, not what the working tree now holds. A gutter drawn from the disk copy
// would mark nothing, ever.
func TestHeadBlobIsHEADNotDisk(t *testing.T) {
	dir := gutterRepo(t)

	text, state, err := HeadBlob(dir, filepath.Join(dir, "tracked.txt"))
	if err != nil {
		t.Fatalf("HeadBlob: %v", err)
	}
	if state != BaselineOK {
		t.Fatalf("a committed file should have a baseline, got %s", state)
	}
	if text != "one\ntwo\n" {
		t.Errorf("the baseline should be HEAD's copy, got %q", text)
	}
}

// TestHeadBlobStates pins the four cases apart. They are all "show failed" to git, and
// all four want a different gutter.
func TestHeadBlobStates(t *testing.T) {
	dir := gutterRepo(t)
	outside := t.TempDir()

	cases := []struct {
		name string
		path string
		want Baseline
	}{
		{"tracked", filepath.Join(dir, "tracked.txt"), BaselineOK},
		{"untracked", filepath.Join(dir, "new.txt"), BaselineAbsent},
		{"ignored", filepath.Join(dir, "ignored.txt"), BaselineIgnored},
		{"outside the repo", filepath.Join(outside, "elsewhere.txt"), BaselineNone},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, state, _ := HeadBlob(dir, c.path)
			if state != c.want {
				t.Errorf("want %s, got %s", c.want, state)
			}
		})
	}
}

// TestHeadBlobThroughASymlinkedRoot is the case that breaks silently on macOS, where
// t.TempDir() sits under /var — itself a symlink to /private/var. RepoRoot answers with
// git's own `rev-parse --show-toplevel`, which resolves symlinks; the document path the
// editor holds does not. Related literally, every such file reads as outside its own
// repository and loses its baseline.
func TestHeadBlobThroughASymlinkedRoot(t *testing.T) {
	dir := gutterRepo(t)
	file := filepath.Join(dir, "tracked.txt")

	root, ok := RepoRoot(file)
	if !ok {
		t.Fatal("the file should find its checkout")
	}
	// The point of the test: root came back resolved, file did not.
	text, state, err := HeadBlob(root, file)
	if err != nil {
		t.Fatalf("HeadBlob through the resolved root: %v", err)
	}
	if state != BaselineOK || text != "one\ntwo\n" {
		t.Errorf("want the committed text, got %s %q", state, text)
	}
}

// TestHeadBlobNoCommits covers a fresh `git init`, where there is no HEAD to show from.
// Every file in such a repo is new, so Absent is the answer — not the error git gives
// ("fatal: ambiguous argument 'HEAD'"), which would blank the gutter on a real repo.
func TestHeadBlobNoCommits(t *testing.T) {
	dir := t.TempDir()
	git(t, dir, "init", "-q", "-b", "main")
	setIdentity(t, dir)
	write(t, dir, "fresh.txt", "hello\n")

	_, state, err := HeadBlob(dir, filepath.Join(dir, "fresh.txt"))
	if err != nil {
		t.Fatalf("a repo with no commits is not an error: %v", err)
	}
	if state != BaselineAbsent {
		t.Errorf("every file in a commitless repo is new, got %s", state)
	}
}

// TestRepoRoot checks the upward search — a file deep in the tree still names the
// checkout — and that a directory outside any repo says so.
func TestRepoRoot(t *testing.T) {
	dir := gutterRepo(t)
	sub := filepath.Join(dir, "a", "b")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	write(t, sub, "deep.txt", "x\n")

	root, ok := RepoRoot(filepath.Join(sub, "deep.txt"))
	if !ok {
		t.Fatal("a file inside a checkout should find its root")
	}
	// macOS hands back /private/var for /var, so compare resolved paths.
	if got, want := resolve(t, root), resolve(t, dir); got != want {
		t.Errorf("root: want %s, got %s", want, got)
	}

	if _, ok := RepoRoot(t.TempDir()); ok {
		t.Error("a directory outside any checkout should have no root")
	}
}

// TestHeadOIDMovesWithHEAD pins the invalidation key: a new commit must change it, or a
// consumer caching baselines against it would keep diffing against the wrong commit.
func TestHeadOIDMovesWithHEAD(t *testing.T) {
	dir := gutterRepo(t)
	before := HeadOID(dir)
	if before == "" {
		t.Fatal("a repo with a commit should have a HEAD oid")
	}
	git(t, dir, "add", ".")
	git(t, dir, "commit", "-q", "-m", "second")
	if HeadOID(dir) == before {
		t.Error("a commit should move the oid")
	}
}

func resolve(t *testing.T, p string) string {
	t.Helper()
	r, err := filepath.EvalSymlinks(p)
	if err != nil {
		return p
	}
	return r
}
