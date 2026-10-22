// Copyright Dokimasia B.V. 2026
// SPDX-License-Identifier: MIT

package conform

import (
	"regexp"
	"strings"
)

// grammar matches an identity: the name of a declaration, an optional name of
// a callable after a dot, a slash, a segment of lowercase words joined by
// hyphens, and an optional case after a number sign.
var grammar = regexp.MustCompile(
	`^[\p{L}_][\p{L}\p{N}_]*(\.[\p{L}_][\p{L}\p{N}_]*)?/[a-z][a-z0-9]*(-[a-z][a-z0-9]*)*(#[a-z0-9]+(-[a-z0-9]+)*)?$`)

// ID is the identity of a check over subjects of the type S, such as
// Store.Get/keeps-the-value. The identity joins the name of the declaration,
// the name of the callable after a dot, and the segment of the check after a
// slash. The segment consists of lowercase words joined by hyphens.
//
// The type parameter ties an identity to the subjects of its suite, so an
// identity of another suite does not compile as an option of an application.
type ID[S any] string

// declaration returns the name of the declaration of the check.
func (id ID[S]) declaration() string {
	callable, _, _ := strings.Cut(string(id), "/")
	declaration, _, _ := strings.Cut(callable, ".")
	return declaration
}

// subtest returns the name of the test of the check: the identity without the
// name of the declaration, such as Get/keeps-the-value for
// Store.Get/keeps-the-value. The name of the test of the suite contains the
// declaration. The identity of a check of a function, such as
// Slug/keeps-the-case, names no callable after a dot and is its own name.
func (id ID[S]) subtest() string {
	callable, segment, _ := strings.Cut(string(id), "/")
	if _, method, named := strings.Cut(callable, "."); named {
		return method + "/" + segment
	}
	return string(id)
}

// Callable is the name of a callable of a declaration whose subjects have the
// type S, such as Store.Get. A generated suite declares one for each callable.
type Callable[S any] string

// Check returns the identity of the check of the callable whose segment is
// segment, such as Store.Get/keeps-the-value for keeps-the-value.
func (c Callable[S]) Check(segment string) ID[S] {
	return ID[S](string(c) + "/" + segment)
}
