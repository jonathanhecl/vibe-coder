#!/usr/bin/env bash
# Builds and validates all packages before tagging and publishing a GitHub release.
# Linux/macOS counterpart of release.ps1.
# Usage:
#   ./release.sh
#   ./release.sh v1.0.0
#   ./release.sh v1.0.0 --run-tests --yes
set -euo pipefail

VERSION=""
RUN_TESTS=0
ASSUME_YES=0
ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
API="https://api.github.com"

die() { printf '\n\033[31m[ERROR] %s\033[0m\n' "$*" >&2; exit 1; }
ok() { printf '\033[32m[OK] %s\033[0m\n' "$*"; }
step() { printf '\n\033[36m%s\033[0m\n' "$*"; }

while [ $# -gt 0 ]; do
    case "$1" in
        --run-tests) RUN_TESTS=1 ;;
        --skip-tests) RUN_TESTS=0 ;;
        --yes|-y) ASSUME_YES=1 ;;
        -h|--help) sed -n '2,7p' "$0" | sed 's#^# #'; exit 0 ;;
        -*) die "Unknown option: $1" ;;
        *)
            [ -z "$VERSION" ] || die "Unexpected argument: $1"
            VERSION="$1"
            ;;
    esac
    shift
done

[ -d "$ROOT/.git" ] || die "Run this script from the repository root."
cd "$ROOT"

for cmd in git curl jq unzip; do
    command -v "$cmd" >/dev/null 2>&1 || die "$cmd is not installed or is not available in PATH."
done

# gh_api METHOD URL [curl args...] -> sets HTTP_STATUS and RESP (body).
# Must not be called inside $(...): the globals would be lost in the subshell.
gh_api() {
    local method="$1" url="$2" out
    shift 2
    out="$(mktemp)"
    HTTP_STATUS="$(curl -sS -o "$out" -w '%{http_code}' -X "$method" \
        -H "Authorization: Bearer $TOKEN" \
        -H "Accept: application/vnd.github+json" \
        -H "X-GitHub-Api-Version: 2022-11-28" \
        "$@" "$url")" || { rm -f "$out"; die "Request to $url failed."; }
    RESP="$(cat "$out")"
    rm -f "$out"
}

api_message() { printf '%s' "$RESP" | jq -r '.message // empty' 2>/dev/null || true; }

# sha256_files DIR -> "HASH  NAME" lines for every *.zip in DIR, sorted.
sha256_files() {
    local dir="$1"
    ( cd "$dir" && \
        if command -v sha256sum >/dev/null 2>&1; then
            sha256sum ./*.zip
        elif command -v shasum >/dev/null 2>&1; then
            shasum -a 256 ./*.zip
        else
            for f in ./*.zip; do
                printf '%s  %s\n' "$(openssl dgst -sha256 -r "$f" | awk '{print $1}')" "$f"
            done
        fi ) | sed 's#\./##' | sort
}

assert_release_assets() {
    local dist="$1" v="$2" name binary path size entries count hash
    local expected="vibe_${v}_windows_amd64.zip vibe.exe
vibe_${v}_linux_amd64.zip vibe
vibe_${v}_linux_arm64.zip vibe
vibe_${v}_darwin_amd64.zip vibe
vibe_${v}_darwin_arm64.zip vibe"

    ASSETS=()
    while read -r name binary; do
        path="$dist/$name"
        [ -f "$path" ] || die "Expected build artifact is missing: $name"
        size="$(wc -c <"$path" | tr -d ' ')"
        [ "$size" -gt 0 ] || die "Build artifact is empty: $name"
        entries="$(unzip -Z1 "$path")" || die "Archive validation failed for $name."
        entries="$(printf '%s\n' "$entries" | sed -e 's#^\./##' -e 's#/$##')"
        printf '%s\n' "$entries" | grep -qx "$binary" \
            || die "Archive '$name' does not contain $binary at its root."
        hash="$(grep -E "[[:space:]]${name//./\\.}$" "$dist/checksums.txt" 2>/dev/null | awk '{print $1}' | head -n 1)"
        [ -n "$hash" ] || die "checksums.txt has no entry for $name."
        ASSETS+=("$path")
        ok "$name ($size bytes)"
    done <<EOF
$expected
EOF

    local actual
    actual="$(sha256_files "$dist")"
    if [ "$actual" != "$(sort "$dist/checksums.txt")" ]; then
        die "dist/checksums.txt does not match the generated archives."
    fi
    ok "checksums.txt matches all archives."

    count="$(find "$dist" -mindepth 1 -maxdepth 1 -type f | wc -l | tr -d ' ')"
    if [ "$count" -ne "$(( ${#ASSETS[@]} + 1 ))" ]; then
        die "dist contains unexpected or missing files. Expected $(( ${#ASSETS[@]} + 1 )), found $count: $(ls "$dist" | tr '\n' ' ')"
    fi
}

echo "Fetching tags from origin..."
git fetch origin --tags --prune --force >/dev/null 2>&1 || die "git fetch origin --tags failed."

LATEST_TAG="$(git tag --sort=-version:refname | head -n 1)"
echo "Latest tag: ${LATEST_TAG:-none}"

if [ -z "$VERSION" ]; then
    read -r -p "Version to release (format vX.Y.Z): " VERSION
    VERSION="$(printf '%s' "$VERSION" | tr -d '[:space:]')"
fi
printf '%s' "$VERSION" | grep -Eq '^v[0-9]+\.[0-9]+\.[0-9]+(-[a-zA-Z0-9.]+)?$' \
    || die "Version must use the format vX.Y.Z (for example, v1.0.0)."

STATUS="$(git status --porcelain)" || die "Could not read git status."
if [ -n "$STATUS" ]; then
    printf '\033[31m[ERROR] The repository has uncommitted changes:\033[0m\n%s\n' "$STATUS" >&2
    die "Commit or stash local changes before releasing."
fi

BRANCH="$(git branch --show-current)"
[ -n "$BRANCH" ] || die "Could not determine the current branch. Detached HEAD is not supported."

REMOTE_URL="$(git remote get-url origin)" || die "Could not read the origin remote URL."
OWNER_REPO="$(printf '%s' "$REMOTE_URL" | sed -nE 's#.*github\.com[:/]([^/]+)/([^/]+)$#\1/\2#p' | sed -E 's#\.git$##')"
[ -n "$OWNER_REPO" ] || die "Could not determine the GitHub owner and repository from origin: $REMOTE_URL"

TOKEN="${GITHUB_TOKEN:-${GH_TOKEN:-}}"
if [ -z "$TOKEN" ] && command -v gh >/dev/null 2>&1; then
    TOKEN="$(gh auth token 2>/dev/null || true)"
fi
if [ -z "$TOKEN" ]; then
    echo "GITHUB_TOKEN was not found."
    read -r -s -p "GitHub token with repository release permissions: " TOKEN
    echo
    [ -n "$TOKEN" ] || die "A GitHub token is required."
fi

LOCAL_TAG_EXISTS=false
[ -n "$(git tag -l "$VERSION")" ] && LOCAL_TAG_EXISTS=true

REMOTE_TAG_EXISTS=false
set +e
git ls-remote --exit-code --tags origin "refs/tags/$VERSION" >/dev/null 2>&1
rc=$?
set -e
case $rc in
    0) REMOTE_TAG_EXISTS=true ;;
    2) ;;
    *) die "Could not check remote tag '$VERSION'. git ls-remote exited with code $rc." ;;
esac

gh_api GET "$API/repos/$OWNER_REPO/releases/tags/$(jq -rn --arg t "$VERSION" '$t|@uri')"
EXISTING_RELEASE_ID=""
case "$HTTP_STATUS" in
    200) EXISTING_RELEASE_ID="$(printf '%s' "$RESP" | jq -r '.id')" ;;
    404) ;;
    *) die "GitHub API returned HTTP $HTTP_STATUS while checking release '$VERSION': $(api_message)" ;;
esac

REPLACING=false
if $LOCAL_TAG_EXISTS || $REMOTE_TAG_EXISTS || [ -n "$EXISTING_RELEASE_ID" ]; then
    REPLACING=true
fi

echo ""
echo "============================================="
echo "              RELEASE SUMMARY                "
echo "============================================="
echo "Latest tag:     ${LATEST_TAG:-none}"
echo "New version:    $VERSION"
echo "Repository:     $OWNER_REPO"
echo "Source branch:  $BRANCH"
echo "Run tests:      $([ "$RUN_TESTS" -eq 1 ] && echo yes || echo no)"
echo "Replace mode:   $REPLACING"
echo "============================================="
if $REPLACING; then
    printf '\033[33mAn existing tag or release will be deleted only after all builds pass validation.\033[0m\n'
fi

if [ "$ASSUME_YES" -ne 1 ]; then
    read -r -p "Continue? (y/n) " CONFIRM
    CONFIRM="$(printf '%s' "$CONFIRM" | tr '[:upper:]' '[:lower:]' | tr -d '[:space:]')"
    if [ "$CONFIRM" != "y" ] && [ "$CONFIRM" != "yes" ]; then
        echo "Release cancelled."
        exit 0
    fi
fi

step "[1/5] Building all release packages..."
[ -f "$ROOT/build-all.sh" ] || die "Build script was not found: $ROOT/build-all.sh"
if [ "$RUN_TESTS" -eq 1 ]; then
    echo "Running the test suite first..."
    go test -timeout 10m ./... || die "Tests failed. No tag or release was modified."
    ok "Tests passed."
fi
bash "$ROOT/build-all.sh" "$VERSION" || die "The multiplatform build failed. No tag or release was modified."

step "[2/5] Validating release packages..."
assert_release_assets "$ROOT/dist" "$VERSION"
ok "All ${#ASSETS[@]} release packages passed validation."

step "[3/5] Pushing source branch..."
git push origin "$BRANCH" || die "git push origin $BRANCH failed."

if $REPLACING; then
    step "Replacing existing release/tag '$VERSION'..."
    if [ -n "$EXISTING_RELEASE_ID" ]; then
        gh_api DELETE "$API/repos/$OWNER_REPO/releases/$EXISTING_RELEASE_ID"
        [ "$HTTP_STATUS" = "204" ] || die "Could not delete the existing GitHub release (HTTP $HTTP_STATUS): $(api_message)"
        ok "Existing GitHub release deleted."
    fi
    if $REMOTE_TAG_EXISTS; then
        git push origin --delete "refs/tags/$VERSION" || die "Could not delete the remote tag."
        ok "Existing remote tag deleted."
    fi
    if $LOCAL_TAG_EXISTS; then
        git tag -d "$VERSION" >/dev/null || die "Could not delete the local tag."
        ok "Existing local tag deleted."
    fi
fi

step "[4/5] Creating and pushing tag '$VERSION'..."
git tag "$VERSION" || die "git tag $VERSION failed."
if ! git push origin "refs/tags/$VERSION"; then
    git tag -d "$VERSION" >/dev/null 2>&1 || true
    die "git push of tag $VERSION failed."
fi

step "[5/5] Creating GitHub release and uploading validated assets..."
BODY="$(jq -n --arg tag "$VERSION" --arg branch "$BRANCH" '{
    tag_name: $tag,
    target_commitish: $branch,
    name: ("Release " + $tag),
    body: ("Release " + $tag + " generated by release.sh after local build validation."),
    draft: false,
    prerelease: false,
    generate_release_notes: true
}')"
gh_api POST "$API/repos/$OWNER_REPO/releases" -H "Content-Type: application/json; charset=utf-8" --data "$BODY"
[ "$HTTP_STATUS" = "201" ] || die "Could not create the GitHub release (HTTP $HTTP_STATUS): $(api_message)"
UPLOAD_BASE="$(printf '%s' "$RESP" | jq -r '.upload_url' | sed -E 's#\{.*\}##')"
HTML_URL="$(printf '%s' "$RESP" | jq -r '.html_url')"

ASSETS+=("$ROOT/dist/checksums.txt")
for asset in "${ASSETS[@]}"; do
    name="$(basename "$asset")"
    echo "Uploading $name..."
    gh_api POST "$UPLOAD_BASE?name=$(jq -rn --arg n "$name" '$n|@uri')" \
        -H "Content-Type: application/octet-stream" --data-binary "@$asset"
    [ "$HTTP_STATUS" = "201" ] || die "Upload of $name failed (HTTP $HTTP_STATUS): $(api_message)"
    ok "Uploaded $name"
done

echo "Verifying published assets..."
gh_api GET "$API/repos/$OWNER_REPO/releases/tags/$(jq -rn --arg t "$VERSION" '$t|@uri')"
[ "$HTTP_STATUS" = "200" ] || die "Could not verify the published release (HTTP $HTTP_STATUS): $(api_message)"
PUBLISHED="$(printf '%s' "$RESP" | jq -r '.assets[].name' | sort)"
EXPECTED="$(for asset in "${ASSETS[@]}"; do basename "$asset"; done | sort)"
[ "$PUBLISHED" = "$EXPECTED" ] || die "Published assets do not match the build output. Published: $(printf '%s' "$PUBLISHED" | tr '\n' ' ')"
ok "All ${#ASSETS[@]} assets are published."

printf '\n'
ok "Release completed: $HTML_URL"
