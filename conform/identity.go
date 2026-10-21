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

// Callable is the name of a callable of a declaration whose subjects have the
// type S, such as Store.Get. A generated suite declares one for each callable.
type Callable[S any] string

// Check returns the identity of the check of the callable whose segment is
// segment, such as Store.Get/keeps-the-value for keeps-the-value.
func (c Callable[S]) Check(segment string) ID[S] {
	return ID[S](string(c) + "/" + segment)
}
