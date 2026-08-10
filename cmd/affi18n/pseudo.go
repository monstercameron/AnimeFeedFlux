package main

import (
	"bufio"
	"fmt"
	"os"

	"github.com/monstercameron/AnimeFeedFlux/internal/i18nlint"
)

// runPseudo implements `affi18n pseudo [string...]` (TODOS.md D6-24):
// prints each argument through i18nlint.Pseudolocalize, or, with no
// arguments, pseudolocalizes stdin line by line — for piping a catalogue's
// values through in a pseudolocale build step.
func runPseudo(args []string, stdout, stderr *os.File) int {
	if len(args) > 0 {
		for _, s := range args {
			fmt.Fprintln(stdout, i18nlint.Pseudolocalize(s))
		}
		return 0
	}

	scanner := bufio.NewScanner(os.Stdin)
	for scanner.Scan() {
		fmt.Fprintln(stdout, i18nlint.Pseudolocalize(scanner.Text()))
	}
	if err := scanner.Err(); err != nil {
		fmt.Fprintf(stderr, "affi18n pseudo: %v\n", err)
		return 1
	}
	return 0
}
