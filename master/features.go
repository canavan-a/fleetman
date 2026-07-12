package main

import (
	_ "embed"
	"fmt"
	"strconv"
	"strings"

	"github.com/canavan-a/fleetman/internal/api"
	"gopkg.in/yaml.v3"
)

//go:embed features.yaml
var featuresYAML []byte

// featureFlag gates one master-initiated capability by the minimum agent
// release that understands it, plus a fleet-wide kill switch.
type featureFlag struct {
	Name            string `yaml:"name"`
	Description     string `yaml:"description"`
	MinAgentVersion string `yaml:"min_agent_version"`
	Enabled         bool   `yaml:"enabled"`
}

var featureFlags = mustLoadFeatureFlags(featuresYAML)

func mustLoadFeatureFlags(data []byte) map[string]featureFlag {
	var doc struct {
		Features []featureFlag `yaml:"features"`
	}
	if err := yaml.Unmarshal(data, &doc); err != nil {
		panic(fmt.Sprintf("master: parse embedded features.yaml: %v", err))
	}
	out := make(map[string]featureFlag, len(doc.Features))
	for _, f := range doc.Features {
		if _, ok := parseSemver(f.MinAgentVersion); !ok {
			panic(fmt.Sprintf("master: features.yaml: feature %q has invalid min_agent_version %q", f.Name, f.MinAgentVersion))
		}
		out[f.Name] = f
	}
	return out
}

// versionSupportsFeature reports whether an agent-reported version string
// meets the minimum required for feature. Agent versions are semver tags
// (e.g. "v0.3.9") injected at build time; "dev" (unset ldflags, i.e. a
// locally built agent) is always treated as supported so development
// builds aren't blocked. An empty version means the agent predates version
// reporting entirely, so it's treated as unsupported.
func versionSupportsFeature(version, feature string) bool {
	f, ok := featureFlags[feature]
	if !ok {
		return true
	}
	if !f.Enabled {
		return false
	}
	if version == "dev" {
		return true
	}
	if version == "" {
		return false
	}
	cmp, ok := compareSemver(version, f.MinAgentVersion)
	if !ok {
		// Unparseable version string — fail closed rather than risk
		// sending a command an unknown agent build can't handle.
		return false
	}
	return cmp >= 0
}

// deviceSupportsFeature checks a device against a named feature and, if
// unsupported, returns a human-readable reason suitable for m.err.
func deviceSupportsFeature(d api.Device, feature string) (bool, string) {
	if versionSupportsFeature(d.Version, feature) {
		return true, ""
	}
	f := featureFlags[feature]
	if !f.Enabled {
		return false, fmt.Sprintf("%s is currently disabled", feature)
	}
	shown := d.Version
	if shown == "" {
		shown = "unknown"
	}
	return false, fmt.Sprintf("%s requires agent %s+ (device is on %s)", feature, f.MinAgentVersion, shown)
}

// compareSemver compares two "vMAJOR.MINOR.PATCH" strings, returning -1, 0,
// or 1 the way strings.Compare does. ok is false if either string doesn't
// parse as semver.
func compareSemver(a, b string) (int, bool) {
	pa, ok := parseSemver(a)
	if !ok {
		return 0, false
	}
	pb, ok := parseSemver(b)
	if !ok {
		return 0, false
	}
	for i := 0; i < 3; i++ {
		if pa[i] != pb[i] {
			if pa[i] < pb[i] {
				return -1, true
			}
			return 1, true
		}
	}
	return 0, true
}

func parseSemver(v string) ([3]int, bool) {
	var out [3]int
	v = strings.TrimPrefix(v, "v")
	parts := strings.SplitN(v, ".", 3)
	if len(parts) != 3 {
		return out, false
	}
	for i, p := range parts {
		n, err := strconv.Atoi(p)
		if err != nil {
			return out, false
		}
		out[i] = n
	}
	return out, true
}
