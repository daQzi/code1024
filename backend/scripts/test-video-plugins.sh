#!/bin/sh
set -eu

SCRIPT_DIR="$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)"
BACKEND_DIR="$(CDPATH= cd -- "$SCRIPT_DIR/.." && pwd)"
ENGINE_DIR="$(CDPATH= cd -- "$BACKEND_DIR/../newapi-engine" && pwd)"
GO_BIN="${GO_BIN:-go}"

if ! command -v "$GO_BIN" >/dev/null 2>&1; then
  printf 'Go is required; set GO_BIN to its executable path.\n' >&2
  exit 1
fi
if [ -z "${PG_BINDIR:-}" ]; then
  INITDB_BIN="$(command -v initdb || true)"
  if [ -z "$INITDB_BIN" ]; then
    printf 'PostgreSQL is required; set PG_BINDIR to its bin directory.\n' >&2
    exit 1
  fi
  PG_BINDIR="$(dirname -- "$INITDB_BIN")"
fi
for program in initdb pg_ctl createdb; do
  if [ ! -x "$PG_BINDIR/$program" ]; then
    printf 'Missing PostgreSQL executable: %s/%s\n' "$PG_BINDIR" "$program" >&2
    exit 1
  fi
done
PG_BINDIR="$(CDPATH= cd -- "$PG_BINDIR" && pwd)"

# Ignore externally supplied database settings. This suite owns its temporary
# cluster, listens only on its private Unix socket and never uses a live DSN.
unset PGHOSTADDR PGSERVICE PGSERVICEFILE PGOPTIONS
TEST_ROOT="$(mktemp -d /tmp/sub2api-video-tests.XXXXXX)"
chmod 700 "$TEST_ROOT"
cleanup() {
  RESULT=$?
  trap - EXIT HUP INT TERM
  if [ "$RESULT" -ne 0 ]; then
    for log in "$TEST_ROOT/initdb.log" "$TEST_ROOT/postgres.log"; do
      if [ -f "$log" ]; then
        printf '\nPostgreSQL diagnostic: %s\n' "$log" >&2
        tail -n 40 "$log" >&2
      fi
    done
  fi
  if [ -f "$TEST_ROOT/pg/postmaster.pid" ]; then
    if ! "$PG_BINDIR/pg_ctl" -D "$TEST_ROOT/pg" -m fast -w stop; then
      printf 'Could not stop test PostgreSQL; retained %s for inspection.\n' "$TEST_ROOT" >&2
      exit 1
    fi
  fi
  rm -r -- "$TEST_ROOT"
  exit "$RESULT"
}
trap cleanup EXIT
trap 'exit 129' HUP
trap 'exit 130' INT
trap 'exit 143' TERM

"$PG_BINDIR/initdb" -D "$TEST_ROOT/pg" -U video_plugin_test -A trust --no-locale -E UTF8 > "$TEST_ROOT/initdb.log" 2>&1
cat >> "$TEST_ROOT/pg/postgresql.conf" <<EOF
listen_addresses = ''
unix_socket_directories = '$TEST_ROOT'
unix_socket_permissions = 0700
port = 55472
fsync = off
EOF
"$PG_BINDIR/pg_ctl" -D "$TEST_ROOT/pg" -l "$TEST_ROOT/postgres.log" -w start
"$PG_BINDIR/createdb" -h "$TEST_ROOT" -p 55472 -U video_plugin_test sub2api_video_test
export SUB2API_GENERATION_TEST_DSN="host=$TEST_ROOT port=55472 user=video_plugin_test dbname=sub2api_video_test sslmode=disable"

cd "$BACKEND_DIR"
printf 'Checking video plugin runtime and task contracts...\n'
(cd "$ENGINE_DIR" && GOWORK=off "$GO_BIN" test -race ./... -count=1)
printf 'Checking host media and HTTP adapters...\n'
"$GO_BIN" test -race ./internal/newapi/mediahost ./internal/newapi/replyfilter ./internal/newapi/officialupload -count=1
printf 'Checking single host assembly and cleanup...\n'
"$GO_BIN" test -race -tags=unit ./internal/newapihost ./internal/service ./cmd/server -run '^(TestStopSupportsPartialAssembly|TestNativeAndEngineWorkers|TestNewAPIEngineDisabled|TestNewAPIModuleWorksWithoutNativePluginManager|TestNativeVideo|TestGenerationEngineHost|TestProvideCleanup)' -count=1
printf 'Checking video admission, task drivers and settlement...\n'
"$GO_BIN" test -race -tags=unit ./internal/service -run '^(TestGenericVideoPlugin|TestGeneration|TestPluginFactoryCatalogAndHostCompatibility|TestNewAPIProviderAudioForwardsAndPinsPrice|TestTaskEgressMultipartBlocksBeforeUpstream)' -count=1
printf 'Checking PostgreSQL migrations, recovery and ledger transactions...\n'
"$GO_BIN" test -race ./internal/repository -run '^TestGeneration' -count=1
printf 'Checking HTTP tasks, logs, downloads and billing...\n'
"$GO_BIN" test -race -tags=unit ./internal/handler -run '^TestGeneration' -count=1
printf 'Video plugin checks passed against the disposable PostgreSQL cluster.\n'
