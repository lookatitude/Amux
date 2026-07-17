package daemon

import (
	"context"

	v1 "github.com/amux-run/amux/api/v1"

	"github.com/amux-run/amux/internal/domain"
	"github.com/amux-run/amux/internal/rpcapi"
)

// This file implements the graph-mutating flows (workspaces, panes, surfaces
// metadata) as thin translations from rpcapi payloads to domain commands
// applied through the session actor. Every mutation is one deterministic
// command (ADR-0002 Apply); the returned revision lets the client correlate
// with the event stream.

// CreateWorkspace implements flow 5.
func (e *Engine) CreateWorkspace(ctx context.Context, p rpcapi.WorkspaceCreateParams) (rpcapi.WorkspaceCreateResult, error) {
	res, err := e.submit(ctx, domain.SessionID(p.Session), domain.CreateWorkspace{
		Name:         p.Name,
		PrimaryRoot:  p.PrimaryRoot,
		FirstPaneCwd: p.FirstPaneCwd,
	})
	if err != nil {
		return rpcapi.WorkspaceCreateResult{}, err
	}
	ev := res.Events[0].Payload.(domain.WorkspaceCreated)
	return rpcapi.WorkspaceCreateResult{
		Workspace:    string(ev.Workspace),
		FirstPane:    string(ev.FirstPane),
		FirstSurface: string(ev.FirstSurface),
		Rev:          res.Rev,
	}, nil
}

// ListWorkspaces implements flow 6.
func (e *Engine) ListWorkspaces(ctx context.Context, p rpcapi.WorkspaceListParams) (rpcapi.WorkspaceListResult, error) {
	st, err := e.state(ctx, domain.SessionID(p.Session))
	if err != nil {
		return rpcapi.WorkspaceListResult{}, err
	}
	var out rpcapi.WorkspaceListResult
	for _, wid := range st.WorkspaceOrder() {
		w, _ := st.Workspace(wid)
		out.Workspaces = append(out.Workspaces, rpcapi.WorkspaceInfo{
			ID:          string(wid),
			Name:        w.Name,
			PrimaryRoot: w.PrimaryRoot,
			Rev:         w.Rev(),
			PaneCount:   len(w.PaneOrder()),
			Focused:     string(w.Focused()),
		})
	}
	return out, nil
}

// RenameWorkspace renames a workspace.
func (e *Engine) RenameWorkspace(ctx context.Context, p rpcapi.WorkspaceRenameParams) (rpcapi.RevResult, error) {
	res, err := e.submit(ctx, domain.SessionID(p.Session), domain.RenameWorkspace{
		Workspace: domain.WorkspaceID(p.Workspace),
		Name:      p.Name,
	})
	if err != nil {
		return rpcapi.RevResult{}, err
	}
	return rpcapi.RevResult{Rev: res.Rev}, nil
}

// DestroyWorkspace removes a workspace and its subtree.
func (e *Engine) DestroyWorkspace(ctx context.Context, p rpcapi.WorkspaceDestroyParams) (rpcapi.RevResult, error) {
	res, err := e.submit(ctx, domain.SessionID(p.Session), domain.CloseWorkspace{
		Workspace: domain.WorkspaceID(p.Workspace),
	})
	if err != nil {
		return rpcapi.RevResult{}, err
	}
	return rpcapi.RevResult{Rev: res.Rev}, nil
}

// SplitPane implements flows 7 and 8.
func (e *Engine) SplitPane(ctx context.Context, p rpcapi.PaneSplitParams) (rpcapi.PaneSplitResult, error) {
	orient, err := orientation(p.Orientation)
	if err != nil {
		return rpcapi.PaneSplitResult{}, err
	}
	res, err := e.submit(ctx, domain.SessionID(p.Session), domain.SplitPane{
		Workspace:   domain.WorkspaceID(p.Workspace),
		Target:      domain.PaneID(p.Target),
		Orientation: orient,
		Ratio:       p.Ratio,
		NewPaneCwd:  p.NewPaneCwd,
	})
	if err != nil {
		return rpcapi.PaneSplitResult{}, err
	}
	ev := res.Events[0].Payload.(domain.PaneSplit)
	return rpcapi.PaneSplitResult{
		NewPane:    string(ev.NewPane),
		NewSurface: string(ev.NewSurface),
		Ratio:      ev.Ratio,
		Rev:        res.Rev,
	}, nil
}

// FocusPane implements flow 9.
func (e *Engine) FocusPane(ctx context.Context, p rpcapi.PaneFocusParams) (rpcapi.RevResult, error) {
	res, err := e.submit(ctx, domain.SessionID(p.Session), domain.FocusPane{
		Workspace: domain.WorkspaceID(p.Workspace),
		Pane:      domain.PaneID(p.Pane),
	})
	if err != nil {
		return rpcapi.RevResult{}, err
	}
	return rpcapi.RevResult{Rev: res.Rev}, nil
}

// ResizePane implements flow 10.
func (e *Engine) ResizePane(ctx context.Context, p rpcapi.PaneResizeParams) (rpcapi.RevResult, error) {
	res, err := e.submit(ctx, domain.SessionID(p.Session), domain.ResizePane{
		Workspace: domain.WorkspaceID(p.Workspace),
		Pane:      domain.PaneID(p.Pane),
		Ratio:     p.Ratio,
	})
	if err != nil {
		return rpcapi.RevResult{}, err
	}
	return rpcapi.RevResult{Rev: res.Rev}, nil
}

// ClosePane closes a pane.
func (e *Engine) ClosePane(ctx context.Context, p rpcapi.PaneCloseParams) (rpcapi.RevResult, error) {
	res, err := e.submit(ctx, domain.SessionID(p.Session), domain.ClosePane{
		Workspace: domain.WorkspaceID(p.Workspace),
		Pane:      domain.PaneID(p.Pane),
	})
	if err != nil {
		return rpcapi.RevResult{}, err
	}
	return rpcapi.RevResult{Rev: res.Rev}, nil
}

// InspectPane implements flow 15: a pane's full observable state, including the
// restore classification of each surface so nothing implies resurrection.
func (e *Engine) InspectPane(ctx context.Context, p rpcapi.PaneInspectParams) (rpcapi.PaneInspectResult, error) {
	st, err := e.state(ctx, domain.SessionID(p.Session))
	if err != nil {
		return rpcapi.PaneInspectResult{}, err
	}
	w, ok := st.Workspace(domain.WorkspaceID(p.Workspace))
	if !ok {
		return rpcapi.PaneInspectResult{}, &engineError{code: v1.ErrNotFound, msg: "workspace not found"}
	}
	pane, ok := w.Pane(domain.PaneID(p.Pane))
	if !ok {
		return rpcapi.PaneInspectResult{}, &engineError{code: v1.ErrNotFound, msg: "pane not found"}
	}
	rt, err := e.runtime(domain.SessionID(p.Session))
	if err != nil {
		return rpcapi.PaneInspectResult{}, err
	}
	out := rpcapi.PaneInspectResult{
		Pane:    string(pane.ID),
		Cwd:     pane.Cwd,
		Project: string(pane.Project),
		Focused: w.Focused() == pane.ID,
		Active:  string(pane.ActiveSurface()),
	}
	for _, sf := range pane.Surfaces() {
		info := rpcapi.SurfaceInfo{ID: string(sf.ID), Title: sf.Title, Active: sf.ID == pane.ActiveSurface()}
		rt.mu.Lock()
		if sr := rt.surfaces[sf.ID]; sr != nil {
			info.Class = string(sr.class)
			info.ExitReason = sr.reason
			if sr.ring != nil && sr.ring.LatestSeq() > out.LatestSeq {
				out.LatestSeq = sr.ring.LatestSeq()
			}
		}
		rt.mu.Unlock()
		out.Surfaces = append(out.Surfaces, info)
	}
	return out, nil
}

// SelectSurface changes a pane's active surface.
func (e *Engine) SelectSurface(ctx context.Context, p rpcapi.SurfaceSelectParams) (rpcapi.RevResult, error) {
	res, err := e.submit(ctx, domain.SessionID(p.Session), domain.SetActiveSurface{
		Workspace: domain.WorkspaceID(p.Workspace),
		Pane:      domain.PaneID(p.Pane),
		Surface:   domain.SurfaceID(p.Surface),
	})
	if err != nil {
		return rpcapi.RevResult{}, err
	}
	return rpcapi.RevResult{Rev: res.Rev}, nil
}

func orientation(o rpcapi.Orientation) (domain.SplitOrientation, error) {
	switch o {
	case rpcapi.OrientHorizontal:
		return domain.SplitHorizontal, nil
	case rpcapi.OrientVertical:
		return domain.SplitVertical, nil
	default:
		return 0, &engineError{code: v1.ErrInvalidArgument, msg: "orientation must be horizontal or vertical"}
	}
}
