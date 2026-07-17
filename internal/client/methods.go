// methods.go carries the typed convenience wrappers for the minor-1 read-only
// projection methods (T4 handoff to T5). They are thin: each is exactly one
// Call/Stream over the same shared connection every other flow uses — no
// TUI-only path, no client-side authority. Older daemons (minor 0) answer the
// unary methods with a typed error the caller surfaces; nothing here retries
// or fakes a projection.
package client

import (
	"context"

	"github.com/amux-run/amux/internal/rpcapi"
)

// SurfaceCells fetches the derived cell-grid projection of one surface
// (rpcapi.MethodSurfaceCells). Pass IfChangedSince to poll cheaply: the
// daemon answers Unchanged=true with no grid when nothing new was committed.
func (c *Client) SurfaceCells(ctx context.Context, p rpcapi.SurfaceCellsParams) (rpcapi.SurfaceCellsResult, error) {
	var out rpcapi.SurfaceCellsResult
	err := c.Call(ctx, rpcapi.MethodSurfaceCells, p, &out)
	return out, err
}

// HookInspect fetches the read-only trust-detail projection for a project
// root (rpcapi.MethodHookInspect). It never changes trust state.
func (c *Client) HookInspect(ctx context.Context, p rpcapi.HookInspectParams) (rpcapi.HookInspectResult, error) {
	var out rpcapi.HookInspectResult
	err := c.Call(ctx, rpcapi.MethodHookInspect, p, &out)
	return out, err
}

// PaneContext fetches the daemon-owned pane context (rpcapi.MethodPaneContext):
// cwd, bounded git facts, foreground process, recorded exit. Zero fields mean
// the daemon could not determine them (fail closed, never fabricated).
func (c *Client) PaneContext(ctx context.Context, p rpcapi.PaneContextParams) (rpcapi.PaneContextResult, error) {
	var out rpcapi.PaneContextResult
	err := c.Call(ctx, rpcapi.MethodPaneContext, p, &out)
	return out, err
}

// WorkspaceTree fetches the authoritative split-tree projection of one
// workspace (rpcapi.MethodWorkspaceTree).
func (c *Client) WorkspaceTree(ctx context.Context, p rpcapi.WorkspaceTreeParams) (rpcapi.WorkspaceTreeResult, error) {
	var out rpcapi.WorkspaceTreeResult
	err := c.Call(ctx, rpcapi.MethodWorkspaceTree, p, &out)
	return out, err
}

// Attach opens the attach stream (rpcapi.MethodAttach). Set p.Cells to receive
// the exact snapshot-at-N cell grid inside the attach_snapshot payload
// (rpcapi.AttachSnapshotCells under the "cells" key).
func (c *Client) Attach(ctx context.Context, p rpcapi.AttachParams) (*Stream, error) {
	return c.Stream(ctx, rpcapi.MethodAttach, p)
}
