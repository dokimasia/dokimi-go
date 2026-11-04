// Copyright Dokimasia B.V. 2026
// SPDX-License-Identifier: MIT

package spec

import (
	"strings"
	"unicode"
)

// Key returns the key form of a check identity, which joins one check across
// languages. Each of the declaration and the callable becomes its words in
// lower case, joined by hyphens, and the segment and the case stay as they
// are: Store.ReadAll/zero-on-error has the key store.read-all/zero-on-error.
// An identity without a segment, such as Store.PutAll, is the identity of a
// callable, and its key is store.put-all. The test of the package checks Key
// against every identity of keys.json, whose keys dokimi derives.
func Key(identity string) string {
	head, rest, segmented := strings.Cut(identity, "/")
	declaration, callable, method := strings.Cut(head, ".")
	key := keyOf(declaration)
	if method {
		key += "." + keyOf(callable)
	}
	if segmented {
		key += "/" + rest
	}
	return key
}

// keyOf returns the words of name in lower case, joined by hyphens.
func keyOf(name string) string {
	return strings.ToLower(strings.Join(words(name), "-"))
}

// words splits name into its words. A word ends at a separator, which is _,
// -, ., a space, a tab or a slash, and before an upper-case letter that a
// lower-case letter or a digit precedes. A run of upper-case letters ends
// before its last letter where a lower-case letter follows that letter. The
// plural s of the run, an s at the end of name or before a rune other than
// a lower-case letter, does not end it: UserIDs splits into User and IDs.
func words(name string) []string {
	runes := []rune(name)
	var out []string
	start := -1
	for i, r := range runes {
		switch {
		case strings.ContainsRune("_-. \t/", r):
			if start >= 0 {
				out = append(out, string(runes[start:i]))
				start = -1
			}
		case i > 0 && breaksBefore(runes, i):
			out = append(out, string(runes[start:i]))
			start = i
		case start < 0:
			start = i
		}
	}
	if start >= 0 {
		out = append(out, string(runes[start:]))
	}
	return out
}

// breaksBefore reports whether a word ends before the rune at i of runes.
func breaksBefore(runes []rune, i int) bool {
	prev, cur := runes[i-1], runes[i]
	if (unicode.IsLower(prev) || unicode.IsDigit(prev)) && unicode.IsUpper(cur) {
		return true
	}
	if unicode.IsUpper(prev) && unicode.IsUpper(cur) && i+1 < len(runes) {
		next := runes[i+1]
		return unicode.IsLower(next) && (next != 's' || i+2 < len(runes) && unicode.IsLower(runes[i+2]))
	}
	return false
}
