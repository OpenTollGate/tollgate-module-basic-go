# The gonuts-tollgate pin is ONE version in every carrier (#791): the manifest
# names it, check-gonuts-pin.py discovers carriers by glob and refuses any
# disagreement — offline and fast, so it belongs here. go.sum is a trigger too:
# a tidy can move a pin without a go.mod hunk. The --latest staleness leg is
# deliberately NOT here — network in a hook means flaky commits; release-check
# owns that verdict.
if git diff --cached --name-only | grep -qE '(^|/)go\.mod$|(^|/)go\.sum$|^packaging/build-inputs\.json$'; then
    echo "🔍 Checking the gonuts-tollgate pin (one version, everywhere)..."
    python3 tests/contract/check-gonuts-pin.py
fi
