package repocmd

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// nestedRepoTree builds base/<name>/.git for each name (real git init), so FindGitRepos
// sees them as depth-1 checkouts.
func nestedRepoTree(t *testing.T, names ...string) string {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	base := t.TempDir()
	for _, name := range names {
		dir := filepath.Join(base, name)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		if out, err := exec.Command("git", "-C", dir, "init", "-q", "-b", "main").CombinedOutput(); err != nil {
			t.Fatalf("git init %s: %v\n%s", name, err, out)
		}
	}
	return base
}

// runCmd drives the command the way cobra will in production -- through argument
// parsing -- rather than reaching past the flags. That is what makes the flag wiring
// (defaults, ResolveDepth, --include-root) part of what is under test.
func runCmd(t *testing.T, opts Options, args ...string) string {
	t.Helper()
	cmd := New(opts)
	var buf bytes.Buffer
	cmd.SetOut(&buf)
	cmd.SetErr(&buf)
	cmd.SetArgs(args)
	if err := cmd.Execute(); err != nil {
		t.Fatalf("execute %v: %v", args, err)
	}
	return buf.String()
}

// List mode: no command, so every matching repo path is printed.
func TestListMode(t *testing.T) {
	base := nestedRepoTree(t, "alpha", "beta")
	got := runCmd(t, Options{AppName: "test", DefaultDepth: 1}, "-C", base)
	for _, want := range []string{"alpha", "beta"} {
		if !strings.Contains(got, want) {
			t.Errorf("list output missing %q:\n%s", want, got)
		}
	}
}

// --include-root opts the scanned base itself into the listing as ".", but only when it
// is a git checkout, and never without the flag.
func TestIncludeRoot(t *testing.T) {
	base := nestedRepoTree(t, "alpha", "beta")
	if out, err := exec.Command("git", "-C", base, "init", "-q", "-b", "main").CombinedOutput(); err != nil {
		t.Fatalf("git init base: %v\n%s", err, out)
	}
	opts := Options{AppName: "test", DefaultDepth: 1}

	hasRootLine := func(s string) bool {
		for _, line := range strings.Split(s, "\n") {
			if strings.TrimSpace(line) == "." {
				return true
			}
		}
		return false
	}

	if got := runCmd(t, opts, "-C", base); hasRootLine(got) {
		t.Errorf("root should be excluded without --include-root:\n%s", got)
	}

	got := runCmd(t, opts, "-C", base, "--include-root")
	if !hasRootLine(got) {
		t.Errorf("--include-root should list the base as \".\":\n%s", got)
	}
	for _, want := range []string{"alpha", "beta"} {
		if !strings.Contains(got, want) {
			t.Errorf("--include-root output missing nested repo %q:\n%s", want, got)
		}
	}
}

// A command runs inside each repo and its output is captured under a header.
func TestRunsCommandInEachRepo(t *testing.T) {
	base := nestedRepoTree(t, "alpha", "beta")
	got := runCmd(t, Options{AppName: "test", DefaultDepth: 1}, "-C", base, "--", "pwd")
	for _, want := range []string{"alpha", "beta"} {
		if !strings.Contains(got, want) {
			t.Errorf("output missing %q:\n%s", want, got)
		}
	}
}

// The per-app knobs. gdaddon scans 5 levels and repoview 1, and repoview layers an
// environment variable underneath its flag -- the two differences that were the reason
// this command existed twice.
func TestPerAppOptions(t *testing.T) {
	t.Run("DefaultDepth reaches the flag", func(t *testing.T) {
		for _, depth := range []int{1, 5} {
			cmd := New(Options{AppName: "test", DefaultDepth: depth})
			if got := cmd.Flags().Lookup("depth").DefValue; got != fmt.Sprint(depth) {
				t.Errorf("DefaultDepth %d showed as %q", depth, got)
			}
		}
	})

	t.Run("DepthDefText overrides what help prints", func(t *testing.T) {
		cmd := New(Options{AppName: "test", DefaultDepth: 1, DepthDefText: "$TEST_DEPTH, else 1"})
		if got := cmd.Flags().Lookup("depth").DefValue; got != "$TEST_DEPTH, else 1" {
			t.Errorf("DepthDefText not applied, got %q", got)
		}
	})

	t.Run("ResolveDepth is consulted and can fail the run", func(t *testing.T) {
		var gotFlag int
		var gotChanged bool
		opts := Options{
			AppName:      "test",
			DefaultDepth: 1,
			ResolveDepth: func(flagDepth int, flagChanged bool) (int, error) {
				gotFlag, gotChanged = flagDepth, flagChanged
				return flagDepth, nil
			},
		}
		base := nestedRepoTree(t, "alpha")
		runCmd(t, opts, "-C", base, "--depth", "3")
		if gotFlag != 3 || !gotChanged {
			t.Errorf("ResolveDepth got (%d, %v), want (3, true)", gotFlag, gotChanged)
		}

		// An error from the resolver must stop the run rather than be swallowed.
		failing := Options{
			AppName:      "test",
			DefaultDepth: 1,
			ResolveDepth: func(int, bool) (int, error) { return 0, fmt.Errorf("bad depth") },
		}
		cmd := New(failing)
		cmd.SetOut(&bytes.Buffer{})
		cmd.SetErr(&bytes.Buffer{})
		cmd.SetArgs([]string{"-C", base})
		if err := cmd.Execute(); err == nil {
			t.Error("a ResolveDepth error should fail the command")
		}
	})

	t.Run("AppName appears in the help examples", func(t *testing.T) {
		cmd := New(Options{AppName: "repoview", DefaultDepth: 1})
		if !strings.Contains(cmd.Long, "repoview repos -- git status -s") {
			t.Errorf("AppName missing from Long:\n%s", cmd.Long)
		}
	})
}
