#!/usr/bin/env python3
import pexpect
import re
import time
import subprocess
import os
from datetime import datetime

END = time.time() + 340 * 60
LOG = "state/ssh-history.log"

def log(msg):
    line = f"[{datetime.utcnow().strftime('%Y-%m-%d %H:%M:%S')} UTC] {msg}"
    print(line, flush=True)
    with open(LOG, "a") as f:
        f.write(line + "\n")

def git_push(link):
    host = link.replace("tcp://", "").split(":")[0]
    port = link.replace("tcp://", "").split(":")[1]
    with open("client.sh", "w") as f:
        f.write("#!/bin/bash\n")
        f.write(f'echo "Connecting to {link} ..."\n')
        f.write(f"ssh -o StrictHostKeyChecking=no -p {port} root@{host}\n")
    os.chmod("client.sh", 0o755)
    subprocess.run(["git", "add", "client.sh", LOG], check=False)
    subprocess.run(["git", "commit", "-m", f"live client.sh: {link}"], check=False)
    token = os.environ.get("GH_TOKEN", "")
    repo = os.environ.get("GITHUB_REPOSITORY", "")
    if token and repo:
        url = f"https://x-access-token:{token}@github.com/{repo}.git"
        subprocess.run(["git", "push", url, "HEAD:main"], check=False)
    log(f"client.sh pushed! {link}")

log("=== Start ===")

while time.time() < END:
    log("Starting tunnel...")
    try:
        child = pexpect.spawn(
            "ssh -p 443 -o StrictHostKeyChecking=no -o UserKnownHostsFile=/dev/null "
            "-o ServerAliveInterval=30 -o ConnectTimeout=30 "
            "-R0:localhost:22 tcp@free.pinggy.io",
            timeout=60,
            encoding="utf-8"
        )
        child.logfile_read = open("tunnel-out.log", "w")

        # handle password (empty)
        idx = child.expect([r"(?i)password:", pexpect.EOF, pexpect.TIMEOUT], timeout=30)
        if idx == 0:
            child.sendline("")
        
        # wait for the tcp link
        link = None
        for _ in range(40):
            time.sleep(1.5)
            try:
                out = open("tunnel-out.log").read()
            except:
                out = ""
            m = re.search(r'tcp://[a-zA-Z0-9.-]+\.[a-zA-Z0-9.-]+:[0-9]+', out)
            if m:
                link = m.group(0)
                break
            if not child.isalive():
                break

        if link:
            log(f"LIVE: {link}")
            git_push(link)
        else:
            log("No link found")
            try:
                print(open("tunnel-out.log").read())
            except:
                pass

        # keep the tunnel alive ~55 min
        child.timeout = 3300
        try:
            child.expect(pexpect.EOF, timeout=3300)
        except pexpect.TIMEOUT:
            log("55min timeout, restarting...")
            child.terminate(force=True)
        except Exception as e:
            log(f"tunnel ended: {e}")

    except Exception as e:
        log(f"error: {e}")

    log("restart in 8s...")
    time.sleep(8)

log("=== End ===")
