package repo

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// Reading a file's baseline — the copy of it that HEAD holds. This is what an editor
// needs to draw diff gutters, and it is deliberately NOT `git diff`.
//
// `git diff` describes what is on disk. An editor's gutter has to describe what is in
// the buffer, which is a different thing the moment anyone types: every keystroke would
// leave the markers a save behind. So the baseline is fetched once, and the consumer
// diffs it against its own live text as often as it likes — no git process per
// keystroke, and markers that move while you edit.
//
// The split also decides where the work goes. Fetching a blob is git's job and belongs
// here; diffing two strings is not, and belongs to whoever owns the buffer.

// Baseline says what HEAD had to offer, which is as much a part of the answer as the
// text is: a file with no baseline is not a file with no changes. Consumers render the
// four cases differently — OK diffs, Absent is wholly new, Ignored and None get no
// gutter at all.
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

// RepoRoot is the checkout path enclosing path, found by asking git rather than by
// walking for a .git entry: only git knows about worktrees, submodules and $GIT_DIR, and
// a hand-rolled walk gets all three wrong. path may name a file or a directory; a file's
// directory is what gets asked.
//
// This looks UP from a path, which is what an open document needs. FindGitRepos (repo.go)
// looks DOWN from a base for many, which is what the repo lists need — the two are not
// substitutes for each other.
func RepoRoot(path string) (string, bool) {
	dir := path
	if fi, err := os.Stat(path); err == nil && !fi.IsDir() {
		dir = filepath.Dir(path)
	}
	if dir == "" {
		return "", false
	}
	root := GitOutput(dir, "rev-parse", "--show-toplevel")
	if root == "" {
		return "", false
	}
	return filepath.Clean(root), true
}

// HeadBlob returns HEAD's copy of path, which must lie inside the checkout at dir (see
// RepoRoot). The Baseline is meaningful on every return, error or not; the text is only
// meaningful with BaselineOK.
//
// The disambiguation is the whole subtlety. `git show HEAD:<p>` failing says nothing on
// its own — the file might be untracked, ignored, outside the repo, or the repo might
// have no commits — and every one of those wants a different gutter. So a failure is
// narrowed rather than reported: no HEAD at all (a fresh `git init`) means every file is
// new, an ignored path says so, and what is left is simply not in HEAD yet.
func HeadBlob(dir, path string) (string, Baseline, error) {
	if dir == "" || path == "" {
		return "", BaselineNone, errors.New("no repo directory")
	}
	rel, err := repoRel(dir, path)
	if err != nil {
		// Outside the checkout: not this repo's file, so it has no baseline here.
		return "", BaselineNone, err
	}

	// A repo with no commits has no HEAD to show from, and asking anyway gets git's
	// "fatal: ambiguous argument 'HEAD'". Every file in such a repo is new, which is
	// exactly what Absent means.
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

// IsIgnored reports whether path (repo-relative, or absolute inside dir) is matched by
// the repo's ignore rules. check-ignore exits 0 for a match, 1 for none, and >1 for a
// real failure — which is read as "not ignored", the answer that shows the user their
// file rather than hiding it.
func IsIgnored(dir, path string) bool {
	rel := path
	if filepath.IsAbs(path) {
		var err error
		if rel, err = repoRel(dir, path); err != nil {
			return false
		}
	}
	cmd := exec.Command("git", "-C", dir, "check-ignore", "-q", "--", rel)
	cmd.Env = GitEnv()
	return cmd.Run() == nil
}

// HeadOID is the commit HEAD points at, or "" when there is none. It is the cache key a
// consumer invalidates baselines against: a commit, a checkout or a rebase moves HEAD,
// and every baseline read before it is then a diff against the wrong thing.
func HeadOID(dir string) string {
	return GitOutput(dir, "rev-parse", "HEAD")
}

// repoRel is path as git wants to hear it: relative to the checkout root and
// slash-separated, because git speaks '/' on every platform including the one where
// filepath does not. A path outside the checkout is an error rather than a "../" that
// git would reject less clearly.
//
// Both sides go through realPath first, and that is not defensive tidying — it is the
// difference between working and not on macOS. RepoRoot's answer comes from
// `rev-parse --show-toplevel`, which resolves symlinks, so a checkout reached through one
// (/tmp and /var both are on macOS, and plenty of people keep their code under one) comes
// back as /private/var/... while the document is still /var/.... Relating those two
// unresolved says the file is outside its own repository, and every such file silently
// loses its baseline.
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

// realPath is p absolute and with symlinks resolved. Only the DIRECTORY is resolved and
// the base name rejoined, so a file that does not exist yet — a new document about to be
// saved — still lands under the same root as its neighbors; EvalSymlinks on the file
// itself would fail and leave it unresolved. Every step falls back to the best answer so
// far, since a path that cannot be resolved is still worth relating literally.
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
