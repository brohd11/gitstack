package repo

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
)

// This file captures diffs for the UI to render. It sits apart from the other two
// git primitives because neither can carry a diff intact:
//
//   - GitStream (ops.go) relays through a Reporter, and lineWriter drops empty lines and
//     trims trailing whitespace. In a diff both are content: a blank context line is a
//     line, and the leading marker column is the whole format.
//   - gitOutput (repo.go) swallows every error to "", which would render a diff that
//     failed as a diff that was empty — a clean tree. The difference matters here.
//
// So these capture whole stdout, byte for byte, and keep the error.
//
// Nothing here passes `-c color.ui=always`. The renderer parses the plain unified format
// and applies its own theme-aware color; git's ANSI would only have to be stripped back
// out before it could be parsed.

// diffAgainst is the revision working-tree diffs are taken against. HEAD (rather than a
// bare `git diff`) includes staged changes as well as unstaged ones, so the view answers
// the same question the commit flow asks — what would `commit -a` contain — rather than
// hiding anything already added to the index.
const diffAgainst = "HEAD"

// Diff returns the unified diff of dir's working tree for one path, or for every changed
// file when path is "". Untracked files are not in HEAD, so git has nothing to diff them
// against and reports nothing at all; untracked=true switches to --no-index against
// /dev/null, which renders the new file as one all-additions hunk.
//
// The returned string is git's raw output, uninterpreted.
func Diff(dir, path string, untracked bool) (string, error) {
	if dir == "" {
		return "", errors.New("no repo directory")
	}
	if untracked {
		if path == "" {
			return "", errors.New("an untracked diff needs a file path")
		}
		return diffNoIndex(dir, path)
	}

	args := []string{"-c", "core.quotepath=false", "diff", diffAgainst}
	if path != "" {
		args = append(args, "--", path)
	}
	out, err := gitCapture(dir, args...)
	if err != nil {
		// A repo with no commits has no HEAD to diff against, and git's own message
		// ("fatal: ambiguous argument 'HEAD'") explains nothing to someone who just
		// pressed Diff. Every file is new in that repo, so say that instead.
		if !hasHEAD(dir) {
			return "", errors.New("this repo has no commits yet — every file is new")
		}
		return "", err
	}
	return out, nil
}

// diffNoIndex renders an untracked file as a diff against the null device (os.DevNull, so
// this holds on Windows too — see terminal.go for the other place the OS shows through).
// --no-index exits 1 when the two sides differ, which for a file with any content at all
// is always — so exit 1 is the success path here and only a worse status is an error.
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

// DiffStats maps each changed file's repo-relative path to its counts, via `diff
// --numstat HEAD`. Untracked files are absent — they aren't in the diff at all — so a
// caller reading a missing entry gets the zero value and should say "new file" instead.
func DiffStats(dir string) (map[string]DiffStat, error) {
	if dir == "" {
		return nil, nil
	}
	out, err := gitCapture(dir, "-c", "core.quotepath=false", "diff", "--numstat", diffAgainst)
	if err != nil {
		if !hasHEAD(dir) {
			return nil, nil // no commits: every file is new, and new files have no numstat
		}
		return nil, err
	}

	stats := make(map[string]DiffStat)
	for _, line := range strings.Split(out, "\n") {
		// "added\tdeleted\tpath", with "-\t-\tpath" for a binary file. A rename arrives
		// as "a\td\told => new" (or a brace form); the path is kept verbatim, matching
		// GitChanges' reading of a rename.
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

// gitCapture runs a read-only `git -C dir <args...>` and returns its stdout whole — no
// trimming, since a diff's leading spaces and blank lines are content. On failure it folds
// git's stderr into the error, which is the part worth reading.
//
// stdout is returned even when err is non-nil, and that is not incidental: a non-zero exit
// does not always mean there is no output. `diff --no-index` exits 1 precisely when it
// found differences — it has produced the whole diff and is reporting what it found — so a
// caller that can read the status decides whether the output counts. Discarding it here
// would silently render every untracked file as empty.
func gitCapture(dir string, args ...string) (string, error) {
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	cmd.Env = gitEnv()
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
	return exec.Command("git", "-C", dir, "rev-parse", "--verify", "HEAD").Run() == nil
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
