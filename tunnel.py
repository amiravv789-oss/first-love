#!/usr/bin/env python3

import os
import pexpect
import re
import subprocess
import time
from datetime import datetime


# ==================================================
# CONFIGURATION
# ==================================================


LOG = "state/ssh-history.log"

RUNFLARE_HOST = "remote-respina-free.runflare.com"
RUNFLARE_PORT = "31212"
RUNFLARE_USER = "tunnel"

RDP_HOST = "remote-respina-free.runflare.com"
RDP_PORT = "31994"

SSH_PASSWORD = os.environ.get(
    "SSH_PASSWORD",
    ""
)


# ==================================================
# LOG
# ==================================================

def log(message):

    line = (
        f"[{datetime.utcnow().strftime('%Y-%m-%d %H:%M:%S')} UTC] "
        f"{message}"
    )

    print(
        line,
        flush=True
    )

    with open(
        LOG,
        "a"
    ) as file:

        file.write(
            line + "\n"
        )


# ==================================================
# CREATE CLIENT.SH
# ==================================================

def create_client(host, port):

    with open(
        "client.sh",
        "w"
    ) as file:

        file.write(
            "#!/bin/bash\n"
        )

        file.write(
            "set -e\n\n"
        )

        file.write(
            f'HOST="{host}"\n'
        )

        file.write(
            f'PORT="{port}"\n'
        )

        file.write(
            f'RDP_HOST="{RDP_HOST}"\n'
        )

        file.write(
            f'RDP_PORT="{RDP_PORT}"\n\n'
        )

        file.write(
            'MODE="${1:-ssh}"\n\n'
        )

        # ------------------------------------------
        # SSH
        # ------------------------------------------

        file.write(
            'if [ "$MODE" = "ssh" ]; then\n'
        )

        file.write(
            '    exec ssh '
            '-o StrictHostKeyChecking=no '
            '-o UserKnownHostsFile=/dev/null '
            '-o ServerAliveInterval=30 '
            '-o ServerAliveCountMax=3 '
            '-p "$PORT" '
            'root@"$HOST"\n'
        )

        file.write(
            "fi\n\n"
        )

        # ------------------------------------------
        # RDP
        # ------------------------------------------

        file.write(
            'if [ "$MODE" = "rdp" ]; then\n'
        )

        file.write(
            '    if command -v xfreerdp >/dev/null 2>&1; then\n'
        )

        file.write(
            '        xfreerdp '
            '/v:"$RDP_HOST:$RDP_PORT" '
            '/u:root\n'
        )

        file.write(
            '        exit 0\n'
        )

        file.write(
            '    fi\n\n'
        )

        # Windows
        file.write(
            '    if command -v mstsc.exe >/dev/null 2>&1; then\n'
        )

        file.write(
            '        mstsc.exe /v:127.0.0.1:3389\n'
        )

        file.write(
            '        wait $TUNNEL_PID || true\n'
        )

        file.write(
            '        exit 0\n'
        )

        file.write(
            '    fi\n\n'
        )

        # macOS
        file.write(
            '    if command -v open >/dev/null 2>&1; then\n'
        )

        file.write(
            '        open '
            '"rdp://full%20address=s:127.0.0.1:3389"\n'
        )

        file.write(
            '        wait $TUNNEL_PID || true\n'
        )

        file.write(
            '        exit 0\n'
        )

        file.write(
            '    fi\n\n'
        )

        file.write(
            '    echo "No RDP client found."\n'
        )

        file.write(
            '    exit 1\n'
        )

        file.write(
            "fi\n\n"
        )

        file.write(
            "exit 1\n"
        )

    os.chmod(
        "client.sh",
        0o755
    )


# ==================================================
# GIT PUSH
# ==================================================

def git_push(host, port):

    create_client(
        host,
        port
    )

    subprocess.run(
        [
            "git",
            "add",
            "client.sh",
            LOG
        ],
        check=False
    )

    subprocess.run(
        [
            "git",
            "commit",
            "-m",
            f"live Runflare tunnel: {host}:{port}"
        ],
        check=False
    )

    token = os.environ.get(
        "GH_TOKEN",
        ""
    )

    repo = os.environ.get(
        "GITHUB_REPOSITORY",
        ""
    )

    if token and repo:

        url = (
            f"https://x-access-token:{token}"
            f"@github.com/{repo}.git"
        )

        result = subprocess.run(
            [
                "git",
                "push",
                url,
                "HEAD:main"
            ],
            capture_output=True,
            text=True
        )

        if result.returncode != 0:

            log(
                "Git push failed: "
                + result.stderr[-1000:]
            )

        else:

            log(
                "client.sh pushed successfully."
            )

    log(
        f"client.sh endpoint: {host}:{port}"
    )


# ==================================================
# PARSE RUNFLARE OUTPUT
# ==================================================

def find_endpoint(output):

    patterns = [

        # tcp://host:port
        r'tcp://([A-Za-z0-9.-]+):([0-9]+)',

        # tcp host:port
        r'tcp[^\n]*?([A-Za-z0-9.-]+):([0-9]+)',

        # Forwarding / Tunnel style
        r'(?:forwarding|tunnel|remote)[^\n]*?'
        r'([A-Za-z0-9.-]+):([0-9]+)'

    ]

    for pattern in patterns:

        match = re.search(
            pattern,
            output,
            re.IGNORECASE
        )

        if match:

            host = match.group(1)
            port = match.group(2)

            if (
                host
                and port
                and host != RUNFLARE_HOST
            ):

                return host, port

    return None


# ==================================================
# RUN RUNFLARE
# ==================================================

def run_tunnel():

    if not SSH_PASSWORD:

        log(
            "ERROR: SSH_PASSWORD secret is empty."
        )

        return False

    log(
        "Starting Runflare reverse SSH tunnel..."
    )
    
    command = (
        "ssh "
        "-p 31212 "
        "-o StrictHostKeyChecking=no "
        "-o UserKnownHostsFile=/dev/null "
        "-o ServerAliveInterval=30 "
        "-o ServerAliveCountMax=3 "
        "-o ConnectTimeout=30 "
        "-o ExitOnForwardFailure=yes "
        "-N "
        "-R 2222:localhost:22 "
        "-R 13389:localhost:3389 "
        "tunnel@remote-respina-free.runflare.com"
    )

    child = None
    logfile = None

    try:

        child = pexpect.spawn(
            command,
            timeout=60,
            encoding="utf-8"
        )

        logfile = open(
            "runflare-out.log",
            "w"
        )

        child.logfile_read = logfile

        endpoint = None

        # ------------------------------------------
        # Handle initial SSH interaction
        # ------------------------------------------

        for _ in range(30):

            try:

                index = child.expect(
                    [
                        r'(?i)password:',
                        r'(?i)yes/no',
                        r'(?i)fingerprint',
                        r'(?i)tcp://[A-Za-z0-9.-]+:[0-9]+',
                        pexpect.EOF,
                        pexpect.TIMEOUT
                    ],
                    timeout=5
                )

            except Exception:

                break

            if index == 0:

                child.sendline(
                    SSH_PASSWORD
                )

            elif index == 1:

                child.sendline(
                    "yes"
                )

            elif index == 2:

                child.sendline(
                    "yes"
                )

            elif index == 3:

                try:

                    output = ""

                    with open(
                        "runflare-out.log",
                        "r"
                    ) as file:

                        output = file.read()

                    endpoint = find_endpoint(
                        output
                    )

                except Exception:
                    pass

                if endpoint:
                    break

            elif index == 4:

                break

            else:

                try:

                    with open(
                        "runflare-out.log",
                        "r"
                    ) as file:

                        output = file.read()

                    endpoint = find_endpoint(
                        output
                    )

                except Exception:
                    pass

                if endpoint:
                    break

        # ------------------------------------------
        # Search output again
        # ------------------------------------------

        if not endpoint:

            for _ in range(20):

                time.sleep(1)

                try:

                    with open(
                        "runflare-out.log",
                        "r"
                    ) as file:

                        output = file.read()

                    endpoint = find_endpoint(
                        output
                    )

                except Exception:

                    output = ""

                if endpoint:
                    break

                if not child.isalive():
                    break

        # ------------------------------------------
        # Endpoint found
        # ------------------------------------------

        if endpoint:

            host, port = endpoint

            log(
                f"RUNFLARE LIVE: {host}:{port}"
            )

            git_push(
                host,
                port
            )

        else:

            log(
                "Runflare endpoint was not found."
            )

            try:

                with open(
                    "runflare-out.log",
                    "r"
                ) as file:

                    output = file.read()

                print(
                    output,
                    flush=True
                )

            except Exception:
                pass

        # ------------------------------------------
        # Keep tunnel alive
        # ------------------------------------------

        while child.isalive():

            try:

                child.expect(
                    pexpect.EOF,
                    timeout=60
                )

            except pexpect.TIMEOUT:

                continue

            except Exception as error:

                log(
                    f"Tunnel monitor error: {error}"
                )

                break

        log(
            "Runflare tunnel disconnected."
        )

        return False

    finally:

        try:

            if child and child.isalive():
                child.terminate(
                    force=True
                )

        except Exception:
            pass

        try:

            if logfile:
                logfile.close()

        except Exception:
            pass


# ==================================================
# MAIN LOOP
# ==================================================

log(
    "=== Runflare tunnel service started ==="
)
while True:

    success = run_tunnel()

    log(
        "Runflare tunnel disconnected."
    )

    log(
        "Restarting Runflare tunnel..."
    )

    time.sleep(1)


log(
    "=== Runflare tunnel service ended ==="
)

