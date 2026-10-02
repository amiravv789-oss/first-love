#!/usr/bin/env python3

import pexpect
import re
import time
import subprocess
import os
from datetime import datetime


END = time.time() + (340 * 60)

LOG = "state/ssh-history.log"


def log(message):

    line = (
        f"[{datetime.utcnow().strftime('%Y-%m-%d %H:%M:%S')} UTC] "
        f"{message}"
    )

    print(line, flush=True)

    with open(LOG, "a") as f:
        f.write(line + "\n")


def git_push(link):

    link = link.replace("tcp://", "")

    host, port = link.rsplit(":", 1)

    # ==================================================
    # CREATE CLIENT.SH
    # ==================================================

    with open("client.sh", "w") as f:

        f.write("#!/bin/bash\n")
        f.write("set -e\n\n")

        f.write(f'HOST="{host}"\n')
        f.write(f'PORT="{port}"\n\n')

        f.write('MODE="${1:-ssh}"\n\n')

        # ==================================================
        # SSH
        # ==================================================

        f.write('if [ "$MODE" = "ssh" ]; then\n')

        f.write(
            '    exec ssh '
            '-o StrictHostKeyChecking=no '
            '-o UserKnownHostsFile=/dev/null '
            '-o ServerAliveInterval=30 '
            '-o ServerAliveCountMax=3 '
            '-p "$PORT" '
            'root@"$HOST"\n'
        )

        f.write("fi\n\n")

        # ==================================================
        # RDP
        # ==================================================

        f.write('if [ "$MODE" = "rdp" ]; then\n')

        f.write(
            '    ssh '
            '-o StrictHostKeyChecking=no '
            '-o UserKnownHostsFile=/dev/null '
            '-o ServerAliveInterval=30 '
            '-o ServerAliveCountMax=3 '
            '-N '
            '-L 3389:127.0.0.1:3389 '
            '-p "$PORT" '
            'root@"$HOST" &\n'
        )

        f.write("    TUNNEL_PID=$!\n\n")

        f.write(
            '    trap "kill $TUNNEL_PID 2>/dev/null || true" EXIT\n'
        )

        f.write("    sleep 2\n\n")

        # Windows
        f.write(
            '    if command -v mstsc.exe >/dev/null 2>&1; then\n'
        )

        f.write(
            '        mstsc.exe /v:127.0.0.1:3389\n'
        )

        f.write(
            '        wait $TUNNEL_PID || true\n'
        )

        f.write("        exit 0\n")
        f.write("    fi\n\n")

        # Linux
        f.write(
            '    if command -v xfreerdp >/dev/null 2>&1; then\n'
        )

        f.write(
            '        xfreerdp '
            '/v:127.0.0.1:3389 '
            '/u:root\n'
        )

        f.write("        exit 0\n")
        f.write("    fi\n\n")

        # macOS
        f.write(
            '    if command -v open >/dev/null 2>&1; then\n'
        )

        f.write(
            '        open '
            '"rdp://full%20address=s:127.0.0.1:3389"\n'
        )

        f.write(
            '        wait $TUNNEL_PID || true\n'
        )

        f.write("        exit 0\n")
        f.write("    fi\n\n")

        f.write(
            '    echo "No RDP client found."\n'
        )

        f.write("    exit 1\n")

        f.write("fi\n\n")

        f.write("exit 1\n")

    os.chmod(
        "client.sh",
        0o755
    )

    # ==================================================
    # COMMIT
    # ==================================================

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
            f"live tunnel: {link}"
        ],
        check=False
    )

    # ==================================================
    # PUSH
    # ==================================================

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

        subprocess.run(
            [
                "git",
                "push",
                url,
                "HEAD:main"
            ],
            check=False
        )

    log(
        f"client.sh pushed: {link}"
    )


# ==================================================
# MAIN LOOP
# ==================================================

log(
    "=== Tunnel service started ==="
)


while time.time() < END:

    log(
        "Starting Pinggy tunnel..."
    )

    try:

        child = pexpect.spawn(
            "ssh "
            "-p 443 "
            "-o StrictHostKeyChecking=no "
            "-o UserKnownHostsFile=/dev/null "
            "-o ServerAliveInterval=30 "
            "-o ServerAliveCountMax=3 "
            "-o ConnectTimeout=30 "
            "-R0:localhost:22 "
            "tcp@free.pinggy.io",
            timeout=60,
            encoding="utf-8"
        )

        logfile = open(
            "tunnel-out.log",
            "w"
        )

        child.logfile_read = logfile

        # ==================================================
        # PINGGY PROMPTS
        # ==================================================

        try:

            idx = child.expect(
                [
                    r"(?i)password:",
                    r"(?i)yes/no",
                    pexpect.EOF,
                    pexpect.TIMEOUT
                ],
                timeout=30
            )

            if idx == 0:
                child.sendline("")

            elif idx == 1:
                child.sendline("yes")

        except Exception as e:

            log(
                f"Initial connection handling: {e}"
            )

        # ==================================================
        # FIND PUBLIC URL
        # ==================================================

        link = None

        for _ in range(40):

            time.sleep(1.5)

            try:

                with open(
                    "tunnel-out.log",
                    "r"
                ) as f:

                    output = f.read()

            except Exception:

                output = ""

            match = re.search(
                r'tcp://[a-zA-Z0-9.-]+:[0-9]+',
                output
            )

            if match:

                link = match.group(0)

                break

            if not child.isalive():

                break

        # ==================================================
        # TUNNEL FOUND
        # ==================================================

        if link:

            log(
                f"LIVE: {link}"
            )

            git_push(link)

        else:

            log(
                "Pinggy tunnel URL was not found."
            )

            try:

                with open(
                    "tunnel-out.log",
                    "r"
                ) as f:

                    print(
                        f.read()
                    )

            except Exception:
                pass

        # ==================================================
        # KEEP TUNNEL ALIVE
        # ==================================================

        try:

            child.expect(
                pexpect.EOF,
                timeout=3300
            )

        except pexpect.TIMEOUT:

            log(
                "55 minute timeout. Restarting..."
            )

            child.terminate(
                force=True
            )

        except Exception as e:

            log(
                f"Tunnel ended: {e}"
            )

        try:
            logfile.close()
        except Exception:
            pass

    except Exception as e:

        log(
            f"Tunnel error: {e}"
        )

    log(
        "Restarting tunnel in 8 seconds..."
    )

    time.sleep(8)


log(
    "=== Tunnel service ended ==="
)

