// Package repocmd is the `repos` subcommand: find every git repo under a directory and
// run a shell command in each. Apps pass their help name, default depth and depth
// resolver.
package repocmd

import (
	"bytes"
	"encoding/base64"
	"encoding/binary"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"unicode/utf16"

	"github.com/brohd11/gitstack/repo"

	"github.com/spf13/cobra"
)

// Options configures the command for one app.
type Options struct {
	// AppName is the binary name shown in the help examples ("repoview repos -- ...").
	AppName string

	// DefaultDepth is the --depth default. repoview scans 1 level, gdaddon 5.
	DefaultDepth int

	// TopLevelOnly defaults discovery to the first checkout on each directory branch.
	// --nested restores recursive discovery. False preserves the recursive default.
	TopLevelOnly bool

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
		nested      bool
	)

	app := opts.AppName
	if app == "" {
		app = "app"
	}

	cmd := &cobra.Command{
		Use:   "repos [flags] -- <command...>",
		Short: "Run a shell command in git repos under a directory",
		Long: fmt.Sprintf(`Walk a directory tree, find git repos (the scanned directory itself is
excluded unless --include-root), and run a shell command inside each one.

With --nested=false, stop descending once a repo is found beneath the scanned directory.
Use --nested to also gather repos inside those repos, within the same depth limit.

%[2]s

  %[1]s repos -- git status -s
  %[1]s repos --dirty -- git fetch
  %[1]s repos -- "%[3]s"

By default output is captured and a header is printed only for repos that produced
output; use --raw to live-stream output instead.`, append([]any{app}, shellHelp(runtime.GOOS)...)...),
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
				skipNested:  !nested,
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
	cmd.Flags().BoolVar(&nested, "nested", !opts.TopLevelOnly, "also gather repos nested inside discovered repos")
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
	skipNested  bool
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

	repos, err := repo.FindGitRepos(base, cfg.depth, repo.ScanOptions{SkipNested: cfg.skipNested})
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
	argv := shellArgv(runtime.GOOS, exec.LookPath, cmdStr)

	for _, rel := range repos {
		full := filepath.Join(base, rel)
		display := filepath.Join(prefix, rel)
		c := exec.CommandContext(cmd.Context(), argv[0], argv[1:]...)
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

// shellArgv is how a joined command runs: "sh -c" on Unix. Windows has no sh unless Git
// Bash is on PATH, so it uses PowerShell: pwsh when installed, else the built-in
// powershell.exe. -EncodedCommand (base64 UTF-16LE) sidesteps PowerShell re-parsing the
// quotes Go's argv escaping produces.
func shellArgv(goos string, lookPath func(string) (string, error), cmdStr string) []string {
	if goos != "windows" {
		return []string{"sh", "-c", cmdStr}
	}
	shell := "powershell"
	if _, err := lookPath("pwsh"); err == nil {
		shell = "pwsh"
	}
	units := utf16.Encode([]rune(cmdStr))
	buf := make([]byte, 2*len(units))
	for i, u := range units {
		binary.LittleEndian.PutUint16(buf[2*i:], u)
	}
	return []string{shell, "-NoProfile", "-NonInteractive", "-EncodedCommand", base64.StdEncoding.EncodeToString(buf)}
}

// shellHelp is the help text's shell paragraph and pipe example for goos.
func shellHelp(goos string) []any {
	if goos == "windows" {
		return []any{`The command is joined and run via PowerShell (pwsh, else powershell.exe), so
pipes and redirects work (&& needs pwsh) — quote them as a single argument so your
own shell doesn't consume them first:`, "git log --oneline | Select-Object -First 1"}
	}
	return []any{`The command is joined and run via "sh -c", so pipes, &&, and redirects work — quote
them as a single argument so your own shell doesn't consume them first:`, "git log --oneline | head -1"}
}

func header(w io.Writer, text string) {
	line := strings.Repeat("-", 50)
	fmt.Fprintf(w, "\n%s\n📁 %s\n%s\n", line, text, line)
}
