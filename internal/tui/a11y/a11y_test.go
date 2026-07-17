package a11y

import (
	"strings"
	"testing"

	"github.com/amux-run/amux/internal/tui/keys"
)

func TestNoColorForcesMonochrome(t *testing.T) {
	p := Resolve(Env{NoColor: true, Term: "xterm-256color"})
	if p.Color != NoColor {
		t.Fatal("NO_COLOR must force monochrome")
	}
	if !p.RenderOptions().Mono {
		t.Fatal("monochrome profile must set render Mono")
	}
}

func TestDumbTerminalIsMonochrome(t *testing.T) {
	if Resolve(Env{Term: "dumb"}).Color != NoColor {
		t.Fatal("dumb terminal should be monochrome")
	}
	if Resolve(Env{Term: ""}).Color != NoColor {
		t.Fatal("empty TERM should be monochrome")
	}
}

func TestTruecolorDetected(t *testing.T) {
	if Resolve(Env{Term: "xterm-256color"}).Color != FullColor {
		t.Fatal("256color should be full color")
	}
	if Resolve(Env{Term: "xterm", ColorTerm: "truecolor"}).Color != FullColor {
		t.Fatal("COLORTERM=truecolor should be full color")
	}
	if Resolve(Env{Term: "xterm"}).Color != LimitedColor {
		t.Fatal("plain xterm should be limited color")
	}
}

func TestReducedMotionAndAscii(t *testing.T) {
	p := Resolve(Env{Term: "xterm-256color", ReducedMotion: true, ASCIIOnly: true})
	o := p.RenderOptions()
	if !o.ReducedMotion || !o.ASCIIBorders {
		t.Fatalf("reduced motion/ascii not propagated: %+v", o)
	}
}

func TestMinSizeFallback(t *testing.T) {
	p := DefaultProfile()
	if p.Fits(10, 3) {
		t.Fatal("10x3 should not fit the 20x5 floor")
	}
	if !p.Fits(80, 24) {
		t.Fatal("80x24 should fit")
	}
	frame := p.MinSizeFrame(40, 5)
	if !strings.Contains(frame, "too small") || !strings.Contains(frame, "CLI") && !strings.Contains(frame, "amux") {
		t.Fatalf("min-size frame should explain and point to CLI:\n%s", frame)
	}
}

func TestHelpListsBindingsAndCLIAlternative(t *testing.T) {
	km := keys.DefaultKeymap()
	lines := HelpLines(km, keys.Prefix)
	joined := strings.Join(lines, "\n")
	if !strings.Contains(joined, "focus_left") || !strings.Contains(joined, "split_horizontal") {
		t.Fatalf("help missing bindings:\n%s", joined)
	}
	if !strings.Contains(joined, "Non-interactive") {
		t.Fatalf("help must point to the CLI alternative:\n%s", joined)
	}
}
