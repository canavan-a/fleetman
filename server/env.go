package main

import (
	"fmt"
	"os"
)

// lookupEnv wraps os.LookupEnv for testability.
var lookupEnv = os.LookupEnv

// archSuffix maps runtime.GOARCH values to release binary suffixes.
// ARM devices all report "arm" regardless of v5/v6/v7 — defaults to armv7.
var archSuffix = map[string]string{
	"amd64":   "linux-amd64",
	"arm64":   "linux-arm64",
	"arm":     "linux-armv7",
	"386":     "linux-386",
	"mips64":  "linux-mips64",
	"mips64le": "linux-mips64le",
	"ppc64le": "linux-ppc64le",
	"s390x":   "linux-s390x",
}

const defaultReleaseBaseURL = "https://github.com/canavan-a/fleetman/releases/download"

// upgradeURL constructs the agent binary download URL for a given arch and version.
// Override the base URL with FLEET_RELEASE_BASE_URL (useful for self-hosted releases).
func upgradeURL(arch, version string) (string, error) {
	base, ok := lookupEnv("FLEET_RELEASE_BASE_URL")
	if !ok || base == "" {
		base = defaultReleaseBaseURL
	}
	suffix, ok := archSuffix[arch]
	if !ok {
		return "", fmt.Errorf("unsupported arch %q", arch)
	}
	return fmt.Sprintf("%s/%s/fleet-agent-%s", base, version, suffix), nil
}
