package main

import "testing"

func TestVersionSupportsFeature(t *testing.T) {
	cases := []struct {
		version string
		want    bool
	}{
		{"v0.4.0", true},
		{"v0.4.1", true},
		{"v1.0.0", true},
		{"v0.3.9", false},
		{"v0.2.0", false},
		{"dev", true},
		{"", false},
		{"garbage", false},
	}
	for _, c := range cases {
		got := versionSupportsFeature(c.version, "shell")
		if got != c.want {
			t.Errorf("versionSupportsFeature(%q, shell) = %v, want %v", c.version, got, c.want)
		}
	}
}

func TestVersionSupportsFeature_UnknownFeatureAlwaysOK(t *testing.T) {
	if !versionSupportsFeature("v0.0.1", "no-such-feature") {
		t.Error("unknown feature should default to supported")
	}
}

func TestVersionSupportsFeature_KillSwitch(t *testing.T) {
	orig := featureFlags["shell"]
	defer func() { featureFlags["shell"] = orig }()

	disabled := orig
	disabled.Enabled = false
	featureFlags["shell"] = disabled

	if versionSupportsFeature("v9.9.9", "shell") {
		t.Error("disabled feature should be unsupported regardless of version")
	}
}

func TestMustLoadFeatureFlags_RejectsBadVersion(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Error("expected panic on invalid min_agent_version")
		}
	}()
	mustLoadFeatureFlags([]byte("features:\n  - name: x\n    min_agent_version: not-a-version\n    enabled: true\n"))
}
