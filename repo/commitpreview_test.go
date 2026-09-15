package repo

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

func TestCommitPreview(t *testing.T) {
	dir := diffRepo(t)
	// Start from a clean baseline with files to delete and rename.
	write(t, dir, "deleted.txt", "gone\naway\n")
	write(t, dir, "old name.txt", "one\ntwo\nthree\nfour\nfive\n")
	git(t, dir, "add", ".")
	git(t, dir, "commit", "-qm", "baseline")
	write(t, dir, "tracked.txt", "one\nstaged\nthree\n")
	git(t, dir, "add", "tracked.txt")
	write(t, dir, "tracked.txt", "one\nfinal\nthree\nextra\n")
	if err := os.Remove(filepath.Join(dir, "deleted.txt")); err != nil {
		t.Fatal(err)
	}
	git(t, dir, "mv", "old name.txt", "new name.txt")
	write(t, dir, "new name.txt", "one\ntwo\nthree\nfour\nfive\nsix\n")
	write(t, dir, "staged addition.txt", "staged\n")
	git(t, dir, "add", "staged addition.txt")
	write(t, dir, "new text.txt", "first\nsecond")
	write(t, dir, "empty.txt", "")
	write(t, dir, "binary.dat", "\x00\x01\x02")
	write(t, dir, "staged binary.dat", "\x00\x01\x02")
	git(t, dir, "add", "staged binary.dat")
	before, err := os.ReadFile(filepath.Join(dir, ".git", "index"))
	if err != nil {
		t.Fatal(err)
	}
	files, err := CommitPreview(dir)
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]DiffStat{
		"tracked.txt":         {Added: 2, Deleted: 1},
		"deleted.txt":         {Deleted: 2},
		"new name.txt":        {Added: 1},
		"staged addition.txt": {Added: 1},
		"new text.txt":        {Added: 2},
		"empty.txt":           {},
		"binary.dat":          {Binary: true},
		"staged binary.dat":   {Binary: true},
	}
	if len(files) != len(want) {
		t.Fatalf("got %d files, want %d: %+v", len(files), len(want), files)
	}
	for _, file := range files {
		st, ok := want[file.Status.Path]
		if !ok || file.Stat == nil || *file.Stat != st {
			t.Errorf("%q: got %+v, want %+v", file.Status.Path, file.Stat, st)
		}
		if file.Status.Path == "new name.txt" && file.Status.OriginalPath != "old name.txt" {
			t.Errorf("lost rename origin: %+v", file.Status)
		}
		if file.Status.Path == "tracked.txt" && (file.Status.Index != 'M' || file.Status.Worktree != 'M') {
			t.Errorf("lost partial staging: %+v", file.Status)
		}
	}
	after, err := os.ReadFile(filepath.Join(dir, ".git", "index"))
	if err != nil || !bytes.Equal(before, after) {
		t.Fatalf("preview changed the index: %v", err)
	}
}

func TestCommitPreviewNoCommits(t *testing.T) {
	dir := t.TempDir()
	git(t, dir, "init", "-q")
	write(t, dir, "staged.txt", "old\n")
	git(t, dir, "add", "staged.txt")
	write(t, dir, "staged.txt", "first\nsecond")
	write(t, dir, "untracked.txt", "first\nsecond")
	files, err := CommitPreview(dir)
	if err != nil || len(files) != 2 {
		t.Fatalf("preview = %+v, %v", files, err)
	}
	for _, f := range files {
		if f.Stat == nil || *f.Stat != (DiffStat{Added: 2}) {
			t.Errorf("%s: %+v", f.Status.Path, f.Stat)
		}
	}
}

func TestCommitPreviewZeroAndClean(t *testing.T) {
	dir := diffRepo(t)
	git(t, dir, "add", ".")
	git(t, dir, "commit", "-qm", "clean")
	files, err := CommitPreview(dir)
	if err != nil || len(files) != 0 {
		t.Fatalf("clean preview = %+v, %v", files, err)
	}
	write(t, dir, "tracked.txt", "staged edit\n")
	git(t, dir, "add", "tracked.txt")
	write(t, dir, "tracked.txt", "one\nTWO\nthree\n")
	git(t, dir, "mv", "new.txt", "renamed.txt")
	files, err = CommitPreview(dir)
	if err != nil || len(files) != 2 {
		t.Fatalf("preview = %+v, %v", files, err)
	}
	for _, f := range files {
		if f.Stat == nil || *f.Stat != (DiffStat{}) {
			t.Errorf("%s: expected verified zero, got %+v", f.Status.Path, f.Stat)
		}
	}
}

func TestCommitStatsUnavailable(t *testing.T) {
	dir := diffRepo(t)
	if st := untrackedCommitStat(dir, "missing.txt"); st != nil {
		t.Fatalf("missing file got counts: %+v", st)
	}
	t.Setenv("PATH", t.TempDir())
	if _, err := CommitPreview(dir); err == nil {
		t.Fatal("missing git should fail the status read")
	}
}

func TestParseCommitStats(t *testing.T) {
	name := "dir/ spaced\t雪\nname -> x "
	stats, err := parseCommitStats("2\t1\t" + name + "\x00" +
		"0\t0\t\x00old/name\x00new/name\x00-\t-\tbinary\x00")
	if err != nil || len(stats) != 3 {
		t.Fatalf("parse = %+v, %v", stats, err)
	}
	if stats[name] != (DiffStat{Added: 2, Deleted: 1}) || stats["new/name"] != (DiffStat{}) || !stats["binary"].Binary {
		t.Fatal(stats)
	}
	for _, bad := range []string{"1\t2\tpath", "bad\x00", "a\tb\tpath\x00", "1\t2\t\x00old\x00", "-1\t0\tpath\x00"} {
		if _, err := parseCommitStats(bad); err == nil {
			t.Errorf("accepted %q", bad)
		}
	}
}
