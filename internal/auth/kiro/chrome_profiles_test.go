package kiro

import (
	"testing"
)

func TestGetChromeProfiles(t *testing.T) {
	profiles, err := GetChromeProfiles()
	if err != nil {
		t.Fatalf("GetChromeProfiles failed: %v", err)
	}
	if len(profiles) == 0 {
		t.Fatalf("expected Chrome profiles, got 0")
	}
	t.Logf("Successfully detected %d Chrome profiles", len(profiles))
	for i, p := range profiles {
		if p.Email == "" || p.ProfileDir == "" {
			t.Errorf("Profile %d has empty field: %+v", i, p)
		}
		t.Logf("Profile %d: %s (%s) [%s]", i+1, p.Email, p.Name, p.ProfileDir)
	}
}
