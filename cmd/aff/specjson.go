package main

import (
	"fmt"
	"os"

	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
)

// loadProtoJSON parses either an inline JSON string (jsonFlag) or a JSON
// file (fileFlag) into msg, in that precedence order if somehow both are
// set. Used for FeedSpec (feed create/update) and Settings (system
// settings-update), which are too structurally nested — sources, novelty,
// price tables — to reasonably expose as one flag per field without either
// a combinatorial flag set or a bespoke mini-language; JSON is the format
// `recipe export`/`ExportTOML` already round-trips through in spirit, and
// protojson (not encoding/json) is required so enum fields parse from their
// string names rather than needing raw integers.
func loadProtoJSON(jsonFlag, fileFlag string, msg proto.Message) error {
	var raw []byte
	switch {
	case jsonFlag != "":
		raw = []byte(jsonFlag)
	case fileFlag != "":
		b, err := os.ReadFile(fileFlag)
		if err != nil {
			return fmt.Errorf("aff: reading %s: %w", fileFlag, err)
		}
		raw = b
	default:
		return nil
	}
	if err := protojson.Unmarshal(raw, msg); err != nil {
		return fmt.Errorf("aff: parsing JSON: %w", err)
	}
	return nil
}
