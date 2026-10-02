#!/bin/bash
set -e

HOST="cooun-20-221-69-180.run.pinggy-free.link"
PORT="45999"

MODE="${1:-ssh}"

if [ "$MODE" = "ssh" ]; then
    exec ssh -o StrictHostKeyChecking=no -o UserKnownHostsFile=/dev/null -p "$PORT" root@"$HOST"
fi

if [ "$MODE" = "rdp" ]; then
    ssh -o StrictHostKeyChecking=no -o UserKnownHostsFile=/dev/null -N -L 3389:127.0.0.1:3389 -p "$PORT" root@"$HOST" &
    TUNNEL_PID=$!

    trap "kill $TUNNEL_PID 2>/dev/null || true" EXIT
    sleep 2

    if command -v mstsc.exe >/dev/null 2>&1; then
        mstsc.exe /v:127.0.0.1:3389
        exit 0
    fi

    if command -v xfreerdp >/dev/null 2>&1; then
        exec xfreerdp /v:127.0.0.1:3389 /u:root
    fi

    if command -v open >/dev/null 2>&1; then
        open "rdp://full%20address=s:127.0.0.1:3389"
        wait
        exit 0
    fi

    echo "No RDP client found."
    exit 1
fi

exit 1
