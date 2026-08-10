package i18nlint

import (
	"reflect"
	"sort"
	"testing"
)

func TestFindKeyRefs(t *testing.T) {
	fsys, root := fixtureFS(t)
	refs, err := FindKeyRefs(fsys, root)
	if err != nil {
		t.Fatalf("FindKeyRefs: %v", err)
	}

	sort.Strings(refs)
	want := []string{"feed.create.subtitle", "feed.create.title"}
	if !reflect.DeepEqual(refs, want) {
		t.Fatalf("refs = %v, want %v", refs, want)
	}
}
