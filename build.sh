#!/bin/sh
# Builds MiniMedia on macOS or Linux: dist/minimedia (the server, for this
# computer) and dist/MiniMedia-tv.apk (the Google TV app, which needs
# Android Studio's JDK and SDK). See README.md.
#
#   ./build.sh             server and app
#   ./build.sh --skip-app  just the server
set -eu
repo=$(cd "$(dirname "$0")" && pwd)
mkdir -p "$repo/dist"
ver=$(git -C "$repo" describe --tags --always --dirty 2>/dev/null || echo dev)

(cd "$repo/server" && CGO_ENABLED=0 go build -trimpath \
    -ldflags "-s -w -X github.com/acip/minimedia/server/internal/version.Version=$ver" \
    -o "$repo/dist/minimedia" ./cmd/minimedia)
echo "Built $repo/dist/minimedia ($ver)"

if [ "${1:-}" != "--skip-app" ]; then
    (cd "$repo/tv-app" && sh ./gradlew -q assembleRelease)
    cp "$repo/tv-app/app/build/outputs/apk/release/app-release.apk" "$repo/dist/MiniMedia-tv.apk"
    echo "Built $repo/dist/MiniMedia-tv.apk"
fi
