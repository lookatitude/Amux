// Package persist freezes the Amux persistence and restore CONTRACT (ADR-0005):
// the canonical authority per field, checkpoint-generation structure, atomic
// multi-component commit ordering, SQLite precedence for security state, and the
// live|restarted|stopped restore classification. It defines the data shapes and
// the pure classification logic; the actual atomic I/O, SQLite migrations, and
// fsync sequencing are implemented in internal/snapshot and internal/store by T4
// backend (B8), strictly against these types.
package persist

// RestartPolicy governs whether a stopped surface's process is relaunched on
// restore. Manual is the default (spec): restore never relaunches a manual
// surface, it classifies it stopped.
type RestartPolicy string

const (
	// RestartManual is the default; restore classifies the surface stopped.
	RestartManual RestartPolicy = "manual"
	// RestartAutomatic relaunches a new process on restore under an explicit
	// automatic policy, classifying the surface restarted.
	RestartAutomatic RestartPolicy = "automatic"
)

// SurfaceClass is the exact-one-of restore classification every restored
// terminal surface receives (spec success criterion 5). It always carries a
// reason for restarted/stopped so no UI can imply process-memory resurrection.
type SurfaceClass string

const (
	// ClassLive: the surface was reconciled to the SAME still-owned PTY/process
	// identity by an in-daemon restore. A fresh daemon can NEVER produce this.
	ClassLive SurfaceClass = "live"
	// ClassRestarted: a new process was launched under an explicit automatic
	// policy.
	ClassRestarted SurfaceClass = "restarted"
	// ClassStopped: the default — manual policy, missing executable/cwd, or
	// failed validation. Always accompanied by an accurate reason.
	ClassStopped SurfaceClass = "stopped"
)

// RestoreContext describes the daemon performing the restore.
type RestoreContext struct {
	// FreshDaemon is true when a NEW daemon incarnation performs the restore
	// (e.g. after a crash or reboot). A fresh daemon owns no live PTYs, so it can
	// never classify a surface live — the classifier enforces this structurally.
	FreshDaemon bool
}

// SurfaceRestoreInput is the per-surface evidence the classifier consumes.
type SurfaceRestoreInput struct {
	RestartPolicy RestartPolicy
	// ExecutablePresent / CwdPresent report whether the recorded argv[0] and cwd
	// still resolve; both are required to (re)launch.
	ExecutablePresent bool
	CwdPresent        bool
	// SamePTYIdentityOwned is meaningful only when !FreshDaemon: the in-daemon
	// restore reconciled the snapshot to a PTY/process identity it still owns.
	SamePTYIdentityOwned bool
	// ValidationError, if non-empty, forces stopped with this exact reason and
	// takes precedence over every other signal (fail-closed).
	ValidationError string
}

// Classify returns the surface's restore class and reason. The ordering encodes
// ADR-0005 precedence:
//
//  1. A validation error forces stopped (fail closed).
//  2. Live only for an in-daemon restore that still owns the identical PTY
//     identity; a fresh daemon is structurally excluded.
//  3. Restarted only under an explicit automatic policy with a launchable
//     executable and cwd.
//  4. Otherwise stopped, with the specific reason.
func Classify(ctx RestoreContext, in SurfaceRestoreInput) (SurfaceClass, string) {
	if in.ValidationError != "" {
		return ClassStopped, in.ValidationError
	}
	if !ctx.FreshDaemon && in.SamePTYIdentityOwned {
		return ClassLive, "reconciled to still-owned pty identity"
	}
	if in.RestartPolicy == RestartAutomatic {
		if !in.ExecutablePresent {
			return ClassStopped, "automatic restart policy but executable is missing"
		}
		if !in.CwdPresent {
			return ClassStopped, "automatic restart policy but cwd is missing"
		}
		return ClassRestarted, "relaunched under automatic restart policy"
	}
	return ClassStopped, "manual restart policy (default): surface not relaunched"
}
