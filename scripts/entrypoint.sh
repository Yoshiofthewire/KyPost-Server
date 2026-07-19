#!/bin/sh
set -eu

mkdir -p /llama_lab/config /llama_lab/logs /llama_lab/state

# Runs synchronously (as root, before the chown below) so admin.env exists
# before any service starts — a hard guarantee that supervisord's
# priority-based program ordering could only approximate.
/bin/sh /opt/llama-lab/scripts/bootstrap.sh

chown -R llamalab:llamalab /llama_lab

exec supervisord -c /etc/supervisord.conf