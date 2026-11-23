#!/bin/sh
# Runs the gateway integration test against a local DStarGateway (see README.md).
# RUN=<pattern> picks tests; KEEP=1 leaves the gateway running.
set -eu
cd "$(dirname "$0")"
NAME=refconnect-test-gateway

docker build -q -t "$NAME" . >/dev/null
docker rm -f "$NAME" >/dev/null 2>&1 || true
# Keep the DPlus login off the real auth server.
docker run -d --name "$NAME" \
  --add-host auth.dstargateway.org:127.0.0.1 \
  --add-host host.docker.internal:host-gateway \
  -p 20001:20001/udp -p 30001:30001/udp -p 30051:30051/udp \
  "$NAME" >/dev/null

finish() {
  printf '\n==> gateway log\n'
  docker logs "$NAME" 2>&1 | tail -n 60
  [ "${KEEP:-}" = 1 ] || docker rm -f "$NAME" >/dev/null
}
trap finish EXIT

sleep 3
if [ -z "$(docker ps -q -f name="^$NAME\$")" ]; then
  echo "gateway did not start" >&2
  exit 1
fi
cd ../..
REFCONNECT_DPLUS_AUTH=off go test -tags gateway -race -run "${RUN:-Gateway}" -count=1 -v ./internal/router
