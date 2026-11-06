#!/bin/sh
# Prints the release identity of this build: the tag the commit sits under, the
# nearest tag plus its distance, or an honest "unknown".
#
# Resolution order:
#   1. BUILD_RELEASE from the environment, when a pipeline already knows it.
#   2. `git describe --tags --always --dirty`, when the build context carries a
#      repository. True for local builds and `docker build` from a clone.
#   3. The GitHub API, when the context has no repository but the commit is
#      known. This is the Railway case: its builder strips .git before building,
#      so .dockerignore cannot help, and RAILWAY_GIT_COMMIT_SHA is the only
#      thing that survives. Requires the repository to be publicly readable.
#   4. "unknown".
#
# Usage: describe-release.sh [commit-sha]
#
# A value nobody can trace to git is worse than no value, so no branch here
# invents a version.
set -eu

commit="${1:-}"
repo="${RELEASE_REPO:-andywithcamera/velm}"
api="${RELEASE_GITHUB_API:-https://api.github.com}"

if [ -n "${BUILD_RELEASE:-}" ]; then
	printf '%s\n' "$BUILD_RELEASE"
	exit 0
fi

# `git describe` walks up from the current directory, so only trust it from a
# real repository root.
if git rev-parse --git-dir >/dev/null 2>&1; then
	# A shallow clone holds the commit but not the history behind it, where
	# describe degrades to a bare sha. Try to deepen once; a build context
	# usually has no credentials to fetch with, so failure is expected and
	# the API fallback below still applies.
	git_dir=$(git rev-parse --git-dir)
	if [ -f "$git_dir/shallow" ]; then
		git fetch --unshallow --tags --quiet >/dev/null 2>&1 ||
			git fetch --tags --quiet >/dev/null 2>&1 || true
	fi
	if described=$(git describe --tags --always --dirty 2>/dev/null) && [ -n "$described" ]; then
		printf '%s\n' "$described"
		exit 0
	fi
fi

[ -n "$commit" ] || commit="${RAILWAY_GIT_COMMIT_SHA:-}"
case "$commit" in
'' | unknown) printf 'unknown\n'; exit 0 ;;
esac

# --- GitHub fallback -------------------------------------------------------
# Ask which tag the commit is under, and how far past it the commit sits. Both
# answers come from the platform that owns the truth about the repository.

api_get() {
	curl -fsS --max-time 10 -H 'Accept: application/vnd.github+json' "$1" 2>/dev/null
}

# GitHub answers pretty-printed, with a space after each colon, so every
# pattern tolerates optional whitespace rather than assuming compact JSON.
json_string() {
	printf '%s' "$1" | grep -o "\"$2\"[[:space:]]*:[[:space:]]*\"[^\"]*\"" | head -1 | cut -d '"' -f4
}

# A tag object carries two "sha" values: the tag object's own, then the commit
# it points at. Take the one inside the nested object, since that is the commit.
json_nested_object_sha() {
	printf '%s' "$1" | sed -n '/"object"[[:space:]]*:/,/}/p' |
		grep -o '"sha"[[:space:]]*:[[:space:]]*"[^"]*"' | head -1 | cut -d '"' -f4
}

json_number() {
	printf '%s' "$1" | grep -o "\"$2\"[[:space:]]*:[[:space:]]*[0-9]*" | head -1 | grep -o '[0-9]*$'
}

tags=$(api_get "$api/repos/$repo/git/refs/tags" || true)
if [ -z "$tags" ]; then
	printf 'unknown\n'
	exit 0
fi

short=$(printf '%s' "$commit" | cut -c1-7)
best=""
best_distance=""
for ref in $(printf '%s' "$tags" | grep -o '"ref"[[:space:]]*:[[:space:]]*"refs/tags/[^"]*"' | cut -d '"' -f4); do
	name=${ref#refs/tags/}
	ref_json=$(api_get "$api/repos/$repo/git/ref/tags/$name" || true)
	[ -n "$ref_json" ] || continue
	target=$(json_string "$ref_json" sha)
	[ -n "$target" ] || continue
	# An annotated tag points at a tag object, which points at the commit.
	if [ "$(json_string "$ref_json" type)" = "tag" ]; then
		tag_json=$(api_get "$api/repos/$repo/git/tags/$target" || true)
		[ -n "$tag_json" ] || continue
		target=$(json_nested_object_sha "$tag_json")
		[ -n "$target" ] || continue
	fi

	compare=$(api_get "$api/repos/$repo/compare/$target...$commit" || true)
	[ -n "$compare" ] || continue
	status=$(json_string "$compare" status)
	case "$status" in
	identical) distance=0 ;;
	ahead) distance=$(json_number "$compare" ahead_by) ;;
	*) continue ;;
	esac
	[ -n "$distance" ] || continue

	if [ -z "$best_distance" ] || [ "$distance" -lt "$best_distance" ]; then
		best=$name
		best_distance=$distance
	fi
done

if [ -n "$best" ]; then
	if [ "$best_distance" -eq 0 ]; then
		printf '%s\n' "$best"
	else
		printf '%s-%s-g%s\n' "$best" "$best_distance" "$short"
	fi
	exit 0
fi

# A tagless repository: the sha is the most this can honestly claim.
printf '%s\n' "$short"
