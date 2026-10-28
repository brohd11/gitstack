package repoui

import (
	"context"

	"github.com/brohd11/bubblestack/core"
	"github.com/brohd11/gitstack/repo"
)

// GitHookApp is an optional capability of Shared.App. It selects the app's
// ~/.<app>/hooks/config.yml without coupling the screens to an app context type.
type GitHookApp interface {
	GitHookApp() string
}

func hookContext(ctx context.Context, sh *core.Shared) context.Context {
	if app, ok := sh.App.(GitHookApp); ok {
		return repo.WithHookApp(ctx, app.GitHookApp())
	}
	return ctx
}
