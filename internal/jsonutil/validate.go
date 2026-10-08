package jsonutil

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
)

// Validate rejects duplicate keys, which otherwise make reconciliation hashes
// depend on which parser's last-value behavior happened to run first.
func Validate(data []byte) error {
	d := json.NewDecoder(bytes.NewReader(data))
	d.UseNumber()
	var value func(int) error
	value = func(depth int) error {
		if depth > 100 {
			return errors.New("JSON nesting limit exceeded")
		}
		t, e := d.Token()
		if e != nil {
			return e
		}
		delim, ok := t.(json.Delim)
		if !ok {
			return nil
		}
		switch delim {
		case '{':
			seen := map[string]bool{}
			for d.More() {
				k, e := d.Token()
				if e != nil {
					return e
				}
				key, ok := k.(string)
				if !ok || seen[key] {
					return errors.New("ambiguous JSON object")
				}
				seen[key] = true
				if e = value(depth + 1); e != nil {
					return e
				}
			}
		case '[':
			for d.More() {
				if e := value(depth + 1); e != nil {
					return e
				}
			}
		default:
			return errors.New("invalid JSON delimiter")
		}
		_, e = d.Token()
		return e
	}
	if e := value(0); e != nil {
		return e
	}
	if _, e := d.Token(); e != io.EOF {
		return errors.New("expected one JSON document")
	}
	return nil
}
