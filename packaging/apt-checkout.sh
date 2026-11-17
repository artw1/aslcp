#!/bin/bash
# Check out the apt repository (the gh-pages branch of this repository) into DIR.
#
#   packaging/apt-checkout.sh [--create] DIR
#
#   --create  when gh-pages does not exist yet (the very first release), start an
#             empty orphan gh-pages branch in DIR instead of failing. The branch
#             is created on the remote by the first push (git push origin HEAD:gh-pages).
#
# Environment:
#   GH_TOKEN           token with contents: write (the workflow's github.token)
#   GITHUB_REPOSITORY  owner/name (set by GitHub Actions)
#   REMOTE_URL         override the remote (for local tests; no token is used then)
#
# The checkout is shallow (the history of the pool is not needed). The token is
# stored in DIR/.git/config only as a header for the later push.
set -euo pipefail

create=0
if [ "${1:-}" = "--create" ]; then create=1; shift; fi
[ $# -eq 1 ] || { echo "usage: apt-checkout.sh [--create] DIR" >&2; exit 2; }
dir=$1

header=""
if [ -n "${REMOTE_URL:-}" ]; then
	url=$REMOTE_URL
else
	: "${GITHUB_REPOSITORY:?GITHUB_REPOSITORY is not set}" "${GH_TOKEN:?GH_TOKEN is not set}"
	url="${GITHUB_SERVER_URL:-https://github.com}/$GITHUB_REPOSITORY.git"
	header="AUTHORIZATION: basic $(printf 'x-access-token:%s' "$GH_TOKEN" | base64 -w0)"
fi
# git_auth ARGS...: git with the auth header (when there is one)
git_auth() {
	if [ -n "$header" ]; then git -c "http.extraheader=$header" "$@"; else git "$@"; fi
}

rc=0
git_auth ls-remote --exit-code --heads "$url" gh-pages >/dev/null || rc=$?
if [ "$rc" -ne 0 ] && [ "$rc" -ne 2 ]; then
	echo "apt-checkout: cannot reach $url (git exit $rc)" >&2 # 2 = no such branch
	exit 1
fi
if [ "$rc" -eq 0 ]; then
	git_auth clone --quiet --no-tags --depth 1 --branch gh-pages "$url" "$dir"
elif [ "$create" -eq 1 ]; then
	echo "apt-checkout: no gh-pages branch yet: starting an orphan branch"
	git init --quiet "$dir"
	git -C "$dir" checkout --quiet --orphan gh-pages
	git -C "$dir" remote add origin "$url"
else
	echo "apt-checkout: the gh-pages branch does not exist (publish a release first)" >&2
	exit 1
fi
if [ -n "$header" ]; then
	git -C "$dir" config http.extraheader "$header"
fi
