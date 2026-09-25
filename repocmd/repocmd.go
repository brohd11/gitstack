// Package repocmd is the `repos` subcommand: find every git repo under a directory and
// run a shell command in each. Apps pass their help name, default depth and depth
// resolver.
package repocmd

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/brohd11/gitstack/repo"

	"github.com/spf13/cobra"
)

// Options configures the command for one app.
type Options struct {
	// AppName is the binary name shown in the help examples ("repoview repos -- ...").
	AppName string

	// DefaultDepth is the --depth default. repoview scans 1 level, gdaddon 5.
	DefaultDepth int

	// DepthDefText overrides the default shown in --help (an app whose ResolveDepth reads an
	// environment variable says so here).
	DepthDefText string

	// ResolveDepth maps the flag to the depth used (see goutil/envopt); nil takes the flag.
	ResolveDepth func(flagDepth int, flagChanged bool) (int, error)
}

// New builds the `repos` command. The caller adds it to its own root.
func New(opts Options) *cobra.Command {
	var (
		dir         string
		raw         bool
		dirty       bool
		depthFlag   int
		includeRoot bool
	)

	app := opts.AppName
	if app == "" {
		app = "app"
	}

	cmd := &cobra.Command{
		Use:   "repos [flags] -- <command...>",
		Short: "Run a shell command in every git repo nested under a directory",
		Long: fmt.Sprintf(`Walk a directory tree, find every nested git repo (the top-level repo is
excluded unless --include-root), and run a shell command inside each one.

The command is joined and run via "sh -c", so pipes, &&, and redirects work — quote
them as a single argument so your own shell doesn't consume them first:

  %[1]s repos -- git status -s
  %[1]s repos --dirty -- git fetch
  %[1]s repos -- "git log --oneline | head -1"

By default output is captured and a header is printed only for repos that produced
output; use --raw to live-stream output instead.`, app),
		Args:          cobra.ArbitraryArgs,
		SilenceUsage:  true,
		SilenceErrors: false,
		RunE: func(cmd *cobra.Command, args []string) error {
			depth := depthFlag
			if opts.ResolveDepth != nil {
				var err error
				depth, err = opts.ResolveDepth(depthFlag, cmd.Flags().Changed("depth"))
				if err != nil {
					return err
				}
			}
			return run(cmd, args, runConfig{
				dir:         dir,
				raw:         raw,
				dirty:       dirty,
				depth:       depth,
				includeRoot: includeRoot,
			})
		},
	}

	// Stop flag parsing at the first non-flag token so the command's own -flags are
	// collected as args; "--" remains supported but optional.
	cmd.Flags().SetInterspersed(false)
	cmd.Flags().StringVarP(&dir, "dir", "C", "", "directory to scan (default: current directory)")
	cmd.Flags().BoolVar(&raw, "raw", false, "live-stream each repo's output instead of capturing it")
	cmd.Flags().BoolVar(&dirty, "dirty", false, "only repos with uncommitted changes")
	cmd.Flags().IntVar(&depthFlag, "depth", opts.DefaultDepth, "max directory depth to search")
	cmd.Flags().BoolVar(&includeRoot, "include-root", false, "also run in the top-level repo (the scanned dir itself)")
	if opts.DepthDefText != "" {
		cmd.Flags().Lookup("depth").DefValue = opts.DepthDefText
	}
	return cmd
}

type runConfig struct {
	dir         string
	raw         bool
	dirty       bool
	depth       int
	includeRoot bool
}

func run(cmd *cobra.Command, args []string, cfg runConfig) error {
	out := cmd.OutOrStdout()
	errOutW := cmd.ErrOrStderr()

	base := cfg.dir
	if base == "" {
		cwd, err := os.Getwd()
		if err != nil {
			return fmt.Errorf("could not get current working directory: %w", err)
		}
		base = cwd
	}
	base, err := filepath.Abs(base)
	if err != nil {
		return fmt.Errorf("could not resolve absolute path for %s: %w", base, err)
	}

	repos, err := repo.FindGitRepos(base, cfg.depth)
	if err != nil {
		return err
	}
	// --include-root adds base itself as "." when it is a checkout.
	if cfg.includeRoot {
		if _, ok := repo.DescribeRoot(base); ok {
			repos = append([]string{"."}, repos...)
		}
	}
	if cfg.dirty {
		dirty := repos[:0:0]
		for _, rel := range repos {
			if repo.HasUncommittedChanges(filepath.Join(base, rel)) {
				dirty = append(dirty, rel)
			}
		}
		repos = dirty
	}

	// No command: list mode — print the matching repo paths, one per line.
	if len(args) == 0 {
		for _, rel := range repos {
			fmt.Fprintln(out, rel)
		}
		return nil
	}

	cmdStr := strings.Join(args, " ")
	prefix := filepath.Base(base)

	for _, rel := range repos {
		full := filepath.Join(base, rel)
		display := filepath.Join(prefix, rel)
		c := exec.CommandContext(cmd.Context(), "sh", "-c", cmdStr)
		c.Dir = full

		if cfg.raw {
			header(out, display)
			c.Stdout = out
			c.Stderr = errOutW
			if err := c.Run(); err != nil {
				fmt.Fprintf(errOutW, "error in %s: %v\n", display, err)
			}
			continue
		}

		var stdout, stderr bytes.Buffer
		c.Stdout = &stdout
		c.Stderr = &stderr
		runErr := c.Run()

		outStr := strings.TrimSpace(stdout.String())
		errStr := strings.TrimSpace(stderr.String())
		if outStr != "" || errStr != "" {
			header(out, display)
			if outStr != "" {
				fmt.Fprintln(out, outStr)
			}
			if errStr != "" {
				fmt.Fprintln(errOutW, errStr)
			}
		}
		if runErr != nil {
			fmt.Fprintf(errOutW, "error in %s: %v\n", display, runErr)
		}
	}
	return nil
}

func header(w io.Writer, text string) {
	line := strings.Repeat("-", 50)
	fmt.Fprintf(w, "\n%s\n📁 %s\n%s\n", line, text, line)
}
