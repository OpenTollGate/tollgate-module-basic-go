"""cdk_e2e_sockproxy.py — LEG D's deterministic ambiguity injector.

An AF_UNIX proxy between the module (w.sock) and the daemon (real.sock).
In pass-through mode it forwards both directions untouched. In swallow
mode (--swallow receive) the REQUEST direction forwards a matching request
and arms a one-shot latch; the RESPONSE direction then reads the daemon's
answer and DROPS it, closing both sides. The daemon provably executed the
call, the module provably never got the answer — ErrSidecarAmbiguous /
ErrOutcomeUnknown, deterministically (no kill-timing race).

Design note (the bug this shape fixes): the swallow must live in the
RESPONSE pump. Two independent readers race on the upstream socket — a
swallow that reads the response from the request side loses the race to
the reverse pump, which happily forwards the answer (measured: the module
got the response and granted, no swallow fired).
"""

import argparse
import os
import re
import socket
import threading

METHOD_RE = re.compile(r'"method"\s*:\s*"([^"]+)"')


def request_pump(client: socket.socket, upstream: socket.socket, swallow_method, state) -> None:
    """client -> upstream. Forwards every line; arms the latch on a
    swallow-method request (the response pump consumes the latch)."""
    buf = b""
    try:
        while True:
            chunk = client.recv(4096)
            if not chunk:
                break
            buf += chunk
            *lines, buf = buf.split(b"\n")
            for line in lines:
                text = line.decode("utf-8", "replace")
                m = METHOD_RE.search(text)
                print(f"FWD {m.group(1) if m else '?'}", flush=True)
                upstream.sendall(line + b"\n")
                if swallow_method and m and m.group(1) == swallow_method:
                    state["armed"] = True
                    print(f"ARMED swallow of next response ({swallow_method})", flush=True)
    except OSError:
        pass
    try:
        upstream.shutdown(socket.SHUT_WR)
    except OSError:
        pass


def response_pump(upstream: socket.socket, client: socket.socket, state) -> None:
    """upstream -> client. When the latch is armed, the next complete
    response line is read and DROPPED, and both sides are closed."""
    buf = b""
    try:
        while True:
            chunk = upstream.recv(4096)
            if not chunk:
                break
            if not state["armed"]:
                client.sendall(chunk)
                continue
            buf += chunk
            if b"\n" not in buf:
                continue
            # The daemon answered the swallowed request: read it, drop it.
            print("SWALLOWED response", flush=True)
            state["armed"] = False
            state["count"] += 1
            client.close()
            upstream.close()
            return
    except OSError:
        pass
    try:
        client.shutdown(socket.SHUT_WR)
    except OSError:
        pass


def handle(client: socket.socket, real_path: str, swallow_method, state) -> None:
    upstream = socket.socket(socket.AF_UNIX, socket.SOCK_STREAM)
    try:
        upstream.connect(real_path)
    except OSError as e:
        print(f"upstream connect failed: {e}", flush=True)
        client.close()
        return
    t = threading.Thread(target=response_pump, args=(upstream, client, state), daemon=True)
    t.start()
    request_pump(client, upstream, swallow_method, state)


def main() -> None:
    ap = argparse.ArgumentParser()
    ap.add_argument("listen_path")
    ap.add_argument("upstream_path")
    ap.add_argument("--swallow", default=None, help="method name whose RESPONSE is dropped")
    args = ap.parse_args()

    try:
        os.unlink(args.listen_path)
    except FileNotFoundError:
        pass
    state = {"armed": False, "count": 0}
    srv = socket.socket(socket.AF_UNIX, socket.SOCK_STREAM)
    srv.bind(args.listen_path)
    srv.listen(8)
    print(f"proxy {args.listen_path} -> {args.upstream_path} (swallow={args.swallow})", flush=True)
    while True:
        client, _ = srv.accept()
        threading.Thread(
            target=handle, args=(client, args.upstream_path, args.swallow, state), daemon=True
        ).start()


if __name__ == "__main__":
    main()
