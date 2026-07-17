package snapshot

import (
	"testing"

	"github.com/amux-run/amux/internal/persist"
)

// TestClassifySurfacesFreshDaemonNeverLive pins the ADR-0005 structural
// exclusion at this layer: even when the probe (wrongly or maliciously) claims
// the identical PTY identity is still owned, a FreshDaemon restore context can
// NEVER yield ClassLive. persist.Classify already guarantees it; this test
// guards the wiring against regression.
func TestClassifySurfacesFreshDaemonNeverLive(t *testing.T) {
	doc := testGraphDoc()
	got := ClassifySurfaces(
		persist.RestoreContext{FreshDaemon: true},
		doc,
		func(SurfaceRuntime) persist.SurfaceRestoreInput {
			return persist.SurfaceRestoreInput{
				RestartPolicy:        persist.RestartManual,
				ExecutablePresent:    true,
				CwdPresent:           true,
				SamePTYIdentityOwned: true, // the lie a fresh daemon must ignore
			}
		},
	)
	if len(got) != len(doc.Surfaces) {
		t.Fatalf("got %d classifications, want %d", len(got), len(doc.Surfaces))
	}
	for _, c := range got {
		if c.Class == persist.ClassLive {
			t.Fatalf("fresh daemon classified surface %s live (reason %q)", c.Surface, c.Reason)
		}
		if c.Reason == "" {
			t.Errorf("surface %s classification lacks a reason", c.Surface)
		}
	}
}

// TestClassifySurfacesDelegatesToPersist: each precedence rule of
// persist.Classify surfaces through the helper with the surface ID attached.
func TestClassifySurfacesDelegatesToPersist(t *testing.T) {
	doc := &GraphDoc{
		SchemaVersion: GraphSchemaVersion,
		Surfaces: []SurfaceRuntime{
			{Surface: "s-validation-error"},
			{Surface: "s-live"},
			{Surface: "s-restarted", RestartPolicy: persist.RestartAutomatic},
			{Surface: "s-stopped", RestartPolicy: persist.RestartManual},
		},
	}
	inputs := map[string]persist.SurfaceRestoreInput{
		"s-validation-error": {ValidationError: "cwd escaped project root", SamePTYIdentityOwned: true},
		"s-live":             {SamePTYIdentityOwned: true},
		"s-restarted":        {RestartPolicy: persist.RestartAutomatic, ExecutablePresent: true, CwdPresent: true},
		"s-stopped":          {RestartPolicy: persist.RestartManual},
	}
	got := ClassifySurfaces(
		persist.RestoreContext{FreshDaemon: false},
		doc,
		func(sr SurfaceRuntime) persist.SurfaceRestoreInput { return inputs[sr.Surface] },
	)
	want := map[string]persist.SurfaceClass{
		"s-validation-error": persist.ClassStopped,
		"s-live":             persist.ClassLive,
		"s-restarted":        persist.ClassRestarted,
		"s-stopped":          persist.ClassStopped,
	}
	if len(got) != len(want) {
		t.Fatalf("got %d classifications, want %d", len(got), len(want))
	}
	for _, c := range got {
		if c.Class != want[c.Surface] {
			t.Errorf("surface %s classified %s (%s), want %s", c.Surface, c.Class, c.Reason, want[c.Surface])
		}
	}
	// The fail-closed rule wins over the live claim.
	if got[0].Reason != "cwd escaped project root" {
		t.Errorf("validation error reason not preserved: %q", got[0].Reason)
	}
}

// TestClassifySurfacesNilProbeFailsClosed: without runtime evidence the helper
// supplies zero-valued input, so nothing can classify live or restarted.
func TestClassifySurfacesNilProbeFailsClosed(t *testing.T) {
	doc := &GraphDoc{Surfaces: []SurfaceRuntime{{Surface: "s1", RestartPolicy: persist.RestartAutomatic}}}
	got := ClassifySurfaces(persist.RestoreContext{FreshDaemon: true}, doc, nil)
	if len(got) != 1 || got[0].Class != persist.ClassStopped {
		t.Fatalf("nil probe classification = %+v, want stopped", got)
	}
}
