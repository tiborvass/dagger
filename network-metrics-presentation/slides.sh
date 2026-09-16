#!/bin/sh
set -eu

script_dir=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
unit=dagger-network-metrics-slides.service
host=127.0.0.1
port=8080

usage() {
  echo "usage: $0 {start|stop}" >&2
  exit 2
}

is_running() {
  systemctl --user is-active --quiet "$unit"
}

server_pid() {
  systemctl --user show "$unit" --property=MainPID --value
}

start() {
  if is_running; then
    echo "slides already running at http://localhost:$port/ (pid $(server_pid))"
    return
  fi

  systemctl --user reset-failed "$unit" 2>/dev/null || true
  systemd-run --user \
    --unit "$unit" \
    --collect \
    --quiet \
    --working-directory "$script_dir" \
    python3 -m http.server "$port" \
      --bind "$host" \
      --directory "$script_dir/dist"

	attempts=0
	while test "$attempts" -lt 30; do
		if curl --fail --silent "http://$host:$port/" 2>/dev/null |
			grep --quiet "Dagger network accounting"; then
			echo "slides started at http://localhost:$port/ (pid $(server_pid))"
			return
		fi
    if ! is_running; then
      echo "failed to start slides" >&2
      journalctl --user-unit "$unit" --lines=10 --no-pager >&2 || true
      exit 1
    fi
    attempts=$((attempts + 1))
    sleep 0.1
  done

  systemctl --user stop "$unit"
  echo "slides did not become ready" >&2
  exit 1
}

stop() {
  if ! is_running; then
    echo "slides are not running"
    return
  fi

  systemctl --user stop "$unit"
  echo "slides stopped"
}

case "${1:-}" in
  start) start ;;
  stop) stop ;;
  *) usage ;;
esac
