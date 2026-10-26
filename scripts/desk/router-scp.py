import os, pty, sys, select, time
# scp over pty: scpassh.py <remote> <localdir>   (empty-password login)
pid, fd = pty.fork()
if pid == 0:
    os.execvp("scp", ["scp", "-O", "-o", "StrictHostKeyChecking=accept-new",
                      "-o", "UserKnownHostsFile=/tmp/opencode/mt3000-knownhosts",
                      sys.argv[1], sys.argv[2]])
out = b""; sent = False; deadline = time.time() + 30
while time.time() < deadline:
    r, _, _ = select.select([fd], [], [], 1.0)
    if r:
        try: chunk = os.read(fd, 4096)
        except OSError: break
        if not chunk: break
        out += chunk
        if not sent and b"password" in out.lower():
            os.write(fd, b"\n"); sent = True
    p, status = os.waitpid(pid, os.WNOHANG)
    if p == pid: break
print(out.decode(errors="replace")[-400:])
