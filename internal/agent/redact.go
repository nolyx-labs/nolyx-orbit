package agent

import (
	"regexp"
	"strings"
)

const redacted = "[redacted]"

// secretKey matches key names that usually hold credentials: DB_PASSWORD, "apiKey", aws_secret_access_key, X-Auth-Token...
const secretKey = `(?i)([\w.-]*(?:password|passwd|secret|token|api[_-]?key|access[_-]?key)[\w.-]*["']?\s*[:=]\s*)`

var secretWords = []string{"password", "passwd", "secret", "token", "key"}

// secretPatterns run in order on every shipped log line, so a likely secret never leaves the server.
// Each needs a distinctive prefix or a key name, which keeps ordinary text untouched. A pattern only runs
// when the lowercased line contains one of its hints: regexes cost ~50µs a line, the hints almost nothing.
var secretPatterns = []struct {
	hints []string
	re    *regexp.Regexp
	repl  string
}{
	{[]string{"private key-----"}, regexp.MustCompile(`-----BEGIN [A-Z ]*PRIVATE KEY-----.*?(?:-----END [A-Z ]*PRIVATE KEY-----|$)`), redacted},
	{[]string{"akia", "asia"}, regexp.MustCompile(`\b(?:AKIA|ASIA)[0-9A-Z]{16}\b`), redacted},
	{[]string{"eyj"}, regexp.MustCompile(`\beyJ[\w-]{8,}\.eyJ[\w-]{8,}\.[\w-]{8,}`), redacted},
	{[]string{"ghp_", "gho_", "ghu_", "ghs_", "ghr_", "github_pat_"}, regexp.MustCompile(`\b(?:gh[pousr]_[A-Za-z0-9]{30,}|github_pat_\w{30,})`), redacted},
	{[]string{"xox"}, regexp.MustCompile(`\bxox[abposr]-[A-Za-z0-9-]{10,}`), redacted},
	{[]string{"sk_", "rk_"}, regexp.MustCompile(`\b[sr]k_(?:live|test)_[A-Za-z0-9]{16,}`), redacted},
	{[]string{"orb_"}, regexp.MustCompile(`\borb_[a-z]{3}_[\w-]{20,}`), redacted}, // Orbit's own tokens (agent, enrollment, invite, API key, reset)
	{[]string{"://"}, regexp.MustCompile(`(\b[a-zA-Z][a-zA-Z0-9+.-]*://[^\s:/@]+:)[^\s/@]+@`), "${1}" + redacted + "@"},
	{[]string{"authorization"}, regexp.MustCompile(`(?i)(\bauthorization["']?\s*[:=]\s*["']?(?:(?:bearer|basic|token|digest)\s+)?)[^\s"',;]+`), "${1}" + redacted},
	{[]string{"bearer"}, regexp.MustCompile(`(?i)(\bbearer\s+)[\w~+/.-]{20,}=*`), "${1}" + redacted},
	{secretWords, regexp.MustCompile(secretKey + `"(?:[^"\\]|\\.)*"`), `${1}"` + redacted + `"`},
	{secretWords, regexp.MustCompile(secretKey + `'[^']*'`), `${1}'` + redacted + `'`},
	{secretWords, regexp.MustCompile(secretKey + `(?:\[redacted\]|[^\s"',;&}\]]+)`), "${1}" + redacted}, // a value already masked above stays whole
}

var (
	keyBegin = regexp.MustCompile(`-----BEGIN [A-Z ]*PRIVATE KEY-----`)
	keyEnd   = regexp.MustCompile(`-----END [A-Z ]*PRIVATE KEY-----`)
)

// redactor masks secrets line by line; it remembers when a private key block spans several lines.
type redactor struct{ inKey bool }

func (r *redactor) line(s string) string {
	if r.inKey {
		r.inKey = !keyEnd.MatchString(s)
		return redacted
	}
	if keyBegin.MatchString(s) && !keyEnd.MatchString(s) {
		r.inKey = true
	}
	lower := strings.ToLower(s)
	for _, p := range secretPatterns {
		for _, h := range p.hints {
			if strings.Contains(lower, h) {
				s = p.re.ReplaceAllString(s, p.repl)
				break
			}
		}
	}
	return s
}
