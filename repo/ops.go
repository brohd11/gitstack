package repo

import (
	"context"
	"strconv"

	"github.com/brohd11/goutil/stream"
)

// This file holds the git operations that *change* a repo (pull/push/commit) or stream
// their output to a UI, as opposed to repo.go's read-only probes. They exist so the
// routine half of working a checkout — see what changed, pull, commit, push — can happen
// without leaving the app. They are deliberately not a git client: anything needing a
// decision (a diverged branch, a conflict, a rebase) fails and leaves the repo untouched
// for the user to sort out in a real terminal.

// GitStream runs a git command in dir, relaying its output to report one line at a time as
// it arrives, and returns a non-nil error when git exits non-zero (with git's own last
// words folded in, since that's the part worth reading). stdout and stderr are interleaved
// the way a terminal would show them — git writes progress and errors to stderr, and a
// caller streaming to a log wants both. ctx cancellation kills the subprocess, which is how
// a TUI's task-abort works. The streaming machinery itself (line splitting, the flush, the
// error folding) lives in goutil/stream; this is just the git-shaped call into it.
func GitStream(ctx context.Context, dir string, report Reporter, args ...string) error {
	return stream.Cmd(ctx, "", GitEnv(), report, append([]string{"git", "-C", dir}, args...)...)
}

// GitStatus streams the working tree's state: `status -sb`, the short form with a branch
// header ("## main...origin/main [behind 1]") — the same facts as a full `git status`
// without the paragraphs of hints, which read poorly in a log pane.
func GitStatus(ctx context.Context, dir string, report Reporter) error {
	return GitStream(ctx, dir, report, "status", "-sb")
}

// GitPull fast-forwards the checkout to its upstream. --ff-only is the whole point: when
// the branch has diverged (local commits *and* new upstream ones) git aborts and changes
// nothing, rather than starting a merge that could leave conflict markers in the working
// tree — or block on an editor — with the user still inside a TUI. Reconciling a divergence
// is a decision, so it belongs in a terminal.
func GitPull(ctx context.Context, dir string, report Reporter) error {
	return GitStream(ctx, dir, report, "pull", "--ff-only")
}

// GitPush pushes the current branch to its upstream. A branch with no upstream fails here
// (git asks for `--set-upstream`), which is the intended outcome: that's a decision about
// where the branch lives, not a routine push.
func GitPush(ctx context.Context, dir string, report Reporter) error {
	return GitStream(ctx, dir, report, "push")
}

// GitCommit commits the working tree with message. stageAll decides what "all" means — the
// distinction the commit form makes the user choose, because git's own answer is a trap:
//
//   - false: `commit -a` stages modifications and deletions to *tracked* files. A file you
//     just created is untracked, so it is NOT committed.
//   - true: `add -A` first, so new files are included too.
//
// The message is passed as an exec argument, never through a shell, so quotes, newlines,
// and `$` in it need no escaping.
func GitCommit(ctx context.Context, dir, message string, stageAll bool, report Reporter) error {
	if stageAll {
		report("%s", "$ git add -A")
		if err := GitStream(ctx, dir, report, "add", "-A"); err != nil {
			return err
		}
	}
	report("%s", "$ git commit -a -m "+strconv.Quote(message))
	return GitStream(ctx, dir, report, "commit", "-a", "-m", message)
}
