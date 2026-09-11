package repo

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"testing"
)

func TestParseWorktreeStatus(t *testing.T) {
	unusual := "dir/ space\t雪\nname -> x "
	s, err := parseWorktreeStatus("# future header\x00" +
		"1 MM N... 100644 100644 100644 abc def mixed.go\x00" +
		"2 R. N... 100644 100644 100644 abc def R100 " + unusual + "\x00old\nname\x00" +
		"u UU N... 100644 100644 100644 100644 a b c conflict.go\x00" +
		"? new/file.go\x00! ignored/\x00")
	if err != nil {
		t.Fatal(err)
	}
	if f := s.Files["mixed.go"]; f.Index != 'M' || f.Worktree != 'M' {
		t.Fatalf("mixed = %+v", f)
	}
	if f := s.Files[unusual]; f.Path != unusual || f.OriginalPath != "old\nname" || f.Index != 'R' {
		t.Fatalf("rename = %+v", f)
	}
	if !s.Files["conflict.go"].Conflict || !s.Files["new/file.go"].Untracked {
		t.Fatal("lost status kind")
	}
	if !reflect.DeepEqual(s.Ignored, []string{"ignored/"}) {
		t.Fatal(s.Ignored)
	}
	for _, bad := range []string{"1 short\x00", "2 R. N... 1 1 1 a b R100 new\x00", "?\x00"} {
		if _, err := parseWorktreeStatus(bad); err == nil {
			t.Fatalf("accepted malformed %q", bad)
		}
	}
}

func TestReadWorktreeStatus(t *testing.T) {
	dir := gutterRepo(t)
	git(t, dir, "add", "tracked.txt")
	write(t, dir, "tracked.txt", "mixed changes\n")
	name := "spaced 雪\nfile.txt"
	if runtime.GOOS == "windows" {
		// Windows rejects newlines in filenames; the parser covers them in
		// TestParseWorktreeStatus, so keep just the spaces and non-ASCII here.
		name = "spaced 雪 file.txt"
	}
	write(t, dir, name, "new\n")
	git(t, dir, "mv", ".gitignore", "renamed ignore")
	// Restore ignore rules so ignored status remains independently testable.
	write(t, dir, ".gitignore", "ignored.txt\ncache/\n")
	if err := os.Mkdir(filepath.Join(dir, "cache"), 0755); err != nil {
		t.Fatal(err)
	}
	write(t, filepath.Join(dir, "cache"), "noise.txt", "noise")
	s, err := ReadWorktreeStatus(context.Background(), dir)
	if err != nil {
		t.Fatal(err)
	}
	if f := s.Files["tracked.txt"]; f.Index != 'M' || f.Worktree != 'M' {
		t.Fatalf("mixed = %+v", f)
	}
	if !s.Files[name].Untracked {
		t.Fatalf("unusual filename missing: %+v", s.Files)
	}
	if f := s.Files["renamed ignore"]; f.OriginalPath != ".gitignore" || f.Index != 'R' {
		t.Fatalf("rename = %+v", f)
	}
	if len(s.Ignored) != 2 {
		t.Fatalf("ignored = %v", s.Ignored)
	}
	sub := filepath.Join(dir, "sub")
	if err := os.Mkdir(sub, 0755); err != nil {
		t.Fatal(err)
	}
	nested, err := ReadWorktreeStatus(context.Background(), sub)
	if err != nil || nested.Root != s.Root {
		t.Fatalf("subdir root = %q, %v", nested.Root, err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := ReadWorktreeStatus(ctx, dir); err == nil {
		t.Fatal("cancelled read succeeded")
	}
}

func TestWorktreeStatusDiscovery(t *testing.T) {
	dir := gutterRepo(t)
	wt := filepath.Join(t.TempDir(), "worktree")
	git(t, dir, "worktree", "add", "-q", "-b", "other", wt)
	write(t, wt, "tracked.txt", "changed\n")
	s, err := ReadWorktreeStatus(context.Background(), wt)
	if err != nil || s.Files["tracked.txt"].Worktree != 'M' {
		t.Fatalf("worktree = %+v %v", s, err)
	}
	plain, err := ReadWorktreeStatus(context.Background(), t.TempDir())
	if err != nil || plain.Root != "" {
		t.Fatalf("plain = %+v %v", plain, err)
	}
	fresh := t.TempDir()
	git(t, fresh, "init", "-q")
	write(t, fresh, "first.txt", "new")
	git(t, fresh, "add", "first.txt")
	s, err = ReadWorktreeStatus(context.Background(), fresh)
	if err != nil || s.Files["first.txt"].Index != 'A' {
		t.Fatalf("unborn = %+v %v", s, err)
	}
}

func TestWorktreeStatusSubmoduleAndMissingGit(t *testing.T) {
	parent := gutterRepo(t)
	source := gutterRepo(t)
	git(t, parent, "-c", "protocol.file.allow=always", "submodule", "add", "-q", source, "module")
	module := filepath.Join(parent, "module")
	write(t, module, "tracked.txt", "submodule change\n")
	status, err := ReadWorktreeStatus(context.Background(), module)
	if err != nil || status.Files["tracked.txt"].Worktree != 'M' {
		t.Fatalf("submodule = %+v, %v", status, err)
	}
	t.Setenv("PATH", t.TempDir())
	if _, err := ReadWorktreeStatus(context.Background(), parent); err == nil {
		t.Fatal("missing Git should report an error")
	}
}
