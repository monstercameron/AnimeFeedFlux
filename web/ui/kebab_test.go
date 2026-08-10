package ui

import (
	"reflect"
	"testing"
)

func TestOrderKebabItems(t *testing.T) {
	item := func(id string, danger bool) KebabItem { return KebabItem{ID: id, Danger: danger} }

	tests := []struct {
		name string
		in   []KebabItem
		want []string // expected IDs in order
	}{
		{
			name: "already safe-then-danger stays stable",
			in:   []KebabItem{item("edit", false), item("duplicate", false), item("delete", true)},
			want: []string{"edit", "duplicate", "delete"},
		},
		{
			name: "danger interleaved gets moved to the end, relative order kept",
			in: []KebabItem{
				item("delete", true), item("edit", false), item("purge", true), item("rename", false),
			},
			want: []string{"edit", "rename", "delete", "purge"},
		},
		{
			name: "all safe",
			in:   []KebabItem{item("a", false), item("b", false)},
			want: []string{"a", "b"},
		},
		{
			name: "all danger",
			in:   []KebabItem{item("a", true), item("b", true)},
			want: []string{"a", "b"},
		},
		{
			name: "empty",
			in:   nil,
			want: []string{},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := OrderKebabItems(tc.in)
			gotIDs := make([]string, len(got))
			for i, it := range got {
				gotIDs[i] = it.ID
			}
			if !reflect.DeepEqual(gotIDs, tc.want) {
				t.Errorf("OrderKebabItems(%v) IDs = %v, want %v", tc.in, gotIDs, tc.want)
			}
		})
	}
}
