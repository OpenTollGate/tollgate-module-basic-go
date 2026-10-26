import os, pty, sys, select, time
#
# router-ssh.py — pty-wrapped ssh for bench routers whose root login is
# password-empty (the desk-test convention). Isolated known-hosts file,
# non-interactive: usage: router-ssh.py <password|-> <host> <command...>
# Companion: router-scp.py (scp -O, same auth).
# usage: sshask.py <password|-> <host> <command...>
password = None if sys.argv[1] == "-" else sys.argv[1]
host, cmd = sys.argv[2], sys.argv[3:]
pid, fd = pty.fork()
if pid == 0:
    os.execvp("ssh", ["ssh", "-o", "StrictHostKeyChecking=accept-new",
                      "-o", "UserKnownHostsFile=/tmp/opencode/mt3000-knownhosts",
                      "-o", "ConnectTimeout=6", f"root@{host}"] + cmd)
out = b""
sent = False
deadline = time.time() + 100
while time.time() < deadline:
    r, _, _ = select.select([fd], [], [], 1.0)
    if r:
        try:
            chunk = os.read(fd, 4096)
        except OSError:
            break
        if not chunk:
            break
        out += chunk
        if not sent and (b"password" in out.lower() or b"password" in chunk.lower()):
            if password is None:
                os.write(fd, b"\n")  # try empty line
            else:
                os.write(fd, password.encode() + b"\n")
            sent = True
    p, status = os.waitpid(pid, os.WNOHANG)
    if p == pid:
        time.sleep(0.3)
        try:
            while True:
                chunk = os.read(fd, 4096)
                if not chunk: break
                out += chunk
        except OSError:
            pass
        print(out.decode(errors="replace"))
        sys.exit(os.waitstatus_to_exitcode(status))
print(out.decode(errors="replace"))
