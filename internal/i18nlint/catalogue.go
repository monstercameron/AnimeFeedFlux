package i18nlint

import "sort"

// CheckCatalogue implements TODOS.md D6-22 and D6-23: the catalogue and the
// code that references it must describe exactly the same set of keys in
// both directions.
//
//   - missing: keys referenced by code (refs) that are absent from cat. A
//     missing key ships as the raw key string, visible on screen — this is
//     the more urgent of the two directions.
//   - unused: keys present in cat that no ref names. An unreferenced key is
//     how a catalogue grows past the interface it actually describes,
//     silently, one abandoned rename at a time.
//
// refs may contain duplicates (the same key referenced from many call
// sites); both return slices are deduplicated and sorted for a stable,
// diffable result.
func CheckCatalogue(cat map[string]string, refs []string) (missing, unused []string) {
	referenced := make(map[string]bool, len(refs))
	missingSet := make(map[string]bool)
	for _, key := range refs {
		referenced[key] = true
		if _, ok := cat[key]; !ok {
			missingSet[key] = true
		}
	}

	for key := range missingSet {
		missing = append(missing, key)
	}
	for key := range cat {
		if !referenced[key] {
			unused = append(unused, key)
		}
	}

	sort.Strings(missing)
	sort.Strings(unused)
	return missing, unused
}
