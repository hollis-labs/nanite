#!/bin/bash
# mac-nanite.sh: run the Mac (dev) Nanite under launchd and deploy it in one command (CW-20261024-0113).
# bash 3.2 compatible. Never prints secrets; the plist carries none.
#
#   mac-nanite.sh render                 print the plist for this user
#   mac-nanite.sh install [--autostart]  write the plist and load it (autostart: load at every login)
#   mac-nanite.sh uninstall              unload and remove the plist (binaries, data and backups stay)
#   mac-nanite.sh start|stop|restart|status|logs
#   mac-nanite.sh rotate                 rotate the launchd logs (copytruncate, 5 kept)
#   mac-nanite.sh deploy <binary> [--force]   backup, install, restart, verify, roll back on failure
#   mac-nanite.sh rollback               point back at the previous binary and restart
set -euo pipefail

LABEL=com.hollis-labs.nanite.mac
WORKSPACE=${NANITE_MAC_WORKSPACE:-mesh-chat}
PORT=${NANITE_MAC_PORT:-8090}
DATA=$HOME/.local/share/nanite
BINDIR=$DATA/bin
LINK=$HOME/.local/bin/nanite
LOGDIR=$HOME/Library/Logs/nanite
BACKUPS=$DATA/backups
PLIST_AUTO=$HOME/Library/LaunchAgents/$LABEL.plist
PLIST_MANUAL=$DATA/launchd/$LABEL.plist
WORKDIR=${NANITE_MAC_WORKDIR:-$HOME/dev/hollis-labs/apps/nanite}
DOMAIN=gui/$(id -u)
SVC=$DOMAIN/$LABEL
HERE=$(cd "$(dirname "$0")" && pwd)
URL=http://127.0.0.1:$PORT
KEEP_BINARIES=3

# The PATH the agents need to find their CLIs (claude, codex, agy, opencode, go, node, git, sqlite3).
SVC_PATH=${NANITE_MAC_PATH:-$HOME/.local/share/mise/installs/go/1.25.3/bin:$HOME/.local/share/mise/installs/node/22.12.0/bin:$HOME/.local/bin:$HOME/.bun/bin:$HOME/go/bin:$HOME/.opencode/bin:/opt/homebrew/bin:/opt/homebrew/sbin:/usr/local/bin:/usr/bin:/bin:/usr/sbin:/sbin}

die() { echo "mac-nanite: $*" >&2; exit 1; }
loaded() { launchctl print "$SVC" >/dev/null 2>&1; }

render() {
  sed -e "s#@HOME@#$HOME#g" -e "s#@USER@#$(id -un)#g" -e "s#@PATH@#$SVC_PATH#g" \
      -e "s#@WORKDIR@#$WORKDIR#g" -e "s#@WORKSPACE@#$WORKSPACE#g" "$HERE/$LABEL.plist.tmpl"
}

health() { curl -fsS -m 3 "$URL/api/health" 2>/dev/null || true; }

wait_healthy() { # wait_healthy <expected version substring> <seconds>
  local want=$1 n=$2 i out
  for i in $(seq 1 "$n"); do
    out=$(health)
    case "$out" in *'"status":"ok"'*"$want"*) echo "$out"; return 0;; esac
    sleep 1
  done
  return 1
}

active_work() { # count of active agent runtimes in the workspace DB (read-only)
  local db=$DATA/workspaces/$WORKSPACE/main.db
  [ -f "$db" ] || { echo 0; return; }
  sqlite3 -readonly "$db" "select count(*) from agent_runtime where state in ('launching','running')" 2>/dev/null || echo 0
}

rotate() {
  mkdir -p "$LOGDIR"
  local f i
  for f in "$LOGDIR/stdout.log" "$LOGDIR/stderr.log"; do
    [ -f "$f" ] || continue
    [ "$(stat -f %z "$f")" -gt 20971520 ] || continue
    for i in 4 3 2 1; do [ -f "$f.$i" ] && mv "$f.$i" "$f.$((i+1))"; done
    cp "$f" "$f.1" && : > "$f"   # copytruncate: launchd keeps the descriptor open
    echo "rotated $f"
  done
}

cmd=${1:-}; shift || true
case "$cmd" in
  render) render ;;
  install)
    autostart=0; [ "${1:-}" = "--autostart" ] && autostart=1
    [ -x "$LINK" ] || die "$LINK is not installed; run: $0 deploy <binary> first"
    mkdir -p "$LOGDIR" "$DATA/launchd" "$BINDIR"
    if loaded; then die "$LABEL is already loaded; use uninstall first"; fi
    if pgrep -f 'nanite[^ ]* serve' >/dev/null 2>&1; then die "a hand-started nanite serve is running; stop it first (it holds port $PORT)"; fi
    if [ $autostart = 1 ]; then plist=$PLIST_AUTO; rm -f "$PLIST_MANUAL"; else plist=$PLIST_MANUAL; rm -f "$PLIST_AUTO"; fi
    render > "$plist"; chmod 644 "$plist"; plutil -lint "$plist" >/dev/null
    launchctl bootstrap "$DOMAIN" "$plist"
    echo "loaded $LABEL from $plist (autostart=$autostart)"; ;;
  uninstall)
    loaded && launchctl bootout "$SVC" || true
    rm -f "$PLIST_AUTO" "$PLIST_MANUAL"; echo "removed $LABEL (binaries, data and backups untouched)" ;;
  start)
    if loaded; then launchctl kickstart "$SVC"; else
      plist=$PLIST_MANUAL; [ -f "$plist" ] || plist=$PLIST_AUTO; [ -f "$plist" ] || die "not installed; run: $0 install"
      launchctl bootstrap "$DOMAIN" "$plist"; fi ;;
  stop) loaded && launchctl bootout "$SVC" || echo "not loaded" ;;   # bootout sends SIGTERM and unloads; start loads it again
  restart) loaded || die "not loaded"; launchctl kickstart -k "$SVC" ;;
  status)
    if loaded; then launchctl print "$SVC" | grep -E 'state =|pid =|last exit|runs =' ; else echo "not loaded"; fi
    echo "health: $(health)"; echo "binary: $(readlink "$LINK" 2>/dev/null || echo '(not a symlink)')" ;;
  logs) exec tail -n "${1:-100}" -f "$LOGDIR/stdout.log" "$LOGDIR/stderr.log" ;;
  rotate) rotate ;;
  deploy)
    bin=${1:-}; shift || true; force=0; [ "${1:-}" = "--force" ] && force=1
    [ -x "$bin" ] || die "usage: $0 deploy <binary> [--force]"
    ver=$("$bin" version | awk '{print $2}'); [ -n "$ver" ] || die "cannot read the version of $bin"
    n=$(active_work); if [ "$n" != 0 ] && [ $force = 0 ]; then die "$n agent runtime(s) active; wait or pass --force"; fi
    ts=$(date +%Y%m%d-%H%M%S); bk=$BACKUPS/$ts-pre-$ver; mkdir -p "$bk" "$BINDIR" "$(dirname "$LINK")" "$LOGDIR"; chmod 700 "$BACKUPS" "$bk"
    for pair in "auth/auth.db:auth.db" "workspaces/$WORKSPACE/main.db:main.db"; do
      src=$DATA/${pair%%:*}; dst=$bk/${pair##*:}
      [ -f "$src" ] || continue
      sqlite3 "$src" ".backup $dst"; [ "$(sqlite3 "$dst" 'pragma integrity_check')" = ok ] || die "backup of $src failed integrity_check"
      chmod 600 "$dst"
    done
    echo "backups: $bk"
    prev=$(readlink "$LINK" 2>/dev/null || true)
    cp -p "$bin" "$BINDIR/nanite-$ver"; chmod 755 "$BINDIR/nanite-$ver"
    [ -n "$prev" ] && echo "$prev" > "$DATA/PREVIOUS_BINARY"
    ln -sfn "$BINDIR/nanite-$ver" "$LINK"
    rotate
    if loaded; then launchctl kickstart -k "$SVC"; else echo "service not loaded; binary installed, start it with: $0 start"; exit 0; fi
    if out=$(wait_healthy "$ver" 40); then
      echo "healthy: $out"
      ls -1t "$BINDIR"/nanite-* 2>/dev/null | tail -n +$((KEEP_BINARIES+1)) | while read -r old; do [ "$old" = "$prev" ] || rm -f "$old"; done
    else
      echo "NOT healthy after deploy; rolling the binary back" >&2
      if [ -n "$prev" ]; then ln -sfn "$prev" "$LINK"; launchctl kickstart -k "$SVC"; fi
      echo "binary restored to ${prev:-<none>}. If a migration ran, restore the DBs from $bk (stop first, remove -wal/-shm)." >&2
      exit 1
    fi ;;
  rollback)
    [ -f "$DATA/PREVIOUS_BINARY" ] || die "no previous binary recorded"
    prev=$(cat "$DATA/PREVIOUS_BINARY"); [ -x "$prev" ] || die "previous binary $prev is gone"
    ln -sfn "$prev" "$LINK"; loaded && launchctl kickstart -k "$SVC"
    echo "binary -> $prev. If the failed deploy ran a migration, restore the DBs from the newest $BACKUPS/*-pre-* (stop first, remove -wal/-shm)." ;;
  *) sed -n '2,13p' "$0"; exit 2 ;;
esac
