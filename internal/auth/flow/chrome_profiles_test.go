package flow

import (
	"os"
	"path/filepath"
	"testing"
)

func TestFindChromeExecutable(t *testing.T) {
	exe := FindChromeExecutable()
	if exe == "" {
		t.Fatal("expected non-empty chrome executable path")
	}
}

func TestGetChromeProfiles_Mock(t *testing.T) {
	tempDir := t.TempDir()
	chromeUserData := filepath.Join(tempDir, "Google", "Chrome", "User Data")
	if err := os.MkdirAll(chromeUserData, 0700); err != nil {
		t.Fatal(err)
	}

	mockState := `{
		"profile": {
			"info_cache": {
				"Default": {
					"name": "Personal",
					"user_name": "user1@gmail.com"
				},
				"Profile 1": {
					"name": "Work",
					"user_name": "user2@gmail.com"
				},
				"Profile 2": {
					"name": "NoEmail",
					"user_name": ""
				}
			}
		}
	}`
	if err := os.WriteFile(filepath.Join(chromeUserData, "Local State"), []byte(mockState), 0600); err != nil {
		t.Fatal(err)
	}

	t.Setenv("LOCALAPPDATA", tempDir)

	profiles, err := GetChromeProfiles()
	if err != nil {
		t.Fatalf("GetChromeProfiles failed: %v", err)
	}
	if len(profiles) != 2 {
		t.Fatalf("expected 2 profiles with emails, got %d", len(profiles))
	}
	if profiles[0].Email != "user1@gmail.com" || profiles[1].Email != "user2@gmail.com" {
		t.Fatalf("unexpected profile emails: %#v", profiles)
	}
}
