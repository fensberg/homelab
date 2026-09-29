// Package ghapi builds GitHub REST API addresses.
//
// Written once: the security patrol and the sweeper each carried the same
// endpoint builder.
package ghapi

import "fmt"

// DefaultBase is GitHub's REST API.
const DefaultBase = "https://api.github.com"

// URL is the API address for a path, formatted like fmt.Sprintf, under base -
// or under DefaultBase when base is empty, which is how a test points a client
// at a local server and production leaves it alone.
func URL(base, format string, args ...any) string {
	if base == "" {
		base = DefaultBase
	}
	return base + fmt.Sprintf(format, args...)
}
