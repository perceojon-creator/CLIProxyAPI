package copilot

import (
	"fmt"
	"strings"
)

// CredentialFileName returns the filename used to persist Copilot credentials.
// It uses the login username or email as a suffix to disambiguate accounts.
func CredentialFileName(ident string) string {
	ident = strings.TrimSpace(ident)
	if ident == "" {
		return "copilot.json"
	}
	safe := strings.Map(func(r rune) rune {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '-' || r == '_' {
			return r
		}
		return '-'
	}, ident)
	safe = strings.Trim(safe, "-")
	if safe == "" {
		return "copilot.json"
	}
	return fmt.Sprintf("copilot-%s.json", safe)
}
