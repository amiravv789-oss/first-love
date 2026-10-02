#!/bin/bash
set -e

HOST="cihvn-128-24-163-99.run.pinggy-free.link"
PORT="40739"

MODE="${1:-ssh}"

if [ "$MODE" = "ssh" ]; then
    exec ssh -o StrictHostKeyChecking=no -o UserKnownHostsFile=/dev/null -o ServerAliveInterval=30 -o ServerAliveCountMax=3 -p "$PORT" root@"$HOST"
fi

if [ "$MODE" = "rdp" ]; then
    ssh -o StrictHostKeyChecking=no -o UserKnownHostsFile=/dev/null -o ServerAliveInterval=30 -o ServerAliveCountMax=3 -N -L 3389:127.0.0.1:3389 -p "$PORT" root@"$HOST" &
    TUNNEL_PID=$!

    trap "kill $TUNNEL_PID 2>/dev/null || true" EXIT
    sleep 2

    if command -v mstsc.exe >/dev/null 2>&1; then
        mstsc.exe /v:127.0.0.1:3389
        wait $TUNNEL_PID || true
        exit 0
    fi

    if command -v xfreerdp >/dev/null 2>&1; then
        xfreerdp /v:127.0.0.1:3389 /u:root
        exit 0
    fi

    if command -v open >/dev/null 2>&1; then
        open "rdp://full%20address=s:127.0.0.1:3389"
        wait $TUNNEL_PID || true
        exit 0
    fi

    echo "No RDP client found."
    exit 1
fi

exit 1
