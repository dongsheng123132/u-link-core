package redact

import "testing"

func TestString(t *testing.T) {
	cases := map[string]string{
		`curl --token abc123 https://x`:  "curl --token *** https://x",
		`password=hunter2 user=bob`:      "password=*** user=bob",
		`Authorization: Bearer eyJhbGci`: "Authorization: Bearer ***",
		`api_key: sk-live-123`:           "api_key: ***",
		`echo hello`:                     "echo hello",
	}
	for in, want := range cases {
		if got := String(in); got != want {
			t.Errorf("%q: want %q, got %q", in, want, got)
		}
	}
}
