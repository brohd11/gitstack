// Package repo is a domain-neutral git engine over plain directories: read-only probes
// and mutating operations, reporting progress through a Reporter. The screens live in
// repoui. This file is the read-only half (discovery, status, divergence, fetch); ops.go
// has pull, push and commit.
package repo

import (
	"context"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"

	"github.com/brohd11/goutil/stream"
	"github.com/brohd11/goutil/strutil"
)

// Reporter receives progress lines (goutil/stream's type, so reporters work with both).
type Reporter = stream.Reporter

// Repo is a checkout a caller carries around: display name, directory, and optional cached
// state (branch, divergence, dirty) to render without re-reading git.
type Repo struct {
	Name   string
	Dir    string
	Branch string  // checked-out branch, for display ("" when unknown/detached)
	Sync   GitSync // cached divergence from upstream, as of the caller's last read
	Dirty  bool    // cached: working tree has uncommitted changes
	Root   bool    // true when this is the scanned base directory itself, not a nested checkout
}

// maxConcurrentFetch caps how many fetches FetchAll runs at once, so a large tree can't
// fire hundreds of parallel git processes (and trip host rate limits).
const maxConcurrentFetch = 8

// CurrentBranch returns dir's checked-out branch, or "" for a non-checkout, detached HEAD
// or unreadable repo.
func CurrentBranch(dir string) string {
	if _, err := os.Stat(filepath.Join(dir, ".git")); err != nil {
		return ""
	}
	if b := GitOutput(dir, "rev-parse", "--abbrev-ref", "HEAD"); b != "" && b != "HEAD" {
		return b
	}
	return ""
}

// Describe reads dir's branch, divergence and dirty state (all local, no network).
func Describe(name, dir string) Repo {
	return Repo{
		Name:   name,
		Dir:    dir,
		Branch: CurrentBranch(dir),
		Sync:   GitSyncStatus(dir),
		Dirty:  HasUncommittedChanges(dir),
	}
}

// Scan finds every checkout under base (to maxDepth, base excluded) and describes each.
// Name is the base-relative path, Dir absolute; results are in walk order.
func Scan(base string, maxDepth int) ([]Repo, error) {
	rels, err := FindGitRepos(base, maxDepth)
	if err != nil {
		return nil, err
	}
	out := make([]Repo, 0, len(rels))
	for _, rel := range rels {
		out = append(out, Describe(rel, filepath.Join(base, rel)))
	}
	return out, nil
}

// DescribeRoot describes base itself, marked Root, for viewers that show the scanned
// directory (which Scan omits). ok is false when base is not a checkout.
func DescribeRoot(base string) (Repo, bool) {
	if _, err := os.Stat(filepath.Join(base, ".git")); err != nil {
		return Repo{}, false
	}
	r := Describe(filepath.Base(base), base)
	r.Root = true
	return r, true
}

// StatusMarker renders the behind/ahead/dirty suffix from cached state, e.g.
// "  ⚠ [behind origin 2 / ahead 1 / uncommitted changes]"; empty when clean and in sync.
func StatusMarker(r Repo) string {
	var parts []string
	if r.Sync.Behind > 0 {
		parts = append(parts, fmt.Sprintf("behind origin %d", r.Sync.Behind))
	}
	if r.Sync.Ahead > 0 {
		parts = append(parts, fmt.Sprintf("ahead %d", r.Sync.Ahead))
	}
	if r.Dirty {
		parts = append(parts, "uncommitted changes")
	}
	if len(parts) == 0 {
		return ""
	}
	return "  ⚠ [" + strings.Join(parts, " / ") + "]"
}

// HasUncommittedChanges reports whether dir is a checkout with modified or untracked
// files.
func HasUncommittedChanges(dir string) bool {
	if _, err := os.Stat(filepath.Join(dir, ".git")); err != nil {
		return false
	}
	return GitOutput(dir, "status", "--porcelain") != ""
}

// GitSync is a checkout's divergence from its upstream as of the last fetch: a local
// comparison, stale until GitFetch runs (as `git status` is).
type GitSync struct {
	Ahead    int  // local commits not on the upstream (unpushed)
	Behind   int  // upstream commits not local (unpulled)
	Tracking bool // false when dir isn't a checkout, HEAD is detached, or the branch has no upstream
}

// GitSyncStatus reports dir's divergence; the zero value covers non-checkouts, detached
// HEAD and untracked branches.
func GitSyncStatus(dir string) GitSync {
	// An empty dir would resolve ".git" against the process's cwd — which may well be a
	// repo — and report a wholly unrelated checkout's divergence.
	if dir == "" {
		return GitSync{}
	}
	if _, err := os.Stat(filepath.Join(dir, ".git")); err != nil {
		return GitSync{}
	}
	// Prints "<behind>\t<ahead>" over the symmetric difference.
	out := GitOutput(dir, "rev-list", "--left-right", "--count", "@{upstream}...HEAD")
	fields := strings.Fields(out)
	if len(fields) != 2 {
		return GitSync{}
	}
	behind, err1 := strconv.Atoi(fields[0])
	ahead, err2 := strconv.Atoi(fields[1])
	if err1 != nil || err2 != nil {
		return GitSync{}
	}
	return GitSync{Ahead: ahead, Behind: behind, Tracking: true}
}

// GitFetch updates dir's remote-tracking refs so GitSyncStatus is current. It is the
// network call, so it takes ctx and returns errors; credential prompts fail fast
// (GitEnv).
func GitFetch(ctx context.Context, dir string) error {
	_, err := runGitCtx(ctx, dir, "fetch")
	return err
}

// FetchResult is one repo's FetchAll outcome: the error and the divergence read afterwards.
type FetchResult struct {
	Name string
	Err  error
	Sync GitSync
}

// FetchAll fetches every repo concurrently (capped at maxConcurrentFetch) and reads each
// one's divergence. ctx bounds the batch. Results are sorted by name.
func FetchAll(ctx context.Context, repos []Repo) []FetchResult {
	var mu sync.Mutex
	var wg sync.WaitGroup
	sem := make(chan struct{}, maxConcurrentFetch)
	var out []FetchResult
	for _, r := range repos {
		wg.Add(1)
		go func(name, dir string) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			res := FetchResult{Name: name, Err: GitFetch(ctx, dir)}
			res.Sync = GitSyncStatus(dir)
			mu.Lock()
			out = append(out, res)
			mu.Unlock()
		}(r.Name, r.Dir)
	}
	wg.Wait()
	slices.SortFunc(out, func(a, b FetchResult) int { return strings.Compare(a.Name, b.Name) })
	return out
}

// FindGitRepos returns the base-relative paths of checkouts under base (base excluded) to
// maxDepth levels, identified by a `.git` entry (directory or submodule file). It descends
// into repos for nested submodules, never into `.git`, and skips unreadable entries.
func FindGitRepos(base string, maxDepth int) ([]string, error) {
	var repos []string
	err := filepath.WalkDir(base, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil // skip unreadable entries
		}
		if !d.IsDir() {
			return nil
		}
		if d.Name() == ".git" {
			return filepath.SkipDir
		}
		if strutil.Depth(base, path) > maxDepth {
			return filepath.SkipDir
		}
		if path == base {
			return nil
		}
		if _, err := os.Stat(filepath.Join(path, ".git")); err == nil {
			if rel, err := filepath.Rel(base, path); err == nil {
				repos = append(repos, rel)
			}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return repos, nil
}

// GitChange is one `git status --porcelain` entry: the two-letter code and repo-relative
// path. "??" is untracked, which `commit -a` does not include.
type GitChange struct {
	Code string
	Path string
}

// Untracked reports whether the change is a file git isn't tracking yet.
func (c GitChange) Untracked() bool { return c.Code == "??" }

// GitChanges lists everything `git status --porcelain` reports (empty for a clean tree or
// non-checkout). -uall names every untracked file rather than collapsing a new directory,
// since diffs and commit confirms act per file; quotepath=false keeps non-ASCII readable.
func GitChanges(dir string) ([]GitChange, error) {
	if dir == "" {
		return nil, nil
	}
	if _, err := os.Stat(filepath.Join(dir, ".git")); err != nil {
		return nil, nil
	}
	out, err := gitCmd(context.Background(), dir, "-c", "core.quotepath=false", "status", "--porcelain", "-uall").Output()
	if err != nil {
		return nil, fmt.Errorf("could not read git status in %s: %w", dir, err)
	}

	var changes []GitChange
	for _, line := range strings.Split(string(out), "\n") {
		// "XY path": the path starts at column 3; a rename's "old -> new" is kept whole.
		if len(line) < 4 {
			continue
		}
		changes = append(changes, GitChange{
			Code: line[:2],
			Path: strings.TrimSpace(line[3:]),
		})
	}
	return changes, nil
}

// GitOutput runs a read-only git command and returns its trimmed stdout, or "" on any
// error (a repo with no origin, say).
func GitOutput(dir string, args ...string) string {
	out, err := gitCmd(context.Background(), dir, args...).Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

// GitEnv is the environment for every git subprocess: git must never wait on input a TUI
// cannot supply, so credential prompts fail and any editor exits empty. Exported for
// callers that spawn their own git (e.g. gdaddon).
func GitEnv() []string {
	return append(os.Environ(), "GIT_TERMINAL_PROMPT=0", "GIT_EDITOR=true")
}

// gitCmd builds `git -C dir <args...>` under GitEnv. Every git subprocess in this package
// starts here.
func gitCmd(ctx context.Context, dir string, args ...string) *exec.Cmd {
	cmd := exec.CommandContext(ctx, "git", append([]string{"-C", dir}, args...)...)
	cmd.Env = GitEnv()
	return cmd
}

// runGitCtx runs a cancellable git command and returns its combined output. On error,
// git's own message is folded into the error. Used by the network calls.
func runGitCtx(ctx context.Context, dir string, args ...string) (string, error) {
	out, err := gitCmd(ctx, dir, args...).CombinedOutput()
	if err != nil {
		msg := strings.TrimSpace(string(out))
		if msg == "" {
			return "", err
		}
		return "", fmt.Errorf("%w: %s", err, msg)
	}
	return string(out), nil
}
