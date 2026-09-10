package kiro

import (
	"testing"
)

func TestParseKiroCallbackURL(t *testing.T) {
	cases := []struct {
		name      string
		input     string
		wantCode  string
		wantState string
		wantErr   string
	}{
		{
			name:      "full valid url",
			input:     "kiro://kiro.kiroAgent/authenticate-success?code=test-code-123&state=test-state-456",
			wantCode:  "test-code-123",
			wantState: "test-state-456",
		},
		{
			name:      "query string only",
			input:     "code=alpha&state=beta",
			wantCode:  "alpha",
			wantState: "beta",
		},
		{
			name:    "error param",
			input:   "kiro://kiro.kiroAgent/authenticate-success?error=access_denied",
			wantErr: "access_denied",
		},
		{
			name:  "empty input",
			input: "",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			code, state, errParam := ParseKiroCallbackURL(tc.input)
			if code != tc.wantCode {
				t.Errorf("code = %q, want %q", code, tc.wantCode)
			}
			if state != tc.wantState {
				t.Errorf("state = %q, want %q", state, tc.wantState)
			}
			if errParam != tc.wantErr {
				t.Errorf("errParam = %q, want %q", errParam, tc.wantErr)
			}
		})
	}
}
