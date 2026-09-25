package repo

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
)

// Reading a file's HEAD copy for diff gutters. The consumer diffs it against its live
// buffer (so markers track typing without a git process per keystroke); fetching the blob
// belongs here, diffing belongs to whoever owns the buffer.

// Baseline is what HEAD had for a file: OK diffs, Absent is wholly new, Ignored and None
// get no gutter.
type Baseline int

const (
	BaselineOK      Baseline = iota // the blob is in HEAD, and the returned text is it
	BaselineAbsent                  // inside a repo but not in HEAD: a new (or newly-added) file
	BaselineIgnored                 // inside a repo and matched by .gitignore
	BaselineNone                    // not inside a repo at all
)

// String names the case for error messages and tests.
func (b Baseline) String() string {
	switch b {
	case BaselineOK:
		return "ok"
	case BaselineAbsent:
		return "absent"
	case BaselineIgnored:
		return "ignored"
	default:
		return "none"
	}
}

// RepoRoot is the checkout enclosing path (a file or a directory), asked of git so
// worktrees, submodules and $GIT_DIR resolve correctly.
func RepoRoot(path string) (string, bool) {
	dir := path
	if fi, err := os.Stat(path); err == nil && !fi.IsDir() {
		dir = filepath.Dir(path)
	}
	root, err := RepoRootContext(context.Background(), dir)
	return root, err == nil && root != ""
}

// HeadBlob returns HEAD's copy of path (inside the checkout at dir). The Baseline is always
// meaningful; the text only with BaselineOK. A failed `git show` is narrowed: no HEAD means
// Absent, an ignored path is Ignored, and anything else is not in HEAD yet (Absent).
func HeadBlob(dir, path string) (string, Baseline, error) {
	if dir == "" || path == "" {
		return "", BaselineNone, errors.New("no repo directory")
	}
	rel, err := repoRel(dir, path)
	if err != nil {
		// Outside the checkout: not this repo's file, so it has no baseline here.
		return "", BaselineNone, err
	}

	// No commits: every file is new.
	if !hasHEAD(dir) {
		return "", BaselineAbsent, nil
	}

	out, err := gitCapture(dir, "-c", "core.quotepath=false", "show", "HEAD:"+rel)
	if err == nil {
		return out, BaselineOK, nil
	}
	if IsIgnored(dir, rel) {
		return "", BaselineIgnored, nil
	}
	return "", BaselineAbsent, nil
}

// IsIgnored reports whether path (repo-relative or absolute) matches the ignore rules. A
// check-ignore failure reads as "not ignored", which shows the file.
func IsIgnored(dir, path string) bool {
	rel := path
	if filepath.IsAbs(path) {
		var err error
		if rel, err = repoRel(dir, path); err != nil {
			return false
		}
	}
	return gitCmd(context.Background(), dir, "check-ignore", "-q", "--", rel).Run() == nil
}

// HeadOID is the commit HEAD points at, or "": a key for invalidating cached baselines.
func HeadOID(dir string) string {
	return GitOutput(dir, "rev-parse", "HEAD")
}

// repoRel is path relative to the checkout root with '/' separators, as git wants; a path
// outside is an error. Both sides go through realPath: rev-parse resolves symlinks (on
// macOS /var is /private/var), and relating unresolved paths would put files outside their
// own repo.
func repoRel(dir, path string) (string, error) {
	rel, err := filepath.Rel(realPath(dir), realPath(path))
	if err != nil {
		return "", err
	}
	if rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", errors.New("path is outside the repository")
	}
	return filepath.ToSlash(rel), nil
}

// realPath makes p absolute with its directory's symlinks resolved (the base name is
// rejoined, so a not-yet-existing file still resolves). Each step falls back to the best
// answer so far.
func realPath(p string) string {
	abs, err := filepath.Abs(p)
	if err != nil {
		return filepath.Clean(p)
	}
	if real, err := filepath.EvalSymlinks(abs); err == nil {
		return real
	}
	dir, base := filepath.Split(abs)
	real, err := filepath.EvalSymlinks(filepath.Clean(dir))
	if err != nil {
		return abs
	}
	return filepath.Join(real, base)
}
