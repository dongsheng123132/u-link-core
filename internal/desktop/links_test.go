package desktop

import "testing"

func TestRepoURLDefault(t *testing.T) {
	t.Setenv("ULINK_REPO_URL", "")
	if got := repoURL(); got != defaultRepoURL {
		t.Fatalf("repoURL() = %q, want %q", got, defaultRepoURL)
	}
	if got := issueURL(); got != defaultRepoURL+"/issues/new" {
		t.Fatalf("issueURL() = %q", got)
	}
	if got := prURL(); got != defaultRepoURL+"/pulls" {
		t.Fatalf("prURL() = %q", got)
	}
}

func TestRepoURLOverrideTrimsTrailingSlash(t *testing.T) {
	t.Setenv("ULINK_REPO_URL", "https://github.com/example/fork/")
	if got := repoURL(); got != "https://github.com/example/fork" {
		t.Fatalf("repoURL() = %q", got)
	}
	if got := issueURL(); got != "https://github.com/example/fork/issues/new" {
		t.Fatalf("issueURL() = %q", got)
	}
	if got := prURL(); got != "https://github.com/example/fork/pulls" {
		t.Fatalf("prURL() = %q", got)
	}
}
