#!/usr/bin/env bash
# Recomputes vendorHash in flake.nix after go.mod/go.sum changes.
set -euo pipefail

cd "$(dirname "$0")/.."

FLAKE=flake.nix
FAKE_HASH="sha256-AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA="

current_hash=$(grep -o 'vendorHash = "[^"]*"' "$FLAKE" | head -1 | sed -E 's/vendorHash = "(.*)"/\1/')
if [ -z "$current_hash" ]; then
  echo "Could not find a vendorHash = \"...\"; line in $FLAKE" >&2
  exit 1
fi

sed -i "s|vendorHash = \"$current_hash\"|vendorHash = \"$FAKE_HASH\"|" "$FLAKE"

echo "Building with fake hash to discover the real one..."
build_output=$(nix build .#fleetman --no-link 2>&1 || true)

new_hash=$(echo "$build_output" | grep -oP 'got:\s+\Ksha256-\S+' | head -1)

if [ -z "$new_hash" ]; then
  echo "Failed to extract new hash. Restoring original hash. Build output:" >&2
  echo "$build_output" >&2
  sed -i "s|vendorHash = \"$FAKE_HASH\"|vendorHash = \"$current_hash\"|" "$FLAKE"
  exit 1
fi

sed -i "s|vendorHash = \"$FAKE_HASH\"|vendorHash = \"$new_hash\"|" "$FLAKE"

echo "Updated vendorHash: $current_hash -> $new_hash"
echo "Verifying build..."
nix build .#fleetman --no-link
nix build .#fleetman-server --no-link
echo "Done."
