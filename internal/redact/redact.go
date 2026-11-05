// Package redact removes common secret shapes from logs and audit lines.
package redact

import "regexp"

var patterns = []*regexp.Regexp{
	regexp.MustCompile(`(?i)((?:password|passwd|pwd|secret|token|api[_-]?key)\s*[:=]\s*)([^\s"']+)`),
	regexp.MustCompile(`(?i)(--?(?:password|passwd|pwd|secret|token|api[_-]?key)\s+)([^\s"']+)`),
	regexp.MustCompile(`(?i)(authorization\s*:\s*bearer\s+)(\S+)`),
}

// String returns s with secret-looking values replaced by ***.
func String(s string) string {
	for _, re := range patterns {
		s = re.ReplaceAllString(s, `${1}***`)
	}
	return s
}
