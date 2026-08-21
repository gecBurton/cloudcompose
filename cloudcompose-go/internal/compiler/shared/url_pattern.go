package shared

import "regexp"

// URLPattern matches a URL whose host is exactly serviceName. Used both
// by inference (deciding whether a connection string references a
// managed service) and by --explain reporting.
func URLPattern(serviceName string) *regexp.Regexp {
	return regexp.MustCompile(
		`^(?P<scheme>[A-Za-z][A-Za-z0-9+.\-]*)://` +
			`(?P<userinfo>[^@/?#]*@)?` +
			regexp.QuoteMeta(serviceName) +
			`(?::(?P<port>\d+))?` +
			`(?P<path>/[^?#]*)?` +
			`(?P<query>[?#].*)?$`,
	)
}
