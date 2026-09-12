package flow

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
)

// ChromeProfile represents a user profile detected in Google Chrome.
type ChromeProfile struct {
	ProfileDir string `json:"profile_dir"`
	Name       string `json:"name"`
	Email      string `json:"email"`
}

// FindChromeExecutable locates the google chrome binary on the machine.
func FindChromeExecutable() string {
	if runtime.GOOS == "windows" {
		candidates := []string{
			filepath.Join(os.Getenv("ProgramFiles"), "Google", "Chrome", "Application", "chrome.exe"),
			filepath.Join(os.Getenv("ProgramFiles(x86)"), "Google", "Chrome", "Application", "chrome.exe"),
			filepath.Join(os.Getenv("LOCALAPPDATA"), "Google", "Chrome", "Application", "chrome.exe"),
		}
		for _, p := range candidates {
			if _, err := os.Stat(p); err == nil {
				return p
			}
		}
		if p, err := exec.LookPath("chrome.exe"); err == nil {
			return p
		}
	}
	return "chrome"
}

// GetChromeProfiles inspects Chrome's Local State and returns all profiles with an authenticated Google account.
func GetChromeProfiles() ([]ChromeProfile, error) {
	localAppData := os.Getenv("LOCALAPPDATA")
	if localAppData == "" {
		return nil, fmt.Errorf("LOCALAPPDATA environment variable is empty")
	}
	localStatePath := filepath.Join(localAppData, "Google", "Chrome", "User Data", "Local State")
	data, err := os.ReadFile(localStatePath)
	if err != nil {
		return nil, fmt.Errorf("reading chrome local state: %w", err)
	}
	var state struct {
		Profile struct {
			InfoCache map[string]struct {
				Name     string `json:"name"`
				UserName string `json:"user_name"`
			} `json:"info_cache"`
		} `json:"profile"`
	}
	if err = json.Unmarshal(data, &state); err != nil {
		return nil, fmt.Errorf("unmarshaling chrome local state: %w", err)
	}
	var profiles []ChromeProfile
	for dir, p := range state.Profile.InfoCache {
		email := strings.TrimSpace(p.UserName)
		if email != "" && strings.Contains(email, "@") {
			profiles = append(profiles, ChromeProfile{
				ProfileDir: dir,
				Name:       strings.TrimSpace(p.Name),
				Email:      email,
			})
		}
	}
	sort.Slice(profiles, func(i, j int) bool {
		return profiles[i].Email < profiles[j].Email
	})
	return profiles, nil
}

// OpenFlowInChromeProfile launches Chrome explicitly using the designated user profile directory at Google Flow.
func OpenFlowInChromeProfile(profileDir string) error {
	chromePath := FindChromeExecutable()
	args := []string{fmt.Sprintf("--profile-directory=%s", profileDir), FlowAppURL}
	cmd := exec.Command(chromePath, args...)
	return cmd.Start()
}
