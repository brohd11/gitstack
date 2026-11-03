package repo

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"gopkg.in/yaml.v3"
)

type hookRecord struct {
	Root, Name, Tag, Version, Operation, Event, Cwd string
	Args                                            []string
}

// Use the test executable as a portable external command; no shell or scripting
// runtime is needed on a test runner, including Windows.
func TestHookCommandProcess(t *testing.T) {
	var args []string
	for i, a := range os.Args {
		if a == "--hook-helper" {
			args = os.Args[i+1:]
			break
		}
	}
	if len(args) == 0 {
		return
	}
	cwd, _ := os.Getwd()
	r := hookRecord{
		Root: os.Getenv("RM_REPO_ROOT"), Name: os.Getenv("RM_REPO_NAME"),
		Tag: os.Getenv("RM_TAG"), Version: os.Getenv("RM_VERSION"),
		Operation: os.Getenv("RM_OPERATION"), Event: os.Getenv("RM_HOOK"),
		Cwd: cwd, Args: args[2:],
	}
	f, err := os.OpenFile(args[1], os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(2)
	}
	_ = json.NewEncoder(f).Encode(r)
	_ = f.Close()
	fmt.Println("stdout: 100% literal")
	fmt.Fprintln(os.Stderr, "stderr: hook details")
	switch args[0] {
	case "fail":
		os.Exit(7)
	case "wait":
		time.Sleep(30 * time.Second) // the parent cancels this process after it records startup
	}
	os.Exit(0)
}

func hookHome(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	return home
}

func TestEnsureHooks(t *testing.T) {
	home := hookHome(t)
	for _, app := range []string{"gdaddon", "repoview"} {
		t.Run(app, func(t *testing.T) {
			created, path, err := EnsureHooks(app)
			if err != nil || !created || path != filepath.Join(home, "."+app, "hooks", "config.yml") {
				t.Fatalf("EnsureHooks = %v, %q, %v", created, path, err)
			}
			seed, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			cfg, err := parseHooks(seed)
			if err != nil || len(cfg.Hooks) != 12 {
				t.Fatalf("generated config is missing events or invalid: %v\n%s", err, seed)
			}
			for _, event := range []string{
				"pre_fetch", "post_fetch", "pre_pull", "post_pull", "pre_push", "post_push",
				"pre_commit", "post_commit", "pre_tag", "post_tag", "pre_delete_tag", "post_delete_tag",
			} {
				if !strings.Contains(string(seed), event+": []") || len(cfg.Hooks[event]) != 0 {
					t.Errorf("expected empty list for %s:\n%s", event, seed)
				}
			}
			if !strings.HasPrefix(string(seed), "# Commands run from the repository root.") ||
				!strings.Contains(string(seed), "#   - command: python") ||
				!strings.Contains(string(seed), `#     args: ["~/hooks/check-version.py"]`) {
				t.Errorf("missing commented command example:\n%s", seed)
			}
			// Even an empty or malformed file belongs to the user; startup
			// must never replace it while trying to restore defaults.
			for _, custom := range []string{"", "# keep my comment\nhooks: {}\n", "hooks: [invalid"} {
				if err := os.WriteFile(path, []byte(custom), 0o600); err != nil {
					t.Fatal(err)
				}
				created, _, err = EnsureHooks(app)
				if err != nil || created {
					t.Fatalf("existing file should be left alone: created=%v, err=%v", created, err)
				}
				data, err := os.ReadFile(path)
				if err != nil || string(data) != custom {
					t.Fatalf("existing config changed: %q, %v", data, err)
				}
			}
			if err := os.Remove(path); err != nil {
				t.Fatal(err)
			}
			created, _, err = EnsureHooks(app)
			if err != nil || !created {
				t.Fatalf("deleted file was not recreated: created=%v, err=%v", created, err)
			}
			data, err := os.ReadFile(path)
			if err != nil || string(data) != string(seed) {
				t.Fatalf("recreated file differs from defaults: %q, %v", data, err)
			}
		})
	}
}

func helperHook(t *testing.T, mode, log string, args ...string) hookCommand {
	t.Helper()
	bin, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	return hookCommand{Command: bin, Args: append([]string{"-test.run=^TestHookCommandProcess$", "--", "--hook-helper", mode, log}, args...)}
}

func writeHooks(t *testing.T, home, app string, hooks map[string][]hookCommand) {
	t.Helper()
	data, err := yaml.Marshal(hookConfig{Hooks: hooks})
	if err != nil {
		t.Fatal(err)
	}
	writeHookConfig(t, home, app, data)
}

func writeHookConfig(t *testing.T, home, app string, data []byte) {
	t.Helper()
	dir := filepath.Join(home, "."+app, "hooks")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "config.yml"), data, 0o600); err != nil {
		t.Fatal(err)
	}
}

func readHookRecords(t *testing.T, path string) []hookRecord {
	t.Helper()
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		t.Fatal(err)
	}
	var records []hookRecord
	for _, line := range strings.Split(strings.TrimSpace(string(data)), "\n") {
		var r hookRecord
		if err := json.Unmarshal([]byte(line), &r); err != nil {
			t.Fatal(err)
		}
		records = append(records, r)
	}
	return records
}

func TestHookTagContextAndAppIsolation(t *testing.T) {
	home := hookHome(t)
	_, work := upstreamClone(t)
	log := filepath.Join(t.TempDir(), "hook records.jsonl")
	literal := []string{"argument with spaces", `$(echo nope)`, "$RM_TAG", "*", "a|b && c", "100%", "~/script with spaces.py"}
	writeHooks(t, home, "gdaddon", map[string][]hookCommand{
		"pre_tag":  {helperHook(t, "record", log, literal...), helperHook(t, "record", log, "second")},
		"post_tag": {helperHook(t, "record", log, "third")},
	})
	// Stale inherited context must be overwritten, including values left empty.
	t.Setenv("RM_TAG", "stale")
	t.Setenv("RM_HOOK", "stale")
	var lines []string
	report := func(format string, args ...any) { lines = append(lines, fmt.Sprintf(format, args...)) }
	ctx := WithHookApp(context.Background(), "gdaddon")
	if err := GitTag(ctx, work, "v1.4.0", report); err != nil {
		t.Fatal(err)
	}
	records := readHookRecords(t, log)
	if len(records) != 3 {
		t.Fatalf("records = %+v", records)
	}
	// Git prints forward slashes on Windows; hooks see the filepath.Clean'd root.
	root := filepath.Clean(GitOutput(work, "rev-parse", "--show-toplevel"))
	for i, r := range records {
		event := "pre_tag"
		if i == 2 {
			event = "post_tag"
		}
		if r.Root != root || r.Cwd != root || r.Name != filepath.Base(root) || r.Tag != "v1.4.0" || r.Version != "1.4.0" || r.Operation != "tag" || r.Event != event {
			t.Fatalf("unexpected context: %+v", r)
		}
	}
	literal[len(literal)-1] = filepath.Join(home, "script with spaces.py")
	if !reflect.DeepEqual(records[0].Args, literal) || records[1].Args[0] != "second" || records[2].Args[0] != "third" {
		t.Fatalf("arguments/order changed: %+v", records)
	}
	output := strings.Join(lines, "\n")
	if !strings.Contains(output, "stdout: 100% literal") || !strings.Contains(output, "stderr: hook details") {
		t.Fatalf("missing hook output: %s", output)
	}
	for i, app := range []string{"repoview", ""} {
		if err := GitTag(WithHookApp(context.Background(), app), work, fmt.Sprintf("v2.0.%d", i), report); err != nil {
			t.Fatal(err)
		}
	}
	if len(readHookRecords(t, log)) != 3 {
		t.Fatal("gdaddon hooks leaked into another app or an unconfigured caller")
	}
}

func TestHookFailures(t *testing.T) {
	for _, phase := range []string{"pre", "post", "git"} {
		t.Run(phase, func(t *testing.T) {
			home := hookHome(t)
			_, work := upstreamClone(t)
			log := filepath.Join(t.TempDir(), "records")
			pre, post := "record", "record"
			if phase == "pre" {
				pre = "fail"
			}
			if phase == "post" {
				post = "fail"
			}
			if phase == "git" {
				git(t, work, "tag", "v1.0.0")
			}
			writeHooks(t, home, "repoview", map[string][]hookCommand{
				"pre_tag":  {helperHook(t, pre, log), helperHook(t, "record", log)},
				"post_tag": {helperHook(t, post, log), helperHook(t, "record", log)},
			})
			err := GitTag(WithHookApp(context.Background(), "repoview"), work, "v1.0.0", func(string, ...any) {})
			if err == nil {
				t.Fatal("expected failure")
			}
			var he *HookError
			if phase != "git" {
				if !errors.As(err, &he) || he.Completed != (phase == "post") || !strings.Contains(err.Error(), "exit status 7") {
					t.Fatalf("unexpected hook error: %v", err)
				}
			} else if errors.As(err, &he) {
				t.Fatalf("git failure reported as hook failure: %v", err)
			}
			wantRecords := map[string]int{"pre": 1, "post": 3, "git": 2}[phase]
			if records := readHookRecords(t, log); len(records) != wantRecords {
				t.Fatalf("hooks didn't stop on failure: %+v", records)
			}
			if exists := GitOutput(work, "tag", "--list", "v1.0.0") != ""; exists != (phase != "pre") {
				t.Fatalf("unexpected tag existence: %v", exists)
			}
		})
	}
}

func TestHookRelativeExecutable(t *testing.T) {
	home := hookHome(t)
	_, work := upstreamClone(t)
	log := filepath.Join(t.TempDir(), "records")
	command := helperHook(t, "record", log)
	data, err := os.ReadFile(command.Command)
	if err != nil {
		t.Fatal(err)
	}
	command.Command = filepath.Join(".", ".git", "hook command.exe")
	if err := os.WriteFile(filepath.Join(work, command.Command), data, 0o755); err != nil {
		t.Fatal(err)
	}
	writeHooks(t, home, "gdaddon", map[string][]hookCommand{"pre_tag": {command}})
	if err := GitTag(WithHookApp(context.Background(), "gdaddon"), work, "v1.0.0", func(string, ...any) {}); err != nil {
		t.Fatal(err)
	}
	if len(readHookRecords(t, log)) != 1 {
		t.Fatal("relative executable did not run from the checkout")
	}
}

func TestHookConfigFailsClosed(t *testing.T) {
	home := hookHome(t)
	_, work := upstreamClone(t)
	ctx := WithHookApp(context.Background(), "repoview")
	report := func(string, ...any) {}
	for _, data := range []string{
		"hooks: [", "hook: {}", "hooks:\n  pre_tagg: []",
		"hooks:\n  pre_tag:\n    - args: [oops]",
		"hooks:\n  pre_tag:\n    - command: git\n      arg: [oops]",
		"hooks:\n  pre_tag: 'echo shell command'",
		"hooks: {}\n---\nhooks: {}",
	} {
		writeHookConfig(t, home, "repoview", []byte(data))
		if err := GitTag(ctx, work, "v1.0.0", report); err == nil || !strings.Contains(err.Error(), "config.yml") {
			t.Fatalf("config %q: expected actionable error, got %v", data, err)
		}
		if len(LocalTags(work)) > 0 {
			t.Fatal("invalid config allowed tag creation")
		}
	}
	// Missing, empty and explicitly empty configs are optional, not failures.
	for i, data := range []string{"", "# no hooks", "hooks: {}"} {
		writeHookConfig(t, home, "repoview", []byte(data))
		if err := GitTag(ctx, work, fmt.Sprintf("empty-%d", i), report); err != nil {
			t.Fatal(err)
		}
	}
}

func TestHookOperations(t *testing.T) {
	home := hookHome(t)
	remote, work := upstreamClone(t)
	git(t, remote, "config", "receive.denyCurrentBranch", "ignore")
	report := func(string, ...any) {}
	ctx := WithHookApp(context.Background(), "repoview")
	for _, tc := range []struct {
		name, operation, tag string
		run                  func() error
	}{
		{"fetch", "fetch", "", func() error { return GitFetch(ctx, work) }},
		{"pull", "pull", "", func() error { return GitPull(ctx, work, report) }},
		{"commit", "commit", "", func() error {
			_ = os.WriteFile(filepath.Join(work, "new"), []byte("new"), 0o600)
			return GitCommit(ctx, work, "new", true, report)
		}},
		{"push", "push", "", func() error { return GitPush(ctx, work, report) }},
		{"tag", "tag", "v2.0.0", func() error { return GitTag(ctx, work, "v2.0.0", report) }},
		{"push tag", "push", "v2.0.0", func() error { return GitPushTag(ctx, work, "v2.0.0", report) }},
		{"delete tag", "delete_tag", "v2.0.0", func() error { return GitDeleteTag(ctx, work, "v2.0.0", report) }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			log := filepath.Join(t.TempDir(), "records")
			writeHooks(t, home, "repoview", map[string][]hookCommand{
				"pre_" + tc.operation:  {helperHook(t, "record", log)},
				"post_" + tc.operation: {helperHook(t, "record", log)},
			})
			if err := tc.run(); err != nil {
				t.Fatal(err)
			}
			records := readHookRecords(t, log)
			if len(records) != 2 || records[0].Event != "pre_"+tc.operation || records[1].Event != "post_"+tc.operation {
				t.Fatalf("wrong hooks: %+v", records)
			}
			for _, r := range records {
				if r.Operation != tc.operation || r.Tag != tc.tag || r.Version != strings.TrimPrefix(tc.tag, "v") {
					t.Fatalf("wrong context: %+v", r)
				}
			}
		})
	}
}

func TestPreCommitFailureDoesNotStage(t *testing.T) {
	home := hookHome(t)
	_, work := upstreamClone(t)
	_ = os.WriteFile(filepath.Join(work, "new"), []byte("new"), 0o600)
	log := filepath.Join(t.TempDir(), "records")
	writeHooks(t, home, "gdaddon", map[string][]hookCommand{"pre_commit": {helperHook(t, "fail", log)}})
	before := GitOutput(work, "rev-parse", "HEAD")
	err := GitCommit(WithHookApp(context.Background(), "gdaddon"), work, "blocked", true, func(string, ...any) {})
	if err == nil || GitOutput(work, "diff", "--cached", "--name-only") != "" || GitOutput(work, "rev-parse", "HEAD") != before {
		t.Fatalf("pre-commit failure staged or committed changes: %v", err)
	}
}

func TestFetchAllCapturesHookOutput(t *testing.T) {
	home := hookHome(t)
	_, work := upstreamClone(t)
	log := filepath.Join(t.TempDir(), "records")
	writeHooks(t, home, "gdaddon", map[string][]hookCommand{"pre_fetch": {helperHook(t, "record", log)}})
	results := FetchAll(WithHookApp(context.Background(), "gdaddon"), []Repo{{Name: "work", Dir: work}})
	if len(results) != 1 || results[0].Err != nil || !strings.Contains(strings.Join(results[0].Output, "\n"), "stderr: hook details") {
		t.Fatalf("fetch lost hook output: %+v", results)
	}
}

func TestHookCancellation(t *testing.T) {
	home := hookHome(t)
	_, work := upstreamClone(t)
	log := filepath.Join(t.TempDir(), "records")
	writeHooks(t, home, "gdaddon", map[string][]hookCommand{"pre_tag": {helperHook(t, "wait", log)}})
	ctx, cancel := context.WithTimeout(WithHookApp(context.Background(), "gdaddon"), 5*time.Second)
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- GitTag(ctx, work, "v1.0.0", func(string, ...any) {}) }()
	for {
		if _, err := os.Stat(log); err == nil {
			break
		}
		select {
		case err := <-done:
			t.Fatalf("hook exited before cancellation: %v", err)
		case <-ctx.Done():
			t.Fatal("hook did not start")
		case <-time.After(10 * time.Millisecond):
		}
	}
	cancel()
	select {
	case err := <-done:
		if err == nil || len(LocalTags(work)) != 0 {
			t.Fatalf("cancelled pre-hook allowed tag: %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("cancellation did not stop hook")
	}
}
