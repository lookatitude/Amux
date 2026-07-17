#!/usr/bin/env bash
# Behavioral proof of the Runbook C cold backup/restore command pattern
# (docs/release/rollback-and-recovery.md §"snapshot backup & restore"), T3 F3.
#
# Guards the exact defect the round-1 review flagged: the backup must keep
# `share/amux` and `state/amux` on DISTINCT in-archive paths and the restore must
# recreate them under `~/.local/share/amux` AND `~/.local/state/amux` — never
# collapse the state tree under the share tree. Runs against a throwaway temp
# root (never the real ~/.local) so it is deterministic on any host (macOS/Linux).
set -euo pipefail

root="$(mktemp -d)"
trap 'rm -rf "$root"' EXIT
localdir="$root/.local"
share="$localdir/share"; state="$localdir/state"

# --- Arrange: a populated share tree and a populated state tree --------------
mkdir -p "$share/amux/snapshots" "$state/amux"
echo "DATA-SENTINEL"  > "$share/amux/data.txt"
echo "SNAP-SENTINEL"  > "$share/amux/snapshots/s1"
echo "STATE-SENTINEL" > "$state/amux/state.txt"

archive="$root/amux-state.tar.gz"

# --- BACKUP: the documented command (distinct prefixes, rooted at ~/.local) --
tar -czf "$archive" -C "$localdir" share/amux state/amux

members="$(tar -tzf "$archive")"
printf '%s\n' "$members" | grep -qx 'share/amux/data.txt'  || { echo "FAIL: share/amux/data.txt missing from archive"; exit 1; }
printf '%s\n' "$members" | grep -qx 'state/amux/state.txt' || { echo "FAIL: state/amux/state.txt missing from archive"; exit 1; }

# --- RESTORE: remove both trees, then extract at ~/.local --------------------
rm -rf "$share/amux" "$state/amux"
tar -xzf "$archive" -C "$localdir"

# --- Assert: both trees restored to their own XDG location, no collision -----
fail=0
[ "$(cat "$share/amux/data.txt"       2>/dev/null)" = "DATA-SENTINEL"  ] || { echo "FAIL: ~/.local/share/amux/data.txt not restored";       fail=1; }
[ "$(cat "$share/amux/snapshots/s1"   2>/dev/null)" = "SNAP-SENTINEL"  ] || { echo "FAIL: share subtree not fully restored";               fail=1; }
[ "$(cat "$state/amux/state.txt"      2>/dev/null)" = "STATE-SENTINEL" ] || { echo "FAIL: ~/.local/state/amux/state.txt not restored";      fail=1; }
[ -d "$state/amux" ]              || { echo "FAIL: ~/.local/state/amux was not recreated";           fail=1; }
# The historical bug: state must NOT be nested under the share tree.
[ ! -e "$share/amux/state.txt" ] || { echo "FAIL: state file leaked under ~/.local/share/amux";      fail=1; }
[ ! -e "$share/state" ]          || { echo "FAIL: a state/ tree was nested under ~/.local/share";     fail=1; }

if [ "$fail" -eq 0 ]; then
  echo "backup-restore-selftest: PASS (share & state restored to distinct XDG trees)"
else
  echo "backup-restore-selftest: FAILED"; exit 1
fi
