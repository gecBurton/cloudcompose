package compiler

import (
	"fmt"
	"regexp"

	"github.com/gecburton/composey/internal/models"
)

// Resolution reports what one environment variable resolved to, and what
// that implies, mirroring connections.py's Resolution dataclass.
//
// A resolved value is no longer just a string: pointing a client at a
// managed service can hand it a credential, and a credential cannot travel
// as a plain environment variable. The caller needs to know which, so the
// fact is reported here rather than re-derived by inspecting the value for
// something password-shaped.
type Resolution struct {
	Value        string
	Service      *string
	Confidential bool
}

// urlPattern matches a URL whose host is exactly serviceName, mirroring
// connections.py's _url_pattern.
func urlPattern(serviceName string) *regexp.Regexp {
	return regexp.MustCompile(
		`^(?P<scheme>[A-Za-z][A-Za-z0-9+.\-]*)://` +
			`(?P<userinfo>[^@/?#]*@)?` +
			regexp.QuoteMeta(serviceName) +
			`(?::(?P<port>\d+))?` +
			`(?P<path>/[^?#]*)?` +
			`(?P<query>[?#].*)?$`,
	)
}

// userinfo is the credentials a client presents, once the managed service
// has replaced the container, mirroring connections.py's _userinfo.
//
// The connection is authoritative, for the same reason it is about the
// port: the username a compose file wrote belonged to a container the
// platform threw away, and the managed service generated its own.
// Preserving what was written locally produces a URL that resolves to a
// real database and is rejected by it.
func userinfo(stated string, connection *models.Connection) string {
	if connection.Username == nil {
		return stated
	}
	if connection.Password == nil {
		return *connection.Username + "@"
	}
	return *connection.Username + ":" + *connection.Password + "@"
}

// connectionPath is the path component, which for a database URL names the
// database, mirroring connections.py's _path.
//
// Substituted for the same reason as the credentials: the name in the
// compose file is the one the local container created, and the managed
// instance holds whatever the compiler asked for.
func connectionPath(stated string, connection *models.Connection) string {
	if connection.Database != nil {
		return "/" + *connection.Database
	}
	return stated
}

// rebuildURL swaps a URL's host, credentials and database for the managed
// service's, mirroring connections.py's _rebuild_url.
func rebuildURL(match []string, names map[string]int, connection *models.Connection) string {
	group := func(name string) string {
		idx, ok := names[name]
		if !ok || idx >= len(match) {
			return ""
		}
		return match[idx]
	}

	// The connection is authoritative about the port: a managed service
	// rarely listens where the local container did. No port means the
	// scheme's default applies, so none is written.
	port := ""
	if connection.Port != nil {
		port = fmt.Sprintf(":%d", *connection.Port)
	}

	return group("scheme") + "://" +
		userinfo(group("userinfo"), connection) +
		connection.Host + port +
		connectionPath(group("path"), connection) +
		group("query")
}

// ResolveValue resolves one environment variable value against the services
// it references, mirroring connections.py's resolve_value. Returns the
// value unchanged, referencing nothing, when it refers to no managed
// service.
//
// Iteration order over connections matters only for which service a value
// is attributed to when a value ambiguously matches more than one -- Python
// iterates dict insertion order; callers here should pass a
// deterministically-ordered slice of names for byte-identical output, since
// Go map iteration order is randomized. See ResolveValueOrdered.
func ResolveValue(value string, connections map[string]models.Connection, order []string) Resolution {
	for _, serviceName := range order {
		connection, ok := connections[serviceName]
		if !ok {
			continue
		}

		if value == serviceName {
			// A bare reference is an address or an identifier, never a
			// credential, so it stays an ordinary environment variable.
			ref := connection.BareReference()
			svc := serviceName
			return Resolution{Value: ref, Service: &svc}
		}

		pattern := urlPattern(serviceName)
		match := pattern.FindStringSubmatch(value)
		if match != nil {
			names := map[string]int{}
			for i, name := range pattern.SubexpNames() {
				if name != "" {
					names[name] = i
				}
			}
			svc := serviceName
			return Resolution{
				Value:        rebuildURL(match, names, &connection),
				Service:      &svc,
				Confidential: connection.Password != nil,
			}
		}
	}

	return Resolution{Value: value}
}

// DefaultPort is the port a client reaches a service on, for firewall-style
// rules, mirroring connections.py's default_port.
func DefaultPort(connection *models.Connection, fallback int) int {
	if connection == nil || connection.Port == nil {
		return fallback
	}
	return *connection.Port
}
