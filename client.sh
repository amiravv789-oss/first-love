#!/bin/bash
set -e
HOST="remote-pishgaman.runflare.com"
PORT="22"
RDP_PORT="3389"
MODE="${1:-ssh}"

if [ "$MODE" = "ssh" ]; then
    echo "Connecting to Runner via Relay Server SSH ($HOST:$PORT)..."
    exec ssh -o StrictHostKeyChecking=no -o UserKnownHostsFile=/dev/null -o ServerAliveInterval=30 -p "$PORT" "root@$HOST"
fi

if [ "$MODE" = "rdp" ]; then
    echo "Connecting to Runner Desktop via Relay Server RDP ($HOST:$RDP_PORT)..."
    if command -v xfreerdp >/dev/null 2>&1; then
        exec xfreerdp /v:"$HOST:$RDP_PORT" /u:root /p:"&&amiravvali1280@1578&&"
    fi
    if command -v mstsc.exe >/dev/null 2>&1; then
        exec mstsc.exe /v:"$HOST:$RDP_PORT"
    fi
    if command -v open >/dev/null 2>&1; then
        exec open "rdp://full%20address=s:${HOST}:${RDP_PORT}&username=s:root"
    fi
    echo "RDP Address: $HOST:$RDP_PORT (User: root)"
    exit 0
fi
