#!/usr/bin/env bash
set -euo pipefail

binary=$(realpath "${1:?usage: smoke.sh path-to-virgil}")
temp_dir=$(mktemp -d)
server_pid=
cleanup() {
  if [[ -n "$server_pid" ]]; then
    kill -INT "$server_pid" 2>/dev/null || true
    for ((attempt = 0; attempt < 50; attempt++)); do
      if ! kill -0 "$server_pid" 2>/dev/null; then
        break
      fi
      sleep 0.1
    done
    if kill -0 "$server_pid" 2>/dev/null; then
      kill -KILL "$server_pid" 2>/dev/null || true
    fi
    wait "$server_pid" 2>/dev/null || true
  fi
  rm -rf -- "$temp_dir"
}
trap cleanup EXIT

port=$(python3 -c 'import socket; s=socket.socket(); s.bind(("127.0.0.1", 0)); print(s.getsockname()[1]); s.close()')
base_url="http://127.0.0.1:$port"
cat > "$temp_dir/virgil.toml" <<EOF
[server]
listen = "127.0.0.1:$port"
[storage]
path = "$temp_dir/virgil.db"
EOF

"$binary" serve --no-open --config "$temp_dir/virgil.toml" >"$temp_dir/stdout.log" 2>"$temp_dir/stderr.log" &
server_pid=$!
ready=false
for ((attempt = 0; attempt < 100; attempt++)); do
  if ! kill -0 "$server_pid" 2>/dev/null; then
    cat "$temp_dir/stderr.log" >&2
    echo 'Virgil exited before readiness' >&2
    exit 1
  fi
  if curl --silent --show-error --noproxy '*' --max-time 1 --output /dev/null "$base_url/health" 2>/dev/null; then
    ready=true
    break
  fi
  sleep 0.1
done
if [[ "$ready" != true ]]; then
  cat "$temp_dir/stderr.log" >&2
  echo 'Virgil did not answer /health' >&2
  exit 1
fi

dashboard=$(curl --fail --silent --show-error --noproxy '*' --max-time 3 "$base_url/dashboard")
[[ "$dashboard" == *Virgil* ]] || { echo 'Virgil did not serve the dashboard' >&2; exit 1; }
credential=$(cat "$temp_dir/control.token")
run_id="smoke_$(python3 -c 'import uuid; print(uuid.uuid4().hex)')"
response=$(curl --fail --silent --show-error --noproxy '*' --max-time 3 \
  --request POST --header "Authorization: Bearer $credential" \
  --header 'Content-Type: application/json' \
  --data "{\"run_id\":\"$run_id\"}" "$base_url/api/executions")
python3 -c 'import json,sys; d=json.load(sys.stdin); assert d["run_id"] == sys.argv[1] and d["run_token"]' "$run_id" <<< "$response"
echo 'Virgil installed-binary smoke passed.'
