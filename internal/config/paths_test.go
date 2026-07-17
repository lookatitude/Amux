package config

import (
	"errors"
	"strings"
	"testing"
)

// envOf builds a hermetic getenv from a map; absent keys read as "".
func envOf(m map[string]string) func(string) string {
	return func(k string) string { return m[k] }
}

func TestResolve(t *testing.T) {
	tests := []struct {
		name    string
		env     map[string]string
		want    Paths
		wantErr error  // matched with errors.Is when non-nil
		errSub  string // substring the error must contain when wantErr is nil
	}{
		{
			name: "explicit XDG dirs win and HOME is not required",
			env: map[string]string{
				"XDG_CONFIG_HOME": "/x/cfg",
				"XDG_DATA_HOME":   "/x/data",
				"XDG_STATE_HOME":  "/x/state",
				"XDG_CACHE_HOME":  "/x/cache",
				"XDG_RUNTIME_DIR": "/run/user/1000",
			},
			want: Paths{
				ConfigDir:  "/x/cfg/amux",
				DataDir:    "/x/data/amux",
				StateDir:   "/x/state/amux",
				CacheDir:   "/x/cache/amux",
				RuntimeDir: "/run/user/1000/amux",
			},
		},
		{
			name: "unset XDG dirs fall back to HOME per the basedir spec",
			env: map[string]string{
				"HOME":            "/home/u",
				"XDG_RUNTIME_DIR": "/run/user/1000",
			},
			want: Paths{
				ConfigDir:  "/home/u/.config/amux",
				DataDir:    "/home/u/.local/share/amux",
				StateDir:   "/home/u/.local/state/amux",
				CacheDir:   "/home/u/.cache/amux",
				RuntimeDir: "/run/user/1000/amux",
			},
		},
		{
			name: "relative XDG values are ignored (treated as unset)",
			env: map[string]string{
				"HOME":            "/home/u",
				"XDG_CONFIG_HOME": "rel/cfg",
				"XDG_DATA_HOME":   "./data",
				"XDG_RUNTIME_DIR": "/run/user/1000",
			},
			want: Paths{
				ConfigDir:  "/home/u/.config/amux",
				DataDir:    "/home/u/.local/share/amux",
				StateDir:   "/home/u/.local/state/amux",
				CacheDir:   "/home/u/.cache/amux",
				RuntimeDir: "/run/user/1000/amux",
			},
		},
		{
			name:    "missing XDG_RUNTIME_DIR fails closed with the typed error",
			env:     map[string]string{"HOME": "/home/u"},
			wantErr: ErrRuntimeDirUnset,
		},
		{
			name: "relative XDG_RUNTIME_DIR is treated as unset",
			env: map[string]string{
				"HOME":            "/home/u",
				"XDG_RUNTIME_DIR": "run/user/1000",
			},
			wantErr: ErrRuntimeDirUnset,
		},
		{
			name: "no HOME and no XDG fallback source is an error",
			env: map[string]string{
				"XDG_RUNTIME_DIR": "/run/user/1000",
			},
			errSub: "HOME",
		},
		{
			name: "relative HOME is treated as unset",
			env: map[string]string{
				"HOME":            "home/u",
				"XDG_RUNTIME_DIR": "/run/user/1000",
			},
			errSub: "HOME",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := Resolve(envOf(tc.env))
			if tc.wantErr != nil {
				if !errors.Is(err, tc.wantErr) {
					t.Fatalf("Resolve err = %v, want errors.Is(%v)", err, tc.wantErr)
				}
				return
			}
			if tc.errSub != "" {
				if err == nil || !strings.Contains(err.Error(), tc.errSub) {
					t.Fatalf("Resolve err = %v, want error containing %q", err, tc.errSub)
				}
				return
			}
			if err != nil {
				t.Fatalf("Resolve: %v", err)
			}
			if got != tc.want {
				t.Fatalf("Resolve\n got: %+v\nwant: %+v", got, tc.want)
			}
		})
	}
}

func TestPathsHelpers(t *testing.T) {
	p, err := Resolve(envOf(map[string]string{
		"XDG_CONFIG_HOME": "/x/cfg",
		"HOME":            "/home/u",
		"XDG_RUNTIME_DIR": "/run/user/1000",
	}))
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if got, want := p.SocketPath(), "/run/user/1000/amux/amuxd.sock"; got != want {
		t.Fatalf("SocketPath = %q, want %q", got, want)
	}
	if got, want := p.ConfigFile(), "/x/cfg/amux/config.jsonc"; got != want {
		t.Fatalf("ConfigFile = %q, want %q", got, want)
	}
}
