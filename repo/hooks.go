package repo

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/brohd11/goutil/configdir"
	"github.com/brohd11/goutil/stream"
	"github.com/brohd11/goutil/strutil"
	"gopkg.in/yaml.v3"
)

type hookAppKey struct{}

// WithHookApp enables ~/.<app>/hooks/config.yml for Git operations on ctx. It is
// scoped to the operation, so two apps can use gitstack without sharing hooks.
// Without it the engine runs no user commands. Config is re-read per operation.
func WithHookApp(ctx context.Context, app string) context.Context {
	return context.WithValue(ctx, hookAppKey{}, app)
}

type hookCommand struct {
	Command string   `yaml:"command"`
	Args    []string `yaml:"args,omitempty"`
}

type hookConfig struct {
	Hooks map[string][]hookCommand `yaml:"hooks"`
}

// Shared by validation and the generated config so new events appear in both.
var hookOperations = []string{"fetch", "pull", "push", "commit", "tag", "delete_tag"}

// EnsureHooks seeds ~/.<app>/hooks/config.yml with empty events when missing.
// Existing files are left untouched, including comments and invalid YAML.
func EnsureHooks(app string) (created bool, path string, err error) {
	path, err = hookConfigPath(app)
	if err != nil {
		return false, path, err
	}
	events := &yaml.Node{Kind: yaml.MappingNode}
	for _, operation := range hookOperations {
		for _, phase := range []string{"pre_", "post_"} {
			events.Content = append(events.Content,
				&yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: phase + operation},
				&yaml.Node{Kind: yaml.SequenceNode, Tag: "!!seq", Style: yaml.FlowStyle},
			)
		}
	}
	seed := &yaml.Node{
		Kind: yaml.MappingNode,
		HeadComment: `# Commands run from the repository root. Empty lists run nothing.
# Replace an empty list with commands, for example:
# pre_tag:
#   - command: python
#     args: ["~/hooks/check-version.py"]`,
		Content: []*yaml.Node{
			{Kind: yaml.ScalarNode, Tag: "!!str", Value: "hooks"},
			events,
		},
	}
	created, err = configdir.Ensure(filepath.Dir(path), filepath.Base(path), seed)
	if err != nil {
		return false, path, fmt.Errorf("create hooks %s: %w", path, err)
	}
	return created, path, nil
}

func hookConfigPath(app string) (string, error) {
	base, err := configdir.Dir(app)
	if err != nil {
		return "", err
	}
	return filepath.Join(base, "hooks", "config.yml"), nil
}

// HookError identifies a failed user command. Completed means Git already
// succeeded: a post-hook failure cannot undo a commit, tag, pull or push.
type HookError struct {
	Event     string
	Command   string
	Completed bool
	Err       error
}

func (e *HookError) Error() string {
	if e.Completed {
		return fmt.Sprintf("git action completed; %s hook %q failed: %v", e.Event, e.Command, e.Err)
	}
	return fmt.Sprintf("%s hook %q failed; git action aborted: %v", e.Event, e.Command, e.Err)
}

func (e *HookError) Unwrap() error { return e.Err }

func loadHooks(app string) (hookConfig, error) {
	path, err := hookConfigPath(app)
	if err != nil {
		return hookConfig{}, err
	}
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return hookConfig{}, nil
	}
	if err != nil {
		return hookConfig{}, fmt.Errorf("load hooks %s: %w", path, err)
	}
	cfg, err := parseHooks(data)
	if err != nil {
		return hookConfig{}, fmt.Errorf("load hooks %s: %w", path, err)
	}
	return cfg, nil
}

func parseHooks(data []byte) (hookConfig, error) {
	var cfg hookConfig
	d := yaml.NewDecoder(bytes.NewReader(data))
	d.KnownFields(true)
	if err := d.Decode(&cfg); err != nil && err != io.EOF {
		return cfg, err
	}
	var extra any
	if err := d.Decode(&extra); err != io.EOF {
		return cfg, fmt.Errorf("expected a single YAML document")
	}
	for event, commands := range cfg.Hooks {
		phase, operation, _ := strings.Cut(event, "_")
		if phase != "pre" && phase != "post" {
			return cfg, fmt.Errorf("unknown hook %q", event)
		}
		if !slices.Contains(hookOperations, operation) {
			return cfg, fmt.Errorf("unknown hook %q", event)
		}
		for i, c := range commands {
			if strings.TrimSpace(c.Command) == "" {
				return cfg, fmt.Errorf("%s hook %d: command is required", event, i+1)
			}
		}
	}
	return cfg, nil
}

// gitOperation surrounds exactly one logical action (including staging for a
// commit). GitStream remains a raw primitive and never dispatches hooks itself.
func gitOperation(ctx context.Context, dir, operation, tag string, report Reporter, run func() error) error {
	app, _ := ctx.Value(hookAppKey{}).(string)
	if app == "" {
		return run()
	}
	cfg, err := loadHooks(app)
	if err != nil {
		return err // fail closed before Git can change anything
	}
	pre, post := "pre_"+operation, "post_"+operation
	if len(cfg.Hooks[pre])+len(cfg.Hooks[post]) == 0 {
		return run()
	}
	root, err := RepoRootContext(ctx, dir)
	if err != nil {
		return err
	}
	if root == "" {
		return fmt.Errorf("cannot run %s hooks: %s is not a checkout", operation, dir)
	}
	if report == nil {
		report = func(string, ...any) {}
	}
	env := append(GitEnv(),
		"RM_REPO_ROOT="+root,
		"RM_REPO_NAME="+filepath.Base(root),
		"RM_TAG="+tag,
		"RM_VERSION="+strings.TrimPrefix(tag, "v"),
		"RM_OPERATION="+operation,
	)
	if err := cfg.run(ctx, root, pre, env, report); err != nil {
		return err
	}
	if err := run(); err != nil {
		return err
	}
	return cfg.run(ctx, root, post, env, report)
}

func (cfg hookConfig) run(ctx context.Context, root, event string, env []string, report Reporter) error {
	env = append(slices.Clone(env), "RM_HOOK="+event)
	for i, c := range cfg.Hooks[event] {
		err := ctx.Err()
		if err == nil {
			report("[%s %d/%d] %s", event, i+1, len(cfg.Hooks[event]), c.Command)
			err = c.run(ctx, root, env, report)
		}
		if err != nil {
			return &HookError{Event: event, Command: c.Command, Completed: strings.HasPrefix(event, "post_"), Err: err}
		}
	}
	return nil
}

func (c hookCommand) run(ctx context.Context, root string, env []string, report Reporter) error {
	argv := append([]string{c.Command}, c.Args...)
	for i, arg := range argv {
		// Only expand a standalone home path; argv is otherwise literal.
		if arg == "~" || strings.HasPrefix(arg, "~/") || strings.HasPrefix(arg, `~\`) {
			var err error
			argv[i], err = strutil.ExpandHome(arg)
			if err != nil {
				return err
			}
		}
	}
	// Resolve explicit relative executable paths before the command builder does
	// PATH lookup (particularly on Windows, where lookup precedes setting Dir).
	if !filepath.IsAbs(argv[0]) && strings.ContainsAny(argv[0], `/\`) {
		argv[0] = filepath.Join(root, argv[0])
	}
	return stream.Cmd(ctx, root, env, report, argv...)
}
