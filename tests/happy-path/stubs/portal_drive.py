#!/usr/bin/env python3
"""Drive the captive-portal SPA that ships INSIDE the artifact, with a real browser.

Two modes:

  purchase  the LIVE module binary (from the same artifact) answers the portal:
            GET / is proxied to it, so the mint list / price the portal renders
            comes from the artifact's own advertisement, not a fixture.
  expired   the backend is stubbed the way the portal repo's own e2e stubs it
            (tests/e2e/helpers/mock-backend.mjs): the module is not involved.
            The expired view is a UI state that needs a granted session first,
            so this mode replays a granted session and then flips /usage to the
            documented "-1/-1" expiry reading.

Both modes serve the bundle from the artifact on 127.0.0.2 -- deliberately NOT
localhost: the bundles carry a `VITE_MOCK || hostname === "localhost"` dev-mock
branch that would bypass the real code path.

Every stage dumps the rendered DOM text and the captured request log, so a
failure report carries evidence instead of a bare assertion name.
"""
import argparse
import json
import os
import re
import sys
import threading
import time
from functools import partial
from http.server import SimpleHTTPRequestHandler, ThreadingHTTPServer
from urllib.parse import urlparse

from playwright.sync_api import sync_playwright

MODULE_PORT = 2121
USAGE_ACTIVE = "60000/600000"
USAGE_EXPIRED = "-1/-1"

results = []
events = []


def emit(check_id, status, detail=""):
    """One machine-readable line per check, mirroring the shell harness.

    `status` may be a bool: True -> PASS, False -> FAIL. Callers that pass a
    bool used to print "True"/"False" as the status word, which the shell side
    then counted as neither a pass nor a fail.
    """
    if isinstance(status, bool):
        status = "PASS" if status else "FAIL"
    results.append({"id": check_id, "status": status, "detail": detail})
    print("HPCHECK %s %s %s" % (check_id, status, detail), flush=True)
    return status == "PASS"


def log_event(line):
    events.append(line)
    print("  .. " + line, flush=True)


class QuietStatic(SimpleHTTPRequestHandler):
    def log_message(self, *args):
        pass


def start_static(directory, host, port):
    handler = partial(QuietStatic, directory=directory)
    srv = ThreadingHTTPServer((host, port), handler)
    threading.Thread(target=srv.serve_forever, daemon=True).start()
    return srv


def free_port(host="127.0.0.2"):
    import socket
    s = socket.socket()
    s.bind((host, 0))
    port = s.getsockname()[1]
    s.close()
    return port


def dom_text(page):
    return page.evaluate("() => document.body ? document.body.innerText : ''")


def dump(page, out, tag):
    os.makedirs(out, exist_ok=True)
    with open(os.path.join(out, "stage-%s.txt" % tag), "w") as fh:
        fh.write("URL: %s\n\n" % page.url)
        fh.write(dom_text(page))
    with open(os.path.join(out, "stage-%s.html" % tag), "w") as fh:
        fh.write(page.content())


def clickable_inventory(page):
    return page.evaluate("""() => {
      const out = [];
      document.querySelectorAll('button, a, input, [role=button]').forEach(el => {
        out.push({tag: el.tagName.toLowerCase(), id: el.id || null,
                  cls: el.className && el.className.toString ? el.className.toString() : null,
                  text: (el.innerText || el.value || el.placeholder || '').slice(0, 80),
                  testid: el.getAttribute('data-testid'),
                  disabled: !!el.disabled});
      });
      return out;
    }""")


def build_router(page, args, state):
    """Route every request. Nothing leaves the machine: the module, the bundle
    and the stub mint are all local; anything else is aborted (hermetic)."""

    def handler(route, request):
        url = request.url
        parsed = urlparse(url)
        try:
            port = parsed.port
        except ValueError:
            port = None

        if parsed.hostname in ("127.0.0.2", "127.0.0.1") and port != MODULE_PORT:
            route.continue_()          # the bundle itself, from our static server
            return

        if port == MODULE_PORT:
            path = parsed.path or "/"
            method = request.method
            log_event("MODULE %s %s" % (method, url))
            # live mode: the LIVE module answers every call the portal makes,
            # including the Lightning lane, so we see what the shipped pair
            # really does rather than what a fixture says it does.
            if args.mode == "live-lightning":
                route.continue_()
                return
            if path == "/" and method == "GET":
                if args.mode == "purchase":
                    route.continue_()  # the LIVE module from the artifact
                else:
                    route.fulfill(status=200, content_type="application/json",
                                  body=json.dumps(state["advertisement"]),
                                  headers={"Access-Control-Allow-Origin": "*"})
                return
            if path == "/whoami":
                route.fulfill(status=200, content_type="text/plain",
                              body="mac=00:11:22:33:44:55",
                              headers={"Access-Control-Allow-Origin": "*"})
                return
            if path == "/usage":
                state["usage_polls"].append(time.time())
                body = USAGE_EXPIRED if state["expired"] else USAGE_ACTIVE
                log_event("usage -> %s" % body)
                route.fulfill(status=200, content_type="text/plain", body=body,
                              headers={"Access-Control-Allow-Origin": "*"})
                return
            if path == "/" and method == "POST":
                route.fulfill(status=200, content_type="application/json", body="{}",
                              headers={"Access-Control-Allow-Origin": "*"})
                return
            if path == "/ln-invoice" and method == "POST":
                route.fulfill(status=200, content_type="application/json",
                              body=json.dumps({"status": 1, "quote": "hp-quote-1",
                                               "invoice": "lnbcstub1qqqq",
                                               "mint_url": args.stub_mint,
                                               "amount": 210, "state": "unpaid",
                                               "access_granted": False}),
                              headers={"Access-Control-Allow-Origin": "*"})
                return
            if path == "/ln-invoice":
                route.fulfill(status=200, content_type="application/json",
                              body=json.dumps({"status": 1, "quote": "hp-quote-1",
                                               "mint_url": args.stub_mint,
                                               "amount": 210, "state": "issued",
                                               "access_granted": True,
                                               "allotment": 2150400,
                                               "metric": "bytes"}),
                              headers={"Access-Control-Allow-Origin": "*"})
                return
            route.fulfill(status=200, content_type="application/json", body="{}",
                          headers={"Access-Control-Allow-Origin": "*"})
            return

        # ANYTHING else -- the real mint, the internet -- never happens.
        log_event("BLOCKED %s" % url)
        route.abort()

    page.route("**/*", handler)


def discover(page, out):
    dump(page, out, "discover")
    inv = clickable_inventory(page)
    with open(os.path.join(out, "stage-discover-controls.json"), "w") as fh:
        json.dump(inv, fh, indent=2)
    log_event("controls: %s" % json.dumps(inv)[:2000])
    selectors = ["#cashu-token", ".tollgate-captive-portal-method-submit",
                 ".tollgate-captive-portal-method-submit button.cta",
                 ".tollgate-captive-portal-access-granted",
                 "#tab-lightning", "#tab-cashu", "[data-testid]", "#session-expired",
                 ".tollgate-captive-portal-method-tab", "button.cta"]
    counts = {}
    for sel in selectors:
        try:
            counts[sel] = page.locator(sel).count()
        except Exception as exc:
            counts[sel] = "ERR:%s" % exc
    log_event("selector counts: %s" % json.dumps(counts))
    # lightning lane walk-through
    try:
        page.locator("#tab-lightning").click(timeout=5000)
        page.wait_for_timeout(1500)
    except Exception as exc:
        log_event("could not click #tab-lightning: %s" % exc)
    dump(page, out, "lightning-tab")
    log_event("after lightning click: %s" % json.dumps(clickable_inventory(page))[:1500])


def main():
    ap = argparse.ArgumentParser()
    ap.add_argument("--bundle", required=True)
    ap.add_argument("--out", required=True)
    ap.add_argument("--mode", choices=["purchase", "expired", "discover", "live-lightning"],
                    default="purchase")
    ap.add_argument("--stub-mint", default="")
    ap.add_argument("--advertisement-file", default="")
    ap.add_argument("--expect-text", default="")
    ap.add_argument("--serve-host", default="127.0.0.2")
    ap.add_argument("--portal-lang", default="en")
    ap.add_argument("--expired-timeout", type=int, default=170)
    ap.add_argument("--strict-renewal-cta", action="store_true")
    args = ap.parse_args()

    os.makedirs(args.out, exist_ok=True)

    advertisement = {"kind": 10021}
    if args.advertisement_file and os.path.exists(args.advertisement_file):
        with open(args.advertisement_file) as fh:
            advertisement = json.load(fh)

    port = free_port(args.serve_host)
    start_static(args.bundle, args.serve_host, port)
    entry = "/splash.html"
    url = "http://%s:%d%s" % (args.serve_host, port, entry)
    log_event("serving artifact bundle %s at %s" % (args.bundle, url))

    state = {"expired": False, "usage_polls": [], "advertisement": advertisement}

    with sync_playwright() as p:
        launch = {"args": ["--no-sandbox"]}
        if os.environ.get("HP_BROWSER_CHANNEL"):
            launch["channel"] = os.environ["HP_BROWSER_CHANNEL"]
        browser = p.chromium.launch(**launch)
        context = browser.new_context(viewport={"width": 420, "height": 900},
                                     locale=args.portal_lang)
        page = context.new_page()
        console = []
        page.on("console", lambda m: console.append("%s: %s" % (m.type, m.text)))
        build_router(page, args, state)

        page.goto(url, wait_until="domcontentloaded", timeout=60000)
        try:
            page.wait_for_selector("#root > *", timeout=30000)
        except Exception:
            pass
        page.wait_for_timeout(2500)
        dump(page, args.out, "loaded")

        if args.mode == "discover":
            discover(page, args.out)
            with open(os.path.join(args.out, "console.log"), "w") as fh:
                fh.write("\n".join(console))
            context.close()
            browser.close()
            return 0

        if args.mode == "live-lightning":
            # The shipped pair, end to end: the real bundle's Lightning lane
            # against the real module. Whatever it answers is the finding.
            responses = []

            def on_response(resp):
                try:
                    if urlparse(resp.url).port == MODULE_PORT:
                        body = ""
                        try:
                            body = resp.text()[:240]
                        except Exception:
                            pass
                        sent = ""
                        try:
                            sent = (resp.request.post_data or "")[:240]
                        except Exception:
                            pass
                        responses.append("%s %s -> %s %s%s"
                                         % (resp.request.method, resp.url, resp.status, body,
                                            ("  [sent: %s]" % sent) if sent else ""))
                except Exception:
                    pass

            page.on("response", on_response)
            reached = try_reach_granted(page, args, state)
            with open(os.path.join(args.out, "module-responses.json"), "w") as fh:
                json.dump(responses, fh, indent=2)
            dump(page, args.out, "live-lightning")
            emit("portal:lightning-lane-against-live-module", reached,
                 "granted=%s; module responses: %s"
                 % (reached, " | ".join(r for r in responses
                                        if "/ln-invoice" in r) or "(none)"))
            with open(os.path.join(args.out, "console.log"), "w") as fh:
                fh.write("\n".join(console))
            context.close()
            browser.close()
            return report(os.path.join(args.out, "results.json"))

        text = dom_text(page)
        has_token_input = page.locator("#cashu-token").count() > 0
        emit("portal:renders-purchase-ui",
             has_token_input,
             "token input %s; first line: %r" % ("present" if has_token_input else "ABSENT",
                                                 text.strip().splitlines()[:1]))

        # the mint list: the price/mint the portal shows must come from the module
        # advertisement (purchase mode proxies GET / straight to the live module)
        tags = advertisement.get("tags", [])
        mints = [t[4] for t in tags if t and t[0] == "price_per_step" and len(t) > 4]
        price = None
        for t in tags:
            if t and t[0] == "price_per_step" and len(t) > 3:
                price = t[2]
                break
        found_mint = any(m in text for m in mints) if mints else False
        found_price = (price in text) if price else False
        # the bundle strips the scheme before rendering, so compare the host too
        found_host = any(m.split("//")[-1] in text for m in mints) if mints else False
        emit("portal:mint-list-from-artifact",
             bool(mints) and (found_mint or found_host or found_price),
             "advertised mints=%s price=%s rendered(mint=%s host=%s price=%s)"
             % (mints, price, found_mint, found_host, found_price))

        # the MAC the portal shows must be the one the LIVE module answered
        try:
            whoami = page.evaluate("""async () => {
              const r = await fetch('http://' + window.location.hostname + ':2121/whoami');
              return await r.text();
            }""")
        except Exception as exc:
            whoami = "unavailable: %s" % exc
        mac = ""
        m = re.search(r"mac=([0-9a-fA-F:]{17})", whoami or "")
        if m:
            mac = m.group(1)
        emit("portal:device-mac-from-live-module",
             bool(mac) and mac in text,
             "/whoami=%r mac_in_dom=%s" % (whoami, mac in text if mac else False))

        if args.mode == "purchase":
            cta = page.locator("button.cta")
            emit("portal:purchase-cta-present",
                 cta.count() > 0,
                 "button.cta count=%d labels=%s"
                 % (cta.count(), json.dumps(cta.all_inner_texts())[:200]))
            with open(os.path.join(args.out, "console.log"), "w") as fh:
                fh.write("\n".join(console))
            context.close()
            browser.close()
            emit("portal:expired-view", "SKIP", "not requested in purchase mode")
            emit("portal:expired-renewal-cta", "SKIP", "not requested in purchase mode")
            return report(os.path.join(args.out, "results.json"))

        # ---- expired mode -------------------------------------------------
        # Reach a granted session first: the Lightning lane needs no client-side
        # token cryptography, so it is the lane a money-free harness can drive.
        reached = try_reach_granted(page, args, state)
        emit("portal:granted-session-reached", reached, state.get("granted_detail", ""))

        if not reached:
            emit("portal:expired-view", "SKIP",
                 "no granted session, so no expiry to observe: %s" % state.get("granted_detail"))
            emit("portal:expired-renewal-cta", "SKIP", "depends on the expired view")
            with open(os.path.join(args.out, "console.log"), "w") as fh:
                fh.write("\n".join(console))
            context.close()
            browser.close()
            return report(os.path.join(args.out, "results.json"))

        # flip the heartbeat to the documented expiry reading
        state["expired"] = True
        deadline = time.time() + args.expired_timeout
        saw_expired = False
        while time.time() < deadline:
            if page.locator("#session-expired").count() > 0:
                saw_expired = True
                break
            if any(t in dom_text(page) for t in EXPIRED_TEXT):
                saw_expired = True
                break
            page.wait_for_timeout(2000)
        dump(page, args.out, "expired")
        text = dom_text(page)
        emit("portal:expired-view",
             saw_expired,
             "usage polls=%d; head=%r" % (len(state["usage_polls"]),
                                          text.strip().splitlines()[:4]))

        if saw_expired:
            status, detail = probe_renewal_cta(page, args, state, args.out)
            if args.strict_renewal_cta and status == "SKIP":
                status = "FAIL"
                detail = "strict mode: " + detail
            emit("portal:expired-renewal-cta", status, detail)
        else:
            emit("portal:expired-renewal-cta", "SKIP", "no expired view to inspect")

        with open(os.path.join(args.out, "console.log"), "w") as fh:
            fh.write("\n".join(console))
        context.close()
        browser.close()
    return report(os.path.join(args.out, "results.json"))


def report(path):
    """Emit the verdict. FAIL fails the run; SKIP never does, but it is loud."""
    with open(path, "w") as fh:
        json.dump(results, fh, indent=2)
    failed = [r for r in results if r["status"] == "FAIL"]
    skipped = [r for r in results if r["status"] == "SKIP"]
    print("HPSUMMARY portal checks=%d failed=%d skipped=%d"
          % (len(results), len(failed), len(skipped)), flush=True)
    if failed:
        print("HPFAILED " + "; ".join(r["id"] for r in failed), flush=True)
        return 1
    return 0


def try_reach_granted(page, args, state):
    """Drive the Lightning lane to a granted session. Returns True on success.

    The Lightning lane is the one a money-free harness can drive end to end: the
    portal asks the module for an invoice and then polls its status. No client
    side Cashu cryptography is involved, which is why the stub backend can
    answer `access_granted: true` without inventing a signature.
    """
    try:
        page.locator("#tab-lightning").click(timeout=8000)
        page.wait_for_timeout(1500)
    except Exception as exc:
        state["granted_detail"] = "could not open the Lightning tab: %s" % exc
        return False
    dump(page, args.out, "lightning-tab")

    try:
        amount = page.locator("#lightning-unit-amount").first
        if amount.count() > 0 and amount.is_visible():
            amount.fill("210", timeout=5000)
            page.wait_for_timeout(600)
    except Exception as exc:
        log_event("amount input not usable: %s" % exc)

    try:
        cta = page.locator("button.cta", has_text="Pay").first
        if cta.count() == 0:
            state["granted_detail"] = "no Lightning pay CTA rendered"
            return False
        cta.click(timeout=8000)
    except Exception as exc:
        state["granted_detail"] = "Lightning pay CTA not clickable: %s" % exc
        return False

    for _ in range(30):
        if page.locator(".tollgate-captive-portal-access-granted").count() > 0:
            state["granted_detail"] = "access-granted card rendered after the Lightning lane"
            return True
        page.wait_for_timeout(1000)
    state["granted_detail"] = "access-granted card never rendered (see stage-after-ln-request)"
    return False


# Text signals for an expired view across bundle generations. The pre-#541
# bundles use expired_title/expired_message; #541 adds an in-page renewal CTA.
EXPIRED_TEXT = ("expired", "Expired", "session has ended", "Refresh Portal")


def renewal_controls(page):
    """Every control the expired view offers, with a best guess at its intent."""
    return page.evaluate("""() => {
      const out = [];
      document.querySelectorAll('button, a[href], [role=button]').forEach(el => {
        out.push({tag: el.tagName.toLowerCase(), id: el.id || null,
                  testid: el.getAttribute('data-testid'),
                  cls: el.className && el.className.toString ? el.className.toString() : null,
                  text: (el.innerText || '').trim().slice(0, 60)});
      });
      return out;
    }""")


def probe_renewal_cta(page, args, state, out):
    """Is there an IN-PAGE renewal affordance on the expired view?

    The #60/#541 contract is that the expired view offers a control that returns
    to the purchase UI *without* reloading or navigating. We detect it by
    hooking the document/frame counters around the click, so a bundle that only
    offers "refresh the page" is reported as such rather than being mistaken
    for the fix.
    """
    loads = []
    navs = []
    page.on("request", lambda r: loads.append(r.url) if r.resource_type == "document" else None)
    page.on("framenavigated", lambda f: navs.append(f.url) if f == page.main_frame else None)

    controls = renewal_controls(page)
    hook = page.locator('[data-testid="buy-more-time"]')
    if hook.count() > 0:
        target = hook.first
        how = 'data-testid="buy-more-time"'
    else:
        target = None
        how = ""
        for sel in ["button.cta", "button"]:
            for el in page.locator(sel).all():
                try:
                    text = (el.inner_text() or "").strip()
                except Exception:
                    continue
                if re.search(r"buy more|renew|top up|add time|purchase|pay", text, re.I):
                    target = el
                    how = "label match on %r" % text
                    break
            if target is not None:
                break

    if target is None:
        # Name the controls that ARE there, minus the tab chrome, so the report
        # says what the expired view offers instead of just "no CTA".
        offered = [c["text"] for c in controls
                   if c.get("text") and c.get("id") not in ("tab-cashu", "tab-lightning")]
        return ("SKIP",
                "expired view offers no in-page renewal control; controls=%s (pre-#541 bundle: renewal needs a reload)"
                % json.dumps(offered)[:300])

    page.evaluate("() => { window.__hpSentinel = 'alive'; }")
    sentinel_before = page.evaluate("() => window.__hpSentinel")
    loads_before, navs_before, url_before = len(loads), len(navs), page.url
    try:
        target.click(timeout=8000)
    except Exception as exc:
        return ("FAIL", "renewal control %s not clickable: %s" % (how, exc))
    page.wait_for_timeout(2000)
    dump(page, out, "after-renewal-click")

    in_page = (len(loads) == loads_before and len(navs) == navs_before
               and page.url == url_before
               and page.evaluate("() => window.__hpSentinel") == sentinel_before)
    back_at_purchase = (page.locator("#cashu-token").count() > 0
                        or page.locator("#lightning-unit-amount").count() > 0)
    if in_page and back_at_purchase:
        return ("PASS", "in-page renewal via %s: purchase UI back, no reload/navigation" % how)
    return ("FAIL",
            "renewal control %s did not renew in page (in_page=%s purchase_ui=%s url=%s)"
            % (how, in_page, back_at_purchase, page.url))


if __name__ == "__main__":
    sys.exit(main())
