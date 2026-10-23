#!/usr/bin/env python3
"""Run a batch of router commands as ONE bench-lock transaction.

WHY THIS EXISTS
An 87-command line-by-line upload is not atomic: losing the lock mid-transfer
leaves a half-written script AND a shell stuck in continuation mode (the `>`
prompt), which then blocks every other user of the port.

THE BUG THIS FILE USED TO HAVE
The first version opened /dev/ttyUSB1 directly and relied on a caller-side
/proc/*/fd scan to decide whether the bench was free. That scan is advisory and
racy: between the check and the open(), another context window can take the port.
So it drove the UART WITHOUT EVER HOLDING THE LOCK -- exactly the two-writer
failure the bench mutex exists to prevent. Do not reintroduce that pattern.

Every command also carries a `timeout` so a hung shell cannot pin the lock
forever; on timeout we release and exit non-zero rather than block the bench.

Usage: bench-cmd.py "cmd1" "cmd2" ...
Exit:  0 ok | 1 command failure | 75 bench held by another owner | 124 timeout
"""
import os
import signal
import sys
import time

sys.path.insert(0, os.path.expanduser("~/worktrees/bench-mutex/scripts"))
import bench_lock  # noqa: E402
import serial  # noqa: E402

PORT = "/dev/ttyUSB1"
PROMPT = b"# "
TASK = os.environ.get("TG_BENCH_TASK", "bench-cmd")
# Hard ceiling on the whole transaction, so a wedged shell releases the bench.
WALL_LIMIT = float(os.environ.get("BENCH_WALL_LIMIT", "600"))


class WallTimeout(Exception):
    pass


def _alarm(signum, frame):
    raise WallTimeout()


def run(cmds):
    s = serial.Serial(PORT, 115200, timeout=1)
    buf = b""
    try:
        # resync: interrupt anything half-typed, then confirm a prompt
        for _ in range(5):
            s.write(b"\x03")
            time.sleep(0.3)
        s.reset_input_buffer()
        s.write(b"\n")
        time.sleep(1.0)
        buf = s.read(65536)
        if PROMPT not in buf:
            s.write(b"\x03")
            time.sleep(0.5)
            s.write(b"\n")
            time.sleep(1.0)
            buf = s.read(65536)

        for cmd in cmds:
            s.reset_input_buffer()
            s.write(cmd.encode() + b"\n")
            deadline = time.time() + 30
            got = b""
            while time.time() < deadline:
                chunk = s.read(4096)
                if chunk:
                    got += chunk
                    if PROMPT in chunk:
                        break
                else:
                    time.sleep(0.1)
            text = got.decode("utf-8", "replace")
            for line in text.replace("\r", "").split("\n"):
                ln = line.strip()
                if not ln or ln.startswith("root@") or ln == cmd.strip():
                    continue
                if ln.startswith("_probe_"):
                    continue
                print(ln)
    finally:
        s.close()


def _tty_holders():
    """PIDs with /dev/ttyUSB1 open, excluding ourselves.

    The flock alone cannot stop a process that ignores it (measured: a script
    holding only the tty caused bench-cmd to open the port and run, corrupting the
    session). This scan is the defence that catches non-locking writers. It is
    still racy on its own -- it is a check, not a lock -- but combined with the
    flock it closes the realistic gap.
    """
    target = os.path.realpath(PORT)
    me = os.getpid()
    found = []
    for pid in os.listdir("/proc"):
        if not pid.isdigit() or int(pid) == me:
            continue
        try:
            for fd in os.listdir(f"/proc/{pid}/fd"):
                try:
                    if os.path.realpath(f"/proc/{pid}/fd/{fd}") == target:
                        try:
                            cmd = open(f"/proc/{pid}/cmdline", "rb").read().replace(b"\0", b" ").decode().strip()
                        except OSError:
                            cmd = "?"
                        found.append((pid, cmd))
                        break
                except OSError:
                    continue
        except OSError:
            continue
    return found


def main(cmds):
    # THE LOCK IS TAKEN FIRST, BEFORE THE PORT IS OPENED. Nothing below may touch
    # /dev/ttyUSB1 without holding this handle.
    try:
        lock = bench_lock.acquire(TASK)
    except bench_lock.LockDenied as exc:
        print(f"DENIED: {exc}", file=sys.stderr)
        return 75
    except bench_lock.BenchLockError as exc:
        print(f"LOCK ERROR: {exc}", file=sys.stderr)
        return 75

    # A non-locking writer can still hold the UART. Refuse rather than corrupt it.
    rogue = _tty_holders()
    if rogue:
        print("DENIED: /dev/ttyUSB1 is held by a process that did not take the lock:", file=sys.stderr)
        for pid, cmd in rogue:
            print(f"  PID {pid}: {cmd[:120]}", file=sys.stderr)
        print("  Do not co-write. Wait for it to exit, or investigate it.", file=sys.stderr)
        bench_lock.release(lock)
        return 75

    try:
        signal.signal(signal.SIGALRM, _alarm)
        signal.alarm(int(WALL_LIMIT))
        run(cmds)
        return 0
    except WallTimeout:
        print(
            f"TIMEOUT after {WALL_LIMIT:.0f}s -- releasing bench (shell may be wedged)",
            file=sys.stderr,
        )
        return 124
    finally:
        signal.alarm(0)
        bench_lock.release(lock)


if __name__ == "__main__":
    sys.exit(main(sys.argv[1:]))
