#!/bin/sh
#
# Produce an opkg/ipk-compatible version string from our internal
# PACKAGE_VERSION, the ipk twin of normalize-apk-version.sh.
#
# Why: opkg compares versions with Debian-style ordering, where a hyphen
# suffix sorts AFTER the bare version — `0.6.0-rc1` compares GREATER than
# `0.6.0`, so a rc1 device would refuse the final release as a downgrade.
# Debian's pre-release encoding is the tilde: `0.6.0~rc1` sorts BEFORE
# `0.6.0`, which is exactly the upgrade topology testers need
# (alpha < beta < rc < final). The leading `v` of the tag is dropped for
# the same reason: a `v` inside the version string has no defined place
# in the ordering and makes every non-v version look like a downgrade.
#
# Inputs (same set the apk twin sees):
#   - Release tags:   v1.2.3, v1.2.3-alpha1, v1.2.3-beta2, v1.2.3-rc1
#   - Branch pushes:  <branch>.<height>.<shorthash>   e.g. main.123.abcdef0
#   - Pull requests:  97/merge -> sanitised to 97-merge.<height>.<sha>
#
# Tag inputs map to N.N.N with pre-release tags re-spelled ~alpha/~beta/
# ~rc/~pre. Branch / PR inputs can contain characters that make the
# ordering meaningless (hyphens are the revision separator in opkg), so
# they collapse to 0.0.0~git<HEIGHT>, which sorts before every release
# while staying unique per height. The human-readable PACKAGE_VERSION
# remains tollgate's runtime version string; this script only produces
# the ipk control-file value.
set -eu

version="${1#v}"

if [ -z "$version" ]; then
    printf '0.0.0~git0\n'
    exit 0
fi

# Release-tag format: N.N.N optionally followed by -alpha/beta/rc/preN.
if printf '%s' "$version" | grep -qE '^[0-9]+\.[0-9]+\.[0-9]+(-(alpha|beta|rc|pre)[0-9]*)?$'; then
    normalized=$(printf '%s' "$version" \
        | sed -e 's/-alpha/~alpha/g' \
              -e 's/-beta/~beta/g'   \
              -e 's/-pre/~pre/g'     \
              -e 's/-rc/~rc/g')
    printf '%s\n' "$normalized"
    exit 0
fi

# Branch / PR inputs: take the last digit-only dot-separated segment as
# the build height (main.123.abcdef0 -> 123; 97-merge.45.deadbee -> 45)
# and collapse; keeps dev builds upgrade-safe by sorting below every
# release.
height=$(printf '%s' "$version" | awk -F. '{for (i=1; i<=NF; i++) if ($i ~ /^[0-9]+$/) h=$i} END {print h+0}')
printf '0.0.0~git%s\n' "$height"
