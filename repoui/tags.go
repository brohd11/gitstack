package repoui

import (
	"context"
	"time"

	"github.com/brohd11/bubblestack/components"
	"github.com/brohd11/bubblestack/core"
	"github.com/brohd11/gitstack/repo"

	"github.com/charmbracelet/bubbles/list"
	tea "github.com/charmbracelet/bubbletea"
)

// The Tags screen: a sidebar of tag actions beside two stacked listings —
// origin's tags over the local ones — so "is the release tag pushed yet" answers
// itself at a glance. The remote half is a network read, so it loads async
// (remoteTagsCmd, kicked off by the screen's Init) while the local half renders
// immediately from disk. Only the listings exist so far; the action rows are
// placeholders that say so rather than silently doing nothing.

// remoteTagsTimeout caps the ls-remote behind the Tags screen, so an unreachable
// origin fails the pane into an error line instead of spinning forever. Shorter
// than FetchTimeout: it's one round-trip, not a fan-out.
const remoteTagsTimeout = 30 * time.Second

// remoteTagsMsg delivers one remoteTagsCmd load. ModularScreen fans non-key msgs
// out to every panel; the tagListPanel holding the remote listing claims it.
type remoteTagsMsg struct {
	tags []string
	err  error
}

// remoteTagsCmd lists origin's tags off the UI thread (repo.RemoteTags is a
// network read) and delivers the result as a remoteTagsMsg.
func remoteTagsCmd(dir string) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), remoteTagsTimeout)
		defer cancel()
		tags, err := repo.RemoteTags(ctx, dir)
		return remoteTagsMsg{tags: tags, err: err}
	}
}

// tagListPanel is the remote-tags ScrollContainer plus the one domain behavior a
// component can't carry: what a remoteTagsMsg means. Embedding promotes the
// Panel/Focusable plumbing, so the only override needed is UpdatePanel — claim
// the load result, otherwise behave exactly like a ScrollContainer.
type tagListPanel struct {
	*components.ScrollContainer
}

var _ components.Panel = (*tagListPanel)(nil)
var _ components.Focusable = (*tagListPanel)(nil)
var _ components.PanelUpdater = (*tagListPanel)(nil)
var _ components.PanelHelper = (*tagListPanel)(nil)

func (p *tagListPanel) UpdatePanel(sh *core.Shared, msg tea.Msg) (core.Action, bool) {
	if m, ok := msg.(remoteTagsMsg); ok {
		switch {
		case m.err != nil:
			p.SetStatus("could not reach origin: " + m.err.Error())
		case len(m.tags) == 0:
			p.SetStatus("origin has no tags")
		default:
			p.SetLines(m.tags)
		}
		return core.Action{}, true
	}
	return p.ScrollContainer.UpdatePanel(sh, msg)
}

// TagsScreen builds the tag view for one checkout. The sidebar's first three
// rows are placeholders — they answer enter with a status line saying so, which
// is honest where a dead row would read as broken; Fetch Tags is the one working
// action, re-running the remote load.
func TagsScreen(sh *core.Shared, r repo.Repo) *components.ModularScreen {
	remote := &tagListPanel{ScrollContainer: components.NewScrollContainer("Remote Tags")}
	remote.SetStatus("fetching remote tags…")

	local := components.NewScrollContainer("Local Tags")
	setLocal := func() {
		if tags := repo.LocalTags(r.Dir); len(tags) > 0 {
			local.SetLines(tags)
		} else {
			local.SetStatus("no local tags")
		}
	}
	setLocal()

	placeholder := func(action string) func(*core.Shared) core.Action {
		return func(*core.Shared) core.Action {
			return core.SetStatusAndLog(action + ": not implemented yet")
		}
	}

	sidebar := components.NewListPanel([]list.Item{
		components.Item{
			Name: "✚ New Tag",
			Desc: "tag the current commit",
			Pick: placeholder("new tag"),
		},
		components.Item{
			Name: "✖ Delete Tag",
			Desc: "remove a local tag",
			Pick: placeholder("delete tag"),
		},
		components.Item{
			Name: "⇧ Push Tags",
			Desc: "push local tags to origin",
			Pick: placeholder("push tags"),
		},
		components.Item{
			Name: "⟳ Fetch Tags",
			Desc: "re-read origin's tags",
			Pick: func(*core.Shared) core.Action {
				remote.SetStatus("fetching remote tags…")
				return core.Async(remoteTagsCmd(r.Dir))
			},
		},
	}, "Tags", components.ListPanelOpts{})

	return components.NewModularScreen(
		[][]components.Slot{
			{{Panel: sidebar}},
			{{Panel: remote, Weight: 1}, {Panel: local, Weight: 1}},
		},
		components.ModularOpts{
			Crumb:     "Tags",
			Dir:       r.Dir, // "t" opens a terminal at this repo from the Tags screen (DirLocator)
			ColWidths: []int{30, 0},
			Init:      func(*core.Shared) tea.Cmd { return remoteTagsCmd(r.Dir) },
			Refresh: func(_ *core.Shared, payload any) bool {
				if _, ok := payload.(RefreshMsg); !ok {
					return false
				}
				setLocal()
				return true
			},
		},
	)
}
