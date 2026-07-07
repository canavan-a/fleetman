// Package headers parses user-supplied static extra HTTP headers
// (curl-style "Name: Value" strings) shared by the agent and master CLIs.
package headers

import (
	"fmt"
	"strings"
)

// Parse splits a curl-style "Name: Value" string into a trimmed name/value
// pair. Rejects an attempt to set Authorization, since both clients already
// set that header themselves and a silent override would be confusing to
// debug.
func Parse(kv string) (name, value string, err error) {
	idx := strings.Index(kv, ":")
	if idx < 0 {
		return "", "", fmt.Errorf("invalid header format, expected \"Name: Value\", got %q", kv)
	}
	name = strings.TrimSpace(kv[:idx])
	value = strings.TrimSpace(kv[idx+1:])
	if name == "" {
		return "", "", fmt.Errorf("invalid header format, empty name in %q", kv)
	}
	if strings.EqualFold(name, "Authorization") {
		return "", "", fmt.Errorf("cannot set Authorization as an extra header")
	}
	return name, value, nil
}
