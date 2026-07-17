package platform

import (
	"io"
	"reflect"
	"testing"
)

// This file is the executable freeze of the ADR-0006 platform seam. It exists
// because the G-lane round-1 review caught the seam being prose-complete but
// code-incomplete: the ADR named PTY, LocalTransport, and Notifier while
// platform.go never declared them. Two gates prevent a recurrence:
//
//  1. Omission: every frozen interface is referenced at compile time below, so
//     deleting or renaming one breaks the build of this test.
//  2. Shape drift: TestSeamShapesAreFrozen compares each interface's full
//     method set — names and exact signatures — against the frozen table.
//
// Changing this table is changing the frozen T1 contract: it requires an
// ADR-0006 amendment under the spec's confirmation rules, not a casual edit.

// Compile-time: PTYHandle and LocalConn must remain byte streams the daemon's
// event pipeline and protocol codecs can consume directly.
var (
	_ io.Reader = PTYHandle(nil)
	_ io.Writer = PTYHandle(nil)
	_ io.Reader = LocalConn(nil)
	_ io.Writer = LocalConn(nil)
)

func ifaceOf[T any]() reflect.Type { return reflect.TypeOf((*T)(nil)).Elem() }

// frozenSeams is the complete ADR-0006 interface set with exact signatures.
// The capability order follows the ADR's decision list.
var frozenSeams = []struct {
	name    string
	typ     reflect.Type
	methods map[string]string
}{
	{"PTY", ifaceOf[PTY](), map[string]string{
		"Start": "func(platform.PTYSpec) (platform.PTYHandle, error)",
	}},
	{"PTYHandle", ifaceOf[PTYHandle](), map[string]string{
		"Read":     "func([]uint8) (int, error)",
		"Write":    "func([]uint8) (int, error)",
		"Resize":   "func(platform.PTYSize) error",
		"Signal":   "func(os.Signal) error",
		"Wait":     "func() (platform.PTYExit, error)",
		"MasterFD": "func() uintptr",
		"Close":    "func() error",
	}},
	{"FilesystemIdentity", ifaceOf[FilesystemIdentity](), map[string]string{
		"Identify": "func(string) (string, platform.FSIdentity, error)",
	}},
	{"Clock", ifaceOf[Clock](), map[string]string{
		"NowUnixMilli":   "func() int64",
		"MonotonicNanos": "func() int64",
	}},
	{"PeerCredentials", ifaceOf[PeerCredentials](), map[string]string{
		"PeerUID": "func(uintptr) (uint32, error)",
	}},
	{"Containment", ifaceOf[Containment](), map[string]string{
		"Prepare": "func(platform.ContainmentSpec) (platform.ContainmentHandle, error)",
	}},
	{"ContainmentHandle", ifaceOf[ContainmentHandle](), map[string]string{
		"KillTree": "func() error",
		"Close":    "func() error",
	}},
	{"DescriptorLaunch", ifaceOf[DescriptorLaunch](), map[string]string{
		"OpenBound":   "func(int, string) (int, platform.FSIdentity, error)",
		"LaunchBound": "func(int, []string, []string, platform.LaunchSpec) (int, error)",
	}},
	{"ProcessInspector", ifaceOf[ProcessInspector](), map[string]string{
		"ForegroundPID": "func(uintptr) (int, error)",
		"Alive":         "func(int) (bool, error)",
	}},
	{"LocalTransport", ifaceOf[LocalTransport](), map[string]string{
		"Listen": "func(platform.TransportSpec) (platform.LocalListener, error)",
		"Dial":   "func(platform.TransportSpec) (platform.LocalConn, error)",
	}},
	{"LocalListener", ifaceOf[LocalListener](), map[string]string{
		"Accept": "func() (platform.LocalConn, error)",
		"Path":   "func() string",
		"Close":  "func() error",
	}},
	{"LocalConn", ifaceOf[LocalConn](), map[string]string{
		"Read":    "func([]uint8) (int, error)",
		"Write":   "func([]uint8) (int, error)",
		"Control": "func(func(uintptr) error) error",
		"Close":   "func() error",
	}},
	{"Notifier", ifaceOf[Notifier](), map[string]string{
		"Notify": "func(platform.Notification) error",
	}},
}

// TestSeamSetIsComplete pins the frozen table to the nine ADR-0006 capability
// interfaces (plus their handle/connection sub-interfaces), so removing an
// entry from the table is as loud as removing the interface itself.
func TestSeamSetIsComplete(t *testing.T) {
	want := []string{
		"PTY", "PTYHandle",
		"FilesystemIdentity",
		"Clock",
		"PeerCredentials",
		"Containment", "ContainmentHandle",
		"DescriptorLaunch",
		"ProcessInspector",
		"LocalTransport", "LocalListener", "LocalConn",
		"Notifier",
	}
	got := make([]string, 0, len(frozenSeams))
	for _, s := range frozenSeams {
		got = append(got, s.name)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("frozen seam set changed (ADR-0006 amendment required)\n got: %v\nwant: %v", got, want)
	}
}

// TestSeamShapesAreFrozen asserts each frozen interface exposes exactly the
// recorded method set with exactly the recorded signatures. A new, renamed,
// removed, or re-typed method fails here until ADR-0006 and this table are
// amended together.
func TestSeamShapesAreFrozen(t *testing.T) {
	for _, seam := range frozenSeams {
		if seam.typ.Kind() != reflect.Interface {
			t.Errorf("%s: frozen seam must be an interface, got %s", seam.name, seam.typ.Kind())
			continue
		}
		if got, want := seam.typ.NumMethod(), len(seam.methods); got != want {
			t.Errorf("%s: method count drifted: got %d, frozen %d", seam.name, got, want)
		}
		for i := 0; i < seam.typ.NumMethod(); i++ {
			m := seam.typ.Method(i)
			wantSig, ok := seam.methods[m.Name]
			if !ok {
				t.Errorf("%s.%s: method not in the frozen table (ADR-0006 amendment required)", seam.name, m.Name)
				continue
			}
			if gotSig := m.Type.String(); gotSig != wantSig {
				t.Errorf("%s.%s: signature drifted\n got: %s\nwant: %s", seam.name, m.Name, gotSig, wantSig)
			}
		}
	}
}
