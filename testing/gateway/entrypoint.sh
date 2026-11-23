#!/bin/sh
# Points the repeater at the Docker host, then starts the gateway.
set -eu
ip=$(getent ahostsv4 host.docker.internal | awk 'NR==1 {print $1}')
[ -n "$ip" ] || { echo "host.docker.internal does not resolve" >&2; exit 1; }
sed "s/@RPT_ADDRESS@/$ip/" /etc/dstargateway.cfg.in > /etc/dstargateway.cfg
exec dstargateway /etc/dstargateway.cfg
