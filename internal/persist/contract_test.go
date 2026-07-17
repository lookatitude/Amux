package persist

import "testing"

// TestClassifyFreshDaemonNeverLive is the load-bearing restore invariant: no
// matter the inputs, a fresh daemon can never classify a surface live (spec
// success criterion 5; ADR-0005).
func TestClassifyFreshDaemonNeverLive(t *testing.T) {
	fresh := RestoreContext{FreshDaemon: true}
	inputs := []SurfaceRestoreInput{
		{RestartPolicy: RestartManual, SamePTYIdentityOwned: true, ExecutablePresent: true, CwdPresent: true},
		{RestartPolicy: RestartAutomatic, SamePTYIdentityOwned: true, ExecutablePresent: true, CwdPresent: true},
	}
	for i, in := range inputs {
		if cls, _ := Classify(fresh, in); cls == ClassLive {
			t.Fatalf("case %d: fresh daemon produced live classification", i)
		}
	}
}

func TestClassifyRules(t *testing.T) {
	cases := []struct {
		name string
		ctx  RestoreContext
		in   SurfaceRestoreInput
		want SurfaceClass
	}{
		{
			"in-daemon reconcile is live",
			RestoreContext{FreshDaemon: false},
			SurfaceRestoreInput{RestartPolicy: RestartManual, SamePTYIdentityOwned: true},
			ClassLive,
		},
		{
			"automatic policy restarts",
			RestoreContext{FreshDaemon: true},
			SurfaceRestoreInput{RestartPolicy: RestartAutomatic, ExecutablePresent: true, CwdPresent: true},
			ClassRestarted,
		},
		{
			"manual policy stays stopped",
			RestoreContext{FreshDaemon: true},
			SurfaceRestoreInput{RestartPolicy: RestartManual, ExecutablePresent: true, CwdPresent: true},
			ClassStopped,
		},
		{
			"automatic but missing exec -> stopped",
			RestoreContext{FreshDaemon: true},
			SurfaceRestoreInput{RestartPolicy: RestartAutomatic, ExecutablePresent: false, CwdPresent: true},
			ClassStopped,
		},
		{
			"validation error forces stopped even with live-eligible identity",
			RestoreContext{FreshDaemon: false},
			SurfaceRestoreInput{RestartPolicy: RestartManual, SamePTYIdentityOwned: true, ValidationError: "checksum mismatch"},
			ClassStopped,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, reason := Classify(tc.ctx, tc.in)
			if got != tc.want {
				t.Fatalf("want %s, got %s (%s)", tc.want, got, reason)
			}
			if got != ClassLive && reason == "" {
				t.Fatal("restarted/stopped must carry a reason")
			}
		})
	}
}
