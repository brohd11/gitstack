package repo

import (
	"context"
	"strconv"

	"github.com/brohd11/goutil/stream"
)

// Git operations that change a repo (pull, push, commit) or stream output. Anything needing
// a decision (divergence, conflicts, rebases) fails and leaves the repo for a real
// terminal.

// GitStream runs git in dir, streaming interleaved stdout/stderr to report line by line; a
// non-zero exit returns an error with git's last line. Cancelling ctx kills it.
func GitStream(ctx context.Context, dir string, report Reporter, args ...string) error {
	return stream.Cmd(ctx, "", GitEnv(), report, append([]string{"git", "-C", dir}, args...)...)
}

// GitStatus streams `status -sb`: the branch header and short status, without the hints.
func GitStatus(ctx context.Context, dir string, report Reporter) error {
	return GitStream(ctx, dir, report, "status", "-sb")
}

// GitPull fast-forwards to the upstream. --ff-only makes a diverged branch abort unchanged
// instead of starting a merge (conflicts, an editor) inside a TUI.
func GitPull(ctx context.Context, dir string, report Reporter) error {
	return GitStream(ctx, dir, report, "pull", "--ff-only")
}

// GitPush pushes the current branch; a branch without an upstream fails, leaving that
// decision to the user.
func GitPush(ctx context.Context, dir string, report Reporter) error {
	return GitStream(ctx, dir, report, "push")
}

// GitCommit commits with message. stageAll chooses what "all" means:
//   - false: `commit -a`, tracked files only; new files are not committed.
//   - true: `add -A` first, including new files.
//
// The message is an exec argument, so it needs no escaping.
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
