package main

import (
	"fmt"
	"strconv"
)

// parseInt64Arg parses a positional argument expected to be a database id.
// Centralised so every command reports the same "not a valid id" wording
// rather than each one leaking a raw strconv error.
func parseInt64Arg(s string) (int64, error) {
	n, err := strconv.ParseInt(s, 10, 64)
	if err != nil {
		return 0, fmt.Errorf("%q is not a valid id", s)
	}
	return n, nil
}
