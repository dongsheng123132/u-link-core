package ulink

import "testing"

func TestExitCodeForCode(t *testing.T) {
	cases := map[string]int{
		"":                 ExitOK,
		"usage":            ExitUsage,
		"device_not_found": ExitOffline,
		"connect_failed":   ExitOffline,
		"auth":             ExitAuth,
		"timeout":          ExitTimeout,
		"remote_failed":    ExitRemote,
		"something_else":   ExitGeneral,
	}
	for code, want := range cases {
		if got := ExitCodeForCode(code); got != want {
			t.Errorf("%q: want %d, got %d", code, want, got)
		}
	}
}

func TestClassifyError(t *testing.T) {
	cases := []struct {
		msg  string
		want string
	}{
		{"device not found: pc-1", "device_not_found"},
		{"auth failed: invalid token", "auth"},
		{"read: i/o timeout", "timeout"},
		{"connect failed: connection refused", "connect_failed"},
		{"weird failure", "failed"},
	}
	for _, c := range cases {
		e := ClassifyError(errString(c.msg))
		if e.Code != c.want {
			t.Errorf("%q: want %s, got %s", c.msg, c.want, e.Code)
		}
	}
}

type errString string

func (e errString) Error() string { return string(e) }
