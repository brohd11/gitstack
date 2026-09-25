package repo

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
)

// Diff capture for the UI. It keeps whole stdout, byte for byte, with the error: GitStream
// drops empty lines and trailing whitespace (content in a diff), and GitOutput turns a
// failure into "", which would read as a clean tree. No color is requested; the renderer
// colors the plain unified format itself.

// diffAgainst is the revision working-tree diffs compare to. HEAD includes staged changes,
// matching what `commit -a` would contain. Always follow it with `--`: git refuses to guess
// when a file or directory named HEAD (or `head` on case-insensitive macOS) exists.
const diffAgainst = "HEAD"

// Diff returns the unified diff of one path in dir's working tree, or of every changed
// file for "". untracked says whether untracked content is in scope (`diff HEAD` reports
// nothing for it): a path diffs against /dev/null, and "" covers the whole tree (see
// diffAll). The output is git's, raw.
func Diff(dir, path string, untracked bool) (string, error) {
	if dir == "" {
		return "", errors.New("no repo directory")
	}
	if untracked {
		if path == "" {
			return diffAll(dir)
		}
		return diffNoIndex(dir, path)
	}

	args := []string{"-c", "core.quotepath=false", "diff", diffAgainst, "--"}
	if path != "" {
		args = append(args, path)
	}
	out, err := gitCapture(dir, args...)
	if err != nil {
		// With no commits there is no HEAD; say every file is new instead of git's error.
		if !hasHEAD(dir) {
			return "", errors.New("this repo has no commits yet — every file is new")
		}
		return "", err
	}
	return out, nil
}

// diffAll is the whole working tree: `diff HEAD` for tracked files plus a --no-index diff
// per untracked file. Two invocations, since `add -N` would write to the index.
func diffAll(dir string) (string, error) {
	var parts []string

	// With no commits every file is untracked, so the loop below still covers the tree.
	if hasHEAD(dir) {
		out, err := gitCapture(dir, "-c", "core.quotepath=false", "diff", diffAgainst, "--")
		if err != nil {
			return "", err
		}
		if strings.TrimSpace(out) != "" {
			parts = append(parts, out)
		}
	}

	changes, err := GitChanges(dir)
	if err != nil {
		return "", err
	}
	for _, c := range changes {
		if !c.Untracked() {
			continue
		}
		out, err := diffNoIndex(dir, c.Path)
		if err != nil {
			return "", err
		}
		if strings.TrimSpace(out) != "" {
			parts = append(parts, out)
		}
	}

	// Each diff ends in its own newline, so the parts abut cleanly: every one begins at a
	// "diff --git" header, which is where the parser starts a new file either way.
	return strings.Join(parts, ""), nil
}

// diffNoIndex diffs an untracked file against os.DevNull. --no-index exits 1 when the
// sides differ, so 1 is success here.
func diffNoIndex(dir, path string) (string, error) {
	out, err := gitCapture(dir, "-c", "core.quotepath=false", "diff", "--no-index", "--", os.DevNull, path)
	if err != nil && exitCode(err) != 1 {
		return "", err
	}
	return out, nil
}

// DiffStat is one file's line counts, for the picker rows. A binary file has no line
// counts (git reports "-"), which Binary records rather than reporting a misleading 0/0.
type DiffStat struct {
	Added   int
	Deleted int
	Binary  bool
}

// DiffStats maps each changed path to its counts (`diff --numstat HEAD`). Untracked files
// are absent; the zero value should read as "new file".
func DiffStats(dir string) (map[string]DiffStat, error) {
	if dir == "" {
		return nil, nil
	}
	out, err := gitCapture(dir, "-c", "core.quotepath=false", "diff", "--numstat", diffAgainst, "--")
	if err != nil {
		if !hasHEAD(dir) {
			return nil, nil // no commits: every file is new, and new files have no numstat
		}
		return nil, err
	}

	stats := make(map[string]DiffStat)
	for _, line := range strings.Split(out, "\n") {
		// "added\tdeleted\tpath" ("-\t-\t" for binary); a rename's path is kept verbatim, as
		// GitChanges does.
		parts := strings.SplitN(line, "\t", 3)
		if len(parts) != 3 {
			continue
		}
		if parts[0] == "-" {
			stats[parts[2]] = DiffStat{Binary: true}
			continue
		}
		added, err1 := strconv.Atoi(parts[0])
		deleted, err2 := strconv.Atoi(parts[1])
		if err1 != nil || err2 != nil {
			continue
		}
		stats[parts[2]] = DiffStat{Added: added, Deleted: deleted}
	}
	return stats, nil
}

// gitCapture runs a read-only git command and returns stdout untrimmed (whitespace is
// content), folding stderr into any error. stdout is returned even on error: `diff
// --no-index` exits 1 when it found differences, and the caller decides.
func gitCapture(dir string, args ...string) (string, error) {
	cmd := gitCmd(context.Background(), dir, args...)
	var stderr strings.Builder
	cmd.Stderr = &stderr

	out, err := cmd.Output()
	if err != nil {
		if msg := strings.TrimSpace(stderr.String()); msg != "" {
			return string(out), fmt.Errorf("%w: %s", err, firstLine(msg))
		}
		return string(out), err
	}
	return string(out), nil
}

// hasHEAD reports whether dir has any commits — the thing `git diff HEAD` needs and a
// freshly-initialized repo lacks.
func hasHEAD(dir string) bool {
	return gitCmd(context.Background(), dir, "rev-parse", "--verify", "HEAD").Run() == nil
}

// exitCode returns the process's exit status, or -1 when err isn't an exit failure.
func exitCode(err error) int {
	var ee *exec.ExitError
	if errors.As(err, &ee) {
		return ee.ExitCode()
	}
	return -1
}

// firstLine is git's opening complaint, which is the useful one; the rest is usually
// hints that read poorly on a status line.
func firstLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return s[:i]
	}
	return s
}
