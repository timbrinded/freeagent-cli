package main

import "testing"

func TestBuildVersionPrefersLinkedRelease(t *testing.T) {
	if got := buildVersion("v0.1.0"); got != "0.1.0" {
		t.Fatalf("buildVersion() = %q", got)
	}
}

func TestBuildVersionKeepsUnprefixedRelease(t *testing.T) {
	if got := buildVersion("0.1.0"); got != "0.1.0" {
		t.Fatalf("buildVersion() = %q", got)
	}
}
