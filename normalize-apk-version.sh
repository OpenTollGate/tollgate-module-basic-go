#!/bin/sh

set -eu

version="${1#v}"

case "$version" in
    "")
        printf '0.0.0-r0\n'
        ;;
    [0-9]*)
        normalized=$(printf '%s' "$version" | sed -e 's/-alpha/_alpha/g' -e 's/-beta/_beta/g' -e 's/-pre/_pre/g' -e 's/-rc/_rc/g' -e 's/-git/_git/g')
        case "$normalized" in
            *-r[0-9]*) ;;
            *) normalized="${normalized}-r0" ;;
        esac
        printf '%s\n' "$normalized"
        ;;
    *)
        build_nr=$(printf '%s' "$version" | sed -n 's/^[^.]*\.\([0-9][0-9]*\)\..*/\1/p')
        if [ -n "$build_nr" ]; then
            printf '0.0.0_git%s-r0\n' "$build_nr"
        else
            printf '0.0.0-r0\n'
        fi
        ;;
esac
