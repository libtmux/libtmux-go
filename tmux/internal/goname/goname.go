// Package goname defines the exported Go spelling shared by generators and
// parity checks for tmux and Python lower_snake_case names.
package goname

import "strings"

// initialisms use Go's conventional all-capital spelling.
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

// compounds records tmux words whose Go spelling needs an internal capital.
var compounds = map[string]string{
	"readonly":     "ReadOnly",
	"termfeatures": "TermFeatures",
	"termname":     "TermName",
	"termtype":     "TermType",
}

// Exported converts lower_snake_case to exported Go spelling, applying known
// initialisms and compounds and ignoring empty segments. Matching is
// case-sensitive; a name with no words returns an empty string.
func Exported(name string) string {
	var exported strings.Builder
	for word := range strings.SplitSeq(name, "_") {
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
