package shared

import (
	"fmt"

	"github.com/gecburton/cloudcompose/internal/models"
)

// Resolution reports what one environment variable resolved to, and what
// that implies. A resolved value is no longer just a string: pointing a
// client at a managed service can hand it a credential, and the caller
// needs to know which, so the fact is reported here rather than
// re-derived by inspecting the value for something password-shaped.
type Resolution struct {
	Value        string
	Service      *string
	Confidential bool
}

// userinfo is the credentials a client presents, once the managed service
// has replaced the container. The connection is authoritative: the
// username a compose file wrote belonged to a container the platform
// threw away, and the managed service generated its own.
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
// database. Substituted for the same reason as the credentials: the name
// in the compose file is the one the local container created, and the
// managed instance holds whatever the compiler asked for.
func connectionPath(stated string, connection *models.Connection) string {
	if connection.Database != nil {
		return "/" + *connection.Database
	}
	return stated
}

// rebuildURL swaps a URL's host, credentials and database for the managed
// service's. The scheme is always the one the value itself declared
// (group("scheme")), never guessed from the target service's capability.
func rebuildURL(match []string, names map[string]int, connection *models.Connection) string {
	group := func(name string) string {
		idx, ok := names[name]
		if !ok || idx >= len(match) {
			return ""
		}
		return match[idx]
	}

	// The connection is authoritative about the port: a managed service
	// rarely listens where the local container did.
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
// it references. Returns the value unchanged, referencing nothing, when it
// refers to no managed service.
//
// Iteration order over connections matters only for which service a value
// is attributed to when it ambiguously matches more than one; callers
// should pass a deterministically-ordered slice of names for deterministic
// output, since Go map iteration order is randomized.
func ResolveValue(value string, connections map[string]models.Connection, order []string) Resolution {
	for _, serviceName := range order {
		connection, ok := connections[serviceName]
		if !ok {
			continue
		}

		if value == serviceName {
			// A bare reference is an address or an identifier, never a
			// credential.
			ref := connection.BareReference()
			svc := serviceName
			return Resolution{Value: ref, Service: &svc}
		}

		pattern := URLPattern(serviceName)
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
