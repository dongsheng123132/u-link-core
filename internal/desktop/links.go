package desktop

import (
	"log"
	"os"
	"strings"
)

// defaultRepoURL is the public project page used by the tray and dashboard
// feedback links.  Issues and pull requests for the shared core are accepted
// there; private/enterprise builds can point somewhere else.
const defaultRepoURL = "https://github.com/dongsheng123132/u-link-core"

// repoURL returns the project page, honouring ULINK_REPO_URL for forks or
// private deployments.
func repoURL() string {
	if v := strings.TrimSpace(os.Getenv("ULINK_REPO_URL")); v != "" {
		return strings.TrimRight(v, "/")
	}
	return defaultRepoURL
}

// issueURL is where users report a problem.
func issueURL() string { return repoURL() + "/issues/new" }

// prURL is where users submit a contribution.
func prURL() string { return repoURL() + "/pulls" }

// openFeedback opens a project page in the user's browser.  Failures are
// logged rather than surfaced in the tray: the menu action already happened,
// and a missing default browser is not something the operator can fix here.
func openFeedback(url string) {
	if err := openBrowser(url); err != nil {
		log.Printf("打开浏览器失败（%s）: %v", url, err)
	}
}
