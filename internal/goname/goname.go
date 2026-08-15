// Package goname converts tmux and Python lower_snake_case names to the Go
// exported spelling this module uses. It is the single source of truth for
// that convention: generated format accessors and the parity omission guard
// must agree on how a name crosses the language boundary, or the guard would
// look for symbols the generator never produces.
package goname

import "strings"

// initialisms are words Go writes fully capitalized. The Go community treats a
// mixed-case initialism such as "Id" or "Utf8" as a style error, so a name
// crossing into Go adopts the capitalized form. Entries cover the tmux
// vocabulary this module already generates plus the initialisms Go programmers
// expect, so a future name containing one needs no change here.
var initialisms = map[string]string{
	"acl":   "ACL",
	"api":   "API",
	"ascii": "ASCII",
	"bg":    "BG",
	"cpu":   "CPU",
	"css":   "CSS",
	"cwd":   "CWD",
	"db":    "DB",
	"dns":   "DNS",
	"eof":   "EOF",
	"fg":    "FG",
	"gid":   "GID",
	"guid":  "GUID",
	"html":  "HTML",
	"http":  "HTTP",
	"https": "HTTPS",
	"id":    "ID",
	"ip":    "IP",
	"json":  "JSON",
	"lhs":   "LHS",
	"os":    "OS",
	"pb":    "PB",
	"pid":   "PID",
	"ram":   "RAM",
	"rhs":   "RHS",
	"rpc":   "RPC",
	"sgr":   "SGR",
	"sla":   "SLA",
	"smtp":  "SMTP",
	"sql":   "SQL",
	"ssh":   "SSH",
	"tcp":   "TCP",
	"tls":   "TLS",
	"ttl":   "TTL",
	"tty":   "TTY",
	"udp":   "UDP",
	"ui":    "UI",
	"uid":   "UID",
	"uri":   "URI",
	"url":   "URL",
	"utf8":  "UTF8",
	"uuid":  "UUID",
	"vm":    "VM",
	"xml":   "XML",
	"xsrf":  "XSRF",
	"xss":   "XSS",
}

// compounds are single lower-case tokens that carry more than one English word.
// tmux spells them unseparated, so splitting on "_" alone would produce
// Readonly or Termname rather than the Go spelling a reader expects.
var compounds = map[string]string{
	"readonly":     "ReadOnly",
	"termfeatures": "TermFeatures",
	"termname":     "TermName",
	"termtype":     "TermType",
}

// Exported returns the Go exported spelling of a lower_snake_case name. It
// capitalizes each underscore-separated word, expands known initialisms and
// compounds, and drops empty segments so leading, trailing, and doubled
// underscores cannot produce an invalid identifier. Callers own the decision
// that a name should be exported at all; Exported never inspects visibility.
//
// Exported returns an empty string when name contributes no words. Word
// matching is exact and case-sensitive, so a name that already carries Go
// casing passes through its non-initialism words unchanged.
func Exported(name string) string {
	var exported strings.Builder
	for _, word := range strings.Split(name, "_") {
		switch {
		case word == "":
			continue
		case initialisms[word] != "":
			exported.WriteString(initialisms[word])
		case compounds[word] != "":
			exported.WriteString(compounds[word])
		default:
			exported.WriteString(strings.ToUpper(word[:1]))
			exported.WriteString(word[1:])
		}
	}
	return exported.String()
}
