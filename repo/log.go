package repo

import (
	"errors"
	"strconv"
	"strings"
)

// This file reads commit history for the UI to render. Like diff.go it captures whole
// stdout through gitCapture rather than going through GitStream (whose line splitting drops
// empty lines — a blank line inside a commit message is content) or GitOutput (which
// swallows every error to "", rendering a log that failed as a repo with no history).
//
// Nothing here passes `-c color.ui=always`, for the same reason diff.go states: the renderer
// applies its own theme-aware color, and git's ANSI would only have to be stripped back out
// before the records could be split.

// LogLimit is how many commits a log capture reads by default. A repo's whole history can
// run to five figures, and every one of those commits would be parsed, held, and re-rendered
// into one string on each mode toggle — for a view whose job is answering "what happened
// recently". Callers that hit the cap are told so (see LogScreen), rather than being quietly
// shown a partial history.
const LogLimit = 500

// The field and record separators. ASCII unit/record separator are chosen precisely because
// git will never emit them from the fields being read: a commit message containing 0x1f
// would have to have been written with one in it.
const (
	fieldSep  = "\x1f"
	recordSep = "\x1e"
)

// logFormat is one commit per record: hash, abbreviated hash, parents, decorations, author
// name and email, date, subject, body.
const logFormat = "%H" + fieldSep + "%h" + fieldSep + "%P" + fieldSep + "%D" +
	fieldSep + "%an" + fieldSep + "%ae" + fieldSep + "%ad" + fieldSep + "%s" +
	fieldSep + "%b" + recordSep

// logFields is how many fields logFormat produces; a record that splits into anything else
// is malformed and skipped rather than read at the wrong offsets.
const logFields = 9

// RefKind classifies a decoration, which is what decides how it's colored — git's own
// palette gives each kind a different color, and that coloring is the whole reason to
// classify them here rather than pass the decoration text through as one string.
type RefKind int

const (
	RefBranch RefKind = iota // a local branch
	RefRemote                // a remote-tracking branch (origin/main)
	RefTag                   // a tag
	RefHead                  // HEAD, and the branch it points at when it isn't detached
)

// Ref is one decoration on a commit. For RefHead, Name is the branch HEAD points at, or ""
// when the checkout is detached.
type Ref struct {
	Name string
	Kind RefKind
}

// Commit is one entry of the log. Body is the message below the subject, which is empty for
// most commits and several paragraphs for the ones that matter.
type Commit struct {
	Hash    string
	Short   string
	Parents []string // two or more means a merge
	Refs    []Ref
	Author  string
	Email   string
	Date    string
	Subject string
	Body    string
}

// Merge reports whether the commit has more than one parent.
func (c Commit) Merge() bool { return len(c.Parents) > 1 }

// ErrNoCommits is returned by Log for a repo that has no commits. It's a sentinel rather
// than a plain error because it isn't a failure: git's own answer ("fatal: your current
// branch 'main' does not have any commits yet") explains nothing to someone who just pressed
// Log, and a caller that reports it as a failed read would be describing an empty history as
// a broken one. Callers test for it with errors.Is and say so plainly.
var ErrNoCommits = errors.New("this repo has no commits yet")

// Log reads dir's most recent commits, newest first, up to limit (LogLimit when limit is not
// positive).
func Log(dir string, limit int) ([]Commit, error) {
	if dir == "" {
		return nil, errors.New("no repo directory")
	}
	if limit <= 0 {
		limit = LogLimit
	}
	if !hasHEAD(dir) {
		return nil, ErrNoCommits
	}

	out, err := gitCapture(dir, "-c", "core.quotepath=false", "log", "--no-color",
		"-n", strconv.Itoa(limit), "--date=default", "--pretty=format:"+logFormat)
	if err != nil {
		return nil, err
	}
	return parseLog(out, remoteSet(dir)), nil
}

// remoteSet is the repo's configured remote names, for telling `origin/main` (a remote
// branch, colored red) from `feature/main` (a local one, colored green). Reading them costs
// one extra git call per capture and is the only way to be right: a "/" in the name proves
// nothing, since branch names may contain them.
//
// A repo with no remotes — or a failed read — yields an empty set, which classifies every
// slash-bearing ref as a local branch. That's the correct answer for the no-remote case and
// the conservative one otherwise.
func remoteSet(dir string) map[string]bool {
	out := GitOutput(dir, "remote")
	if out == "" {
		return nil
	}
	set := make(map[string]bool)
	for _, name := range strings.Fields(out) {
		set[name] = true
	}
	return set
}

// parseLog splits the capture into commits. Records are separated by recordSep and joined by
// git with a newline, so every record after the first opens with one.
func parseLog(raw string, remotes map[string]bool) []Commit {
	records := strings.Split(raw, recordSep)
	commits := make([]Commit, 0, len(records))

	for _, rec := range records {
		rec = strings.TrimLeft(rec, "\n")
		if rec == "" {
			continue // the tail after the final separator
		}
		f := strings.Split(rec, fieldSep)
		if len(f) != logFields {
			continue
		}
		commits = append(commits, Commit{
			Hash:    f[0],
			Short:   f[1],
			Parents: strings.Fields(f[2]),
			Refs:    parseRefs(f[3], remotes),
			Author:  f[4],
			Email:   f[5],
			Date:    f[6],
			Subject: f[7],
			// %b ends in the newlines that separated it from the next record's fields, and
			// a message often carries a trailing blank line of its own besides.
			Body: strings.TrimRight(f[8], " \t\n"),
		})
	}
	return commits
}

// parseRefs reads a commit's decorations (%D) — "HEAD -> main, tag: v1.0.0, origin/main" —
// into classified refs. An empty decoration list yields nil.
func parseRefs(d string, remotes map[string]bool) []Ref {
	d = strings.TrimSpace(d)
	if d == "" {
		return nil
	}

	var refs []Ref
	for _, part := range strings.Split(d, ", ") {
		part = strings.TrimSpace(part)
		switch {
		case part == "":
			continue
		case strings.HasPrefix(part, "tag: "):
			refs = append(refs, Ref{Name: strings.TrimPrefix(part, "tag: "), Kind: RefTag})
		case part == "HEAD":
			// Detached: HEAD names a commit rather than a branch.
			refs = append(refs, Ref{Kind: RefHead})
		case strings.HasPrefix(part, "HEAD -> "):
			// One ref, not two: the arrow binds HEAD to the branch, and rendering them
			// separately would put a comma between them.
			refs = append(refs, Ref{Name: strings.TrimPrefix(part, "HEAD -> "), Kind: RefHead})
		case isRemoteRef(part, remotes):
			refs = append(refs, Ref{Name: part, Kind: RefRemote})
		default:
			refs = append(refs, Ref{Name: part, Kind: RefBranch})
		}
	}
	return refs
}

// isRemoteRef reports whether name's first path segment is one of the repo's remotes.
func isRemoteRef(name string, remotes map[string]bool) bool {
	i := strings.IndexByte(name, '/')
	return i > 0 && remotes[name[:i]]
}
