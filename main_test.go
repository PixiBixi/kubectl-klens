package main

import "testing"

// TestBuildVarsDefault guards against accidental removal of the ldflags vars.
func TestBuildVarsDefault(t *testing.T) {
	if version == "" || commit == "" || date == "" {
		t.Fatal("build vars must have non-empty defaults")
	}
}
