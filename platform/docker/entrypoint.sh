#!/bin/sh
set -eu

if [ "$(id -u)" = "0" ]; then
  ownership_marker=/data/.lazu-owned-v1
  if [ ! -e "$ownership_marker" ]; then
    # Images before the non-root runtime wrote existing named volumes as root.
    # Repair them once, then keep normal API and worker processes unprivileged.
    chown -R lazu:lazu /data
    touch "$ownership_marker"
    chown lazu:lazu "$ownership_marker"
  fi
fi

run_as_lazu() {
  if [ "$(id -u)" = "0" ]; then
    su-exec lazu:lazu "$@"
  else
    "$@"
  fi
}

if [ "${LAZU_RUN_MIGRATIONS_ON_START:-false}" = "true" ]; then
  run_as_lazu /migrate
  if [ "${LAZU_BOOTSTRAP_LEDGER_ON_START:-false}" = "true" ]; then
    # Opens financial continuity on a fresh database and returns the existing
    # state on a running one. It refuses, rather than guesses, when history
    # needs recovery; see "ledger-recover" in docs/DEPLOYMENT.md.
    ledger_source="${LAZU_MIGRATION_SOURCE_URL:-${DATABASE_URL:-}}"
    run_as_lazu env LAZU_MIGRATION_SOURCE_URL="$ledger_source" /migrate -command ledger-bootstrap
  fi
  # A migration gate may keep this container alive briefly to expose /readyz.
  # The follow-on API process must only validate the schema and must never run
  # API-owned background loops.
  export LAZU_RUN_MIGRATIONS_ON_START=false
  export LAZU_MIGRATION_MODE=validate
  export PROCESS_ROLE=api
  export RELAY_EVENT_CONSUMER_ENABLED=false
fi

if [ "$#" -eq 0 ] || [ "${1#-}" != "$1" ]; then
  set -- /lazu-api "$@"
fi

if [ "$(id -u)" = "0" ]; then
  exec su-exec lazu:lazu "$@"
fi
exec "$@"
