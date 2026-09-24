#!/usr/bin/env python3
"""Resolve the cache-bust stub's own redirect expression.

The :2050 stub does not answer with a `Location:` header -- it is an HTML page
whose JavaScript builds the target at run time:

    location.replace('http://' + location.hostname + ':2051/splash.html?_cb='
                     + Date.now() + location.search.replace('?', '&'));

So "the stub redirects to the SPA on :2051" is a claim about an expression, not
about a header. This module PARSES that expression and evaluates the small JS
subset it is allowed to use, so the harness asserts the real target instead of
assuming a URL shape (assuming the shape is exactly the mistake this check
exists to catch).

Supported tokens: string literals, '+', location.hostname, location.port,
location.pathname, Date.now(), location.search.replace('?', '&').
Anything else -> non-zero exit with a reason, so the check FAILS loudly rather
than silently passing on a stub nobody understands any more.

stdlib only.
"""

import argparse
import re
import sys

EXPR_RE = re.compile(r"location\.replace\(\s*(?P<expr>.*?)\s*\)\s*;", re.S)
NOSCRIPT_RE = re.compile(r"<noscript>(.*?)</noscript>", re.S | re.I)
HREF_RE = re.compile(r'href\s*=\s*["\']([^"\']+)["\']', re.I)


def resolve(expr, host, search=""):
    """-> (url, error). Exactly one of the two is meaningful."""
    # split on '+' that is not inside a string literal
    parts, buf, quote = [], "", None
    for ch in expr:
        if quote:
            buf += ch
            if ch == quote:
                quote = None
            continue
        if ch in "'\"":
            quote = ch
            buf += ch
            continue
        if ch == "+":
            parts.append(buf)
            buf = ""
            continue
        buf += ch
    parts.append(buf)

    out = ""
    for raw in parts:
        token = raw.strip()
        if len(token) >= 2 and token[0] == token[-1] and token[0] in "'\"":
            out += token[1:-1]
            continue
        if token == "location.hostname":
            out += host
        elif token == "location.port":
            out += ""
        elif token == "location.pathname":
            return "", "expression uses location.pathname, which this resolver does not model"
        elif token == "location.search.replace('?', '&')" or \
             token == 'location.search.replace("?", "&")':
            out += search.replace("?", "&")
        elif token == "location.search":
            out += search
        elif token == "Date.now()":
            out += "1"          # a stand-in for a cache-bust value
        elif token == "":
            continue
        else:
            return "", "unrecognised token in the stub expression: %r" % token
    return out, ""


def main():
    ap = argparse.ArgumentParser()
    ap.add_argument("--stub-file", required=True)
    ap.add_argument("--host", required=True)
    ap.add_argument("--search", default="")
    ap.add_argument("--noscript-href", action="store_true")
    args = ap.parse_args()

    try:
        html = open(args.stub_file, "r", errors="replace").read()
    except OSError as exc:
        print("cannot read %s: %r" % (args.stub_file, exc), file=sys.stderr)
        return 2

    if args.noscript_href:
        block = NOSCRIPT_RE.search(html)
        if not block:
            print("no <noscript> block in the stub", file=sys.stderr)
            return 1
        href = HREF_RE.search(block.group(1))
        if not href:
            print("no anchor href inside the stub's <noscript> block", file=sys.stderr)
            return 1
        print(href.group(1))
        return 0

    match = EXPR_RE.search(html)
    if not match:
        print("no location.replace(...) expression in the stub", file=sys.stderr)
        return 1
    url, err = resolve(match.group("expr"), args.host, args.search)
    if err:
        print(err, file=sys.stderr)
        return 1
    print(url)
    return 0


if __name__ == "__main__":
    sys.exit(main())
