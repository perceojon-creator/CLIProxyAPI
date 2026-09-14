package flow

import (
	"testing"
)

func TestExtractCookiesFromInput_CurlHeader(t *testing.T) {
	input := `curl 'https://flow.google.com/_/AiSandboxAngularFrontend/data/batchexecute' -H 'Cookie: SID=test_sid; HSID=test_hsid' --data-raw 'f.req=123&at=AIQ-test%3A789'`
	cookies, atToken := ExtractCookiesFromInput(input)

	if cookies != "SID=test_sid; HSID=test_hsid" {
		t.Fatalf("unexpected cookies: %s", cookies)
	}
	if atToken != "AIQ-test:789" {
		t.Fatalf("unexpected atToken: %s", atToken)
	}
}

func TestExtractCookiesFromInput_CurlCookieFlag(t *testing.T) {
	input := `curl 'https://flow.google.com/' -b 'SID=flag_sid; SAPISID=flag_sapisid' -d 'at=AIQ-direct_token'`
	cookies, atToken := ExtractCookiesFromInput(input)

	if cookies != "SID=flag_sid; SAPISID=flag_sapisid" {
		t.Fatalf("unexpected cookies: %s", cookies)
	}
	if atToken != "AIQ-direct_token" {
		t.Fatalf("unexpected atToken: %s", atToken)
	}
}

func TestExtractCookiesFromInput_RawCookieHeader(t *testing.T) {
	input := "Cookie: SID=raw_sid; __Secure-1PSID=raw_sec"
	cookies, atToken := ExtractCookiesFromInput(input)

	if cookies != "SID=raw_sid; __Secure-1PSID=raw_sec" {
		t.Fatalf("unexpected cookies: %s", cookies)
	}
	if atToken != "" {
		t.Fatalf("expected empty atToken, got: %s", atToken)
	}
}
