#!/usr/bin/env bash
set -euo pipefail

# release.sh - build cross-platform archives, tag, push, and publish a GitHub Release.
# Usage: ./release.sh v1.0.5 [--skip-tests] [--yes]

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
cd "$SCRIPT_DIR"

usage() {
  cat <<'EOF'
Usage: ./release.sh <vX.Y.Z> [--skip-tests] [--yes]

Builds cross-platform binaries, tags the release, pushes to origin, and creates
a GitHub Release with the archives and checksums.txt as assets.

Options:
  --skip-tests   Do not run the test suite before building.
  --yes, -y      Skip the interactive confirmation prompt.
  -h, --help     Show this help.

Authentication:
  Set GITHUB_TOKEN (or GH_TOKEN) with 'repo' scope, or have an authenticated
  `gh` CLI available. Otherwise the script prompts for a token.
EOF
}

VERSION=""
SKIP_TESTS=0
ASSUME_YES=0

while [[ $# -gt 0 ]]; do
  case "$1" in
    --skip-tests) SKIP_TESTS=1; shift ;;
    --yes|-y) ASSUME_YES=1; shift ;;
    -h|--help) usage; exit 0 ;;
    -*)
      echo "[ERROR] Unknown option: $1" >&2
      usage >&2
      exit 1
      ;;
    *)
      if [[ -z "$VERSION" ]]; then
        VERSION="$1"
      else
        echo "[ERROR] Unexpected argument: $1" >&2
        exit 1
      fi
      shift
      ;;
  esac
done

if [[ -z "$VERSION" ]]; then
  echo "[ERROR] A version is required (e.g. v1.0.5)." >&2
  usage >&2
  exit 1
fi

if [[ ! "$VERSION" =~ ^v[0-9]+\.[0-9]+\.[0-9]+(-[A-Za-z0-9.]+)?$ ]]; then
  echo "[ERROR] Version must be in the format vX.Y.Z (e.g. v1.0.5)." >&2
  exit 1
fi

command -v go >/dev/null 2>&1 || { echo "[ERROR] go is required but was not found in PATH." >&2; exit 1; }
command -v git >/dev/null 2>&1 || { echo "[ERROR] git is required but was not found in PATH." >&2; exit 1; }
command -v curl >/dev/null 2>&1 || { echo "[ERROR] curl is required but was not found in PATH." >&2; exit 1; }

if [[ ! -d .git ]]; then
  echo "[ERROR] This script must be run from the root of a Git repository." >&2
  exit 1
fi

status="$(git status --porcelain)"
if [[ -n "$status" ]]; then
  echo "[ERROR] There are uncommitted changes in the repository:" >&2
  printf '%s\n' "$status" >&2
  echo "[ERROR] Please commit or stash your changes before releasing." >&2
  exit 1
fi

branch="$(git branch --show-current)"
if [[ -z "$branch" ]]; then
  echo "[ERROR] Could not determine the current branch (are you in detached HEAD?)." >&2
  exit 1
fi
if [[ "$branch" != "main" ]]; then
  echo "[WARN] Current branch is '$branch', not 'main'. Make sure you intend to release from this branch." >&2
fi

if git rev-parse -q --verify "refs/tags/$VERSION" >/dev/null; then
  echo "[ERROR] Tag '$VERSION' already exists locally." >&2
  exit 1
fi

remote_url="$(git remote get-url origin)"
if [[ "$remote_url" =~ github\.com[:/]([^/]+)/([^/.]+)(\.git)?$ ]]; then
  owner="${BASH_REMATCH[1]}"
  repo="${BASH_REMATCH[2]}"
else
  echo "[ERROR] Could not determine GitHub owner/repo from origin URL: $remote_url" >&2
  exit 1
fi

token="${GITHUB_TOKEN:-${GH_TOKEN:-}}"
if [[ -z "$token" ]] && command -v gh >/dev/null 2>&1; then
  token="$(gh auth token 2>/dev/null || true)"
fi
if [[ -z "$token" ]]; then
  echo "[WARN] GITHUB_TOKEN is not set in the environment." >&2
  echo "[INFO] Enter a GitHub token with 'repo' scope to create the release:" >&2
  read -r -s token || true
  echo
  if [[ -z "$token" ]]; then
    echo "[ERROR] A GitHub token is required to create a release." >&2
    exit 1
  fi
fi

# Package a single binary from its staging directory into an archive.
make_zip() {
  local stage_dir="$1" asset_path="$2" bin_name="$3"
  if command -v zip >/dev/null 2>&1; then
    ( cd "$stage_dir" && zip -q -X "$asset_path" "$bin_name" )
  elif command -v 7z >/dev/null 2>&1; then
    ( cd "$stage_dir" && 7z a -tzip "$asset_path" "$bin_name" >/dev/null )
  elif command -v bsdtar >/dev/null 2>&1; then
    ( cd "$stage_dir" && bsdtar -a -cf "$asset_path" "$bin_name" )
  elif command -v python3 >/dev/null 2>&1; then
    python3 - "$stage_dir" "$asset_path" "$bin_name" <<'PY'
import sys, zipfile
stage, dest, name = sys.argv[1], sys.argv[2], sys.argv[3]
with zipfile.ZipFile(dest, "w", zipfile.ZIP_DEFLATED) as archive:
    archive.write(f"{stage}/{name}", name)
PY
  else
    echo "[ERROR] No zip tool found (install 'zip', 7z, bsdtar, or python3)." >&2
    exit 1
  fi
}

echo "============================================="
echo "   PREPARING RELEASE"
echo "============================================="
echo "Version:   $VERSION"
echo "Repo:      $owner/$repo"
echo "Branch:    $branch"
echo "============================================="
echo

if [[ "$ASSUME_YES" -ne 1 ]]; then
  read -r -p "Continue with release build and GitHub upload? (y/n) " reply
  if [[ ! "$reply" =~ ^[Yy]([Ee][Ss])?$ ]]; then
    echo "[INFO] Release cancelled."
    exit 0
  fi
fi

if [[ "$SKIP_TESTS" -ne 1 ]]; then
  echo "[1/5] Running tests..."
  go test -timeout 10m ./...
  echo "[OK] Tests passed."
else
  echo "[1/5] Skipping tests as requested."
fi

dist_dir="$SCRIPT_DIR/dist"
rm -rf "$dist_dir"
mkdir -p "$dist_dir"

echo "[2/5] Compiling cross-platform binaries..."

targets=("windows/amd64" "linux/amd64" "linux/arm64" "darwin/amd64" "darwin/arm64")
ldflags="-s -w -X github.com/jonathanhecl/vibe-coder/internal/version.Value=$VERSION"

for target in "${targets[@]}"; do
  goos="${target%%/*}"
  goarch="${target##*/}"
  ext=""
  [[ "$goos" == "windows" ]] && ext=".exe"
  bin_name="vibe${ext}"
  asset_name="vibe_${VERSION}_${goos}_${goarch}.zip"
  stage_dir="$dist_dir/build_${goos}_${goarch}"

  mkdir -p "$stage_dir"
  echo "[INFO] Building $goos/$goarch..."
  GOOS="$goos" GOARCH="$goarch" CGO_ENABLED=0 \
    go build -ldflags "$ldflags" -o "$stage_dir/$bin_name" ./cmd/vibe
  make_zip "$stage_dir" "$dist_dir/$asset_name" "$bin_name"
  rm -rf "$stage_dir"
  echo "[OK] Built $asset_name"
done

( cd "$dist_dir" && sha256sum *.zip > checksums.txt )
echo "[OK] Wrote dist/checksums.txt"

echo "[3/5] Creating tag and pushing to origin..."
git tag -a "$VERSION" -m "Release $VERSION"
if ! git push origin "$branch"; then
  echo "[ERROR] Failed to push branch '$branch' to origin." >&2
  git tag -d "$VERSION" >/dev/null 2>&1 || true
  exit 1
fi
if ! git push origin "$VERSION"; then
  echo "[ERROR] Failed to push tag '$VERSION' to origin." >&2
  exit 1
fi
echo "[OK] Tag pushed to origin."

echo "[4/5] Creating GitHub release..."
release_url="https://api.github.com/repos/$owner/$repo/releases"
release_body="$(cat <<JSON
{
  "tag_name": "$VERSION",
  "target_commitish": "$branch",
  "name": "Release $VERSION",
  "body": "Release $VERSION of vibe.",
  "draft": false,
  "prerelease": false,
  "generate_release_notes": true
}
JSON
)"

response_file="$(mktemp)"
http_code="$(curl -sS -o "$response_file" -w '%{http_code}' \
  -X POST "$release_url" \
  -H "Authorization: Bearer $token" \
  -H "Accept: application/vnd.github+json" \
  -H "X-GitHub-Api-Version: 2022-11-28" \
  -H "Content-Type: application/json" \
  -d "$release_body")"
response="$(cat "$response_file")"
rm -f "$response_file"

if [[ "$http_code" != "201" ]]; then
  echo "[ERROR] Failed to create GitHub release (HTTP $http_code):" >&2
  echo "$response" >&2
  echo "[INFO] To retry, delete the remote tag with: git push origin --delete $VERSION" >&2
  exit 1
fi

flat="$(printf '%s' "$response" | tr -d '\n')"
upload_url="$(printf '%s' "$flat" | sed -n 's/.*"upload_url": *"\([^"]*\)".*/\1/p')"
html_url="$(printf '%s' "$flat" | sed -n 's/.*"html_url": *"\([^"]*\)".*/\1/p')"
upload_url="${upload_url%%\{*}"
echo "[OK] Created GitHub release: $html_url"

echo "[5/5] Uploading release assets..."
shopt -s nullglob
assets=("$dist_dir"/*.zip "$dist_dir"/checksums.txt)
shopt -u nullglob
if [[ ${#assets[@]} -eq 0 ]]; then
  echo "[ERROR] No assets found in dist/ to upload." >&2
  exit 1
fi

failures=0
for asset in "${assets[@]}"; do
  file_name="$(basename "$asset")"
  echo "[INFO] Uploading $file_name..."
  upload_response="$(mktemp)"
  upload_code="$(curl -sS -o "$upload_response" -w '%{http_code}' \
    -X POST "${upload_url}?name=${file_name}" \
    -H "Authorization: Bearer $token" \
    -H "Accept: application/vnd.github+json" \
    -H "X-GitHub-Api-Version: 2022-11-28" \
    -H "Content-Type: application/octet-stream" \
    --data-binary @"$asset")"
  if [[ "$upload_code" == "201" ]]; then
    echo "[OK] Uploaded $file_name"
  else
    echo "[ERROR] Failed to upload $file_name (HTTP $upload_code):" >&2
    cat "$upload_response" >&2
    echo >&2
    failures=$((failures + 1))
  fi
  rm -f "$upload_response"
done

if [[ "$failures" -ne 0 ]]; then
  echo "[ERROR] $failures asset(s) failed to upload. Re-run the upload or add them manually at $html_url" >&2
  exit 1
fi

echo
echo "[OK] Release process completed."
echo "[INFO] Release available at: $html_url"
