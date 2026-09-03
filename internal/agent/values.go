package agent

import (
	"encoding/json"
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

// validKey matches what a POSIX shell and docker compose both accept. Keys come from
// a hand-edited blob, so one bad key must be named rather than silently passed on to
// break every consumer at once.
var validKey = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

// Values is the normalised payload: every value is a string, so no consumer has to
// guess how a bool or a number should be spelled.
type Values map[string]string

// Normalize converts a decoded JSON object into Values, rejecting anything that
// cannot be represented as an environment variable.
func Normalize(raw map[string]any) (Values, error) {
	if len(raw) == 0 {
		return nil, fmt.Errorf("payload is empty")
	}

	values := make(Values, len(raw))
	for key, value := range raw {
		if !validKey.MatchString(key) {
			return nil, fmt.Errorf("key %q is not a valid environment variable name", key)
		}
		switch typed := value.(type) {
		case string:
			values[key] = typed
		case bool:
			values[key] = strconv.FormatBool(typed)
		case float64:
			// encoding/json gives every number as float64; keep integers integral so
			// a port number does not arrive as "8080.000000".
			if typed == float64(int64(typed)) {
				values[key] = strconv.FormatInt(int64(typed), 10)
			} else {
				values[key] = strconv.FormatFloat(typed, 'f', -1, 64)
			}
		case nil:
			values[key] = ""
		default:
			return nil, fmt.Errorf("key %s holds %T, expected a scalar", key, value)
		}
	}
	return values, nil
}

// Decode parses a payload and normalises it in one step.
func Decode(payload []byte) (Values, error) {
	var raw map[string]any
	if err := json.Unmarshal(payload, &raw); err != nil {
		// Deliberately does not wrap the decoder error: encoding/json includes the
		// offending input in its message, which here is secret material.
		return nil, fmt.Errorf("payload is not a JSON object")
	}
	return Normalize(raw)
}

// Merge returns a copy of base with overlay applied on top.
func Merge(base, overlay Values) Values {
	merged := make(Values, len(base)+len(overlay))
	for key, value := range base {
		merged[key] = value
	}
	for key, value := range overlay {
		merged[key] = value
	}
	return merged
}

// Subset returns the values whose key carries the given prefix.
func (v Values) Subset(prefix string) Values {
	subset := make(Values)
	for key, value := range v {
		if strings.HasPrefix(key, prefix) {
			subset[key] = value
		}
	}
	return subset
}

// Keys returns the keys in a stable order, so digests over Values are reproducible.
func (v Values) Keys() []string {
	keys := make([]string, 0, len(v))
	for key := range v {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

// Environ renders the values as KEY=value pairs for exec, which needs no quoting at
// all — the whole class of dotenv escaping bugs does not exist on this path.
func (v Values) Environ() []string {
	pairs := make([]string, 0, len(v))
	for _, key := range v.Keys() {
		pairs = append(pairs, key+"="+v[key])
	}
	return pairs
}

// RenderSystemdEnv produces systemd EnvironmentFile format: double quoted with
// backslash escapes. A newline cannot be represented in that format at all, so such a
// value is refused loudly instead of being written as garbage that only shows up
// later as a runtime authentication failure.
func (v Values) RenderSystemdEnv() (string, error) {
	var out strings.Builder
	for _, key := range v.Keys() {
		value := v[key]
		if strings.ContainsAny(value, "\n\r") {
			return "", fmt.Errorf("%s contains a newline, which systemd cannot represent", key)
		}
		escaped := strings.ReplaceAll(value, `\`, `\\`)
		escaped = strings.ReplaceAll(escaped, `"`, `\"`)
		fmt.Fprintf(&out, "%s=\"%s\"\n", key, escaped)
	}
	return out.String(), nil
}

// JSON renders a stable representation, used for the cache and for digests.
func (v Values) JSON() ([]byte, error) {
	return json.MarshalIndent(map[string]string(v), "", "  ")
}
