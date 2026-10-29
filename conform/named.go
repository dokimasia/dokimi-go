// Copyright Dokimasia B.V. 2026
// SPDX-License-Identifier: MIT

package conform

// Named is a value with the name by which the source declares it, such as a
// variant of an enumeration or an error value of a package. A probe over
// values names each value at fault in its contract.
type Named[T any] struct {
	Value T
	Name  string
}
