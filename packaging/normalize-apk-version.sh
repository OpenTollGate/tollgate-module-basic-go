#!/bin/sh
#
# Produce an apk-tools-compatible version string from our internal
# PACKAGE_VERSION. apk versions must match:
#   <digit>[.<digit>]*[_<suffix><digit>*]*[-r<digit>+]
# i.e. the version body is digit.digit.digit (or similar), with '_'
# used for pre-release tags, and '-r<N>' reserved for the package
# release revision.
#
# Inputs we see:
#   - Release tags:   v1.2.3, v1.2.3-alpha1, v1.2.3-beta2, v1.2.3-rc1
#   - Branch pushes:  <branch>.<height>.<shorthash>   e.g. main.123.abcdef0
#   - Pull requests:  97/merge -> sanitised to 97-merge.<height>.<sha>
#
# Tag inputs are passed through (with -alpha/-beta/-rc → _alpha/_beta/_rc
# so apk sees them as pre-release markers).
#
# Branch / PR inputs can contain any punctuation apk dislikes (hyphens
# in branch names, slashes, etc.), so we don't try to preserve them in
# the apk version — we collapse to 0.0.0_git<HEIGHT>-r0, which is a
# valid apk pre-release version. The human-readable PACKAGE_VERSION is
# still exposed as tollgate's runtime version string and embedded in the
# ipk/apk filename; this script only produces the apk control-file value.
set -eu

version="${1#v}"

if [ -z "$version" ]; then
    printf '0.0.0-r0\n'
    exit 0
fi

# Release-tag format: N.N.N optionally followed by -alpha/beta/rc/preN.
# apk wants _alpha / _beta / _rc etc., not hyphens.
if printf '%s' "$version" | grep -qE '^[0-9]+\.[0-9]+\.[0-9]+(-(alpha|beta|rc|pre)[0-9]*)?$'; then
    normalized=$(printf '%s' "$version" \
        | sed -e 's/-alpha/_alpha/g' \
              -e 's/-beta/_beta/g'   \
              -e 's/-pre/_pre/g'     \
              -e 's/-rc/_rc/g')
    printf '%s-r0\n' "$normalized"
    exit 0
fi

# Branch / PR dev build: extract the commit-height component (second
# dot-segment) and embed it in a deterministic apk-safe stub.
build_nr=$(printf '%s' "$version" | sed -n 's/^[^.]*\.\([0-9][0-9]*\)\..*/\1/p')
if [ -n "$build_nr" ]; then
    printf '0.0.0_git%s-r0\n' "$build_nr"
else
    printf '0.0.0-r0\n'
fi
