#!/bin/sh
# Builds and starts a local DStarGateway repeater gateway, runs RefConnect's
# gateway integration test against it, prints the gateway's log, and removes
# the container. KEEP=1 leaves the gateway running afterwards, e.g. to link
# the app to it by hand (Repeater mode: N0CALL, module B, host 127.0.0.1).
set -eu
cd "$(dirname "$0")"
NAME=refconnect-test-gateway

docker build -q -t "$NAME" . >/dev/null
docker rm -f "$NAME" >/dev/null 2>&1 || true
# auth.dstargateway.org → 127.0.0.1 keeps the DPlus login, which starts
# whenever DPlus is enabled, off the real trust server.
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
go test -tags gateway -race -run "${RUN:-Gateway}" -count=1 -v ./internal/router
