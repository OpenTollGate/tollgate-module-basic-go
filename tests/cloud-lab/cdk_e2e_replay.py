"""cdk_e2e_replay.py — re-present a staged payment event.

Reads the kind-21000 event written by cdk_e2e_pay.py --event-out and POSTs
the IDENTICAL event again. Used by run-cdk-backend-e2e.sh leg B: after a
sidecar crash mid-payment and its restart+recovery, the same proofs must
never grant a second session (sidecar CheckTokenSpent via NUT-07).
"""

import argparse
import json
import os

import requests


def main() -> None:
    ap = argparse.ArgumentParser()
    ap.add_argument("event_file")
    ap.add_argument("--verbose", action="store_true",
                    help="include session tags (allotment is the session's CUMULATIVE total)")
    args = ap.parse_args()

    event = json.load(open(args.event_file))
    r = requests.post(os.environ["UPSTREAM_URL"], json=event, timeout=60)
    body = {}
    try:
        body = r.json()
    except ValueError:
        pass
    out = {"status": r.status_code, "kind": body.get("kind")}
    if args.verbose:
        tags = {t[0]: t[1:] for t in body.get("tags", [])}
        keep = ("expiry", "expires", "allotment", "amount", "session")
        out["tags"] = {k: tags[k] for k in tags if k in keep}
    print(json.dumps(out))


if __name__ == "__main__":
    main()
