// Copyright Dokimasia B.V. 2026
// SPDX-License-Identifier: MIT

package conform

import (
	"fmt"
	"reflect"
	"slices"

	"go.dokimi.dev/assert"
)

// Supplied is the door of a value of one type: the function that opens the
// value for a subject, such as the client of the backend of a store. An
// application passes it through the option [Supplies], and a check passes it
// to its planted defect through the field Values of [Doors]. [Supply] returns
// it.
type Supplied[S any] struct {
	of   reflect.Type
	open func(h *Handle[S], s S) any
}

// Supply returns the door of a value of the type V, which open opens for a
// subject s. The type of the value identifies the door, so a suite declares a
// type for each door.
func Supply[S, V any](open func(h *Handle[S], s S) V) Supplied[S] {
	return Supplied[S]{of: reflect.TypeFor[V](), open: func(h *Handle[S], s S) any { return open(h, s) }}
}

// Supplies supplies the doors of values of the subjects of an application.
// [Open] returns the value of the door of its type. A door replaces the door
// of its type that an earlier option or an earlier door supplies.
func Supplies[S any](doors ...Supplied[S]) Option[S] {
	return Option[S]{set: func(c *config[S]) {
		for _, d := range doors {
			at := slices.IndexFunc(c.doors.Values, d.opens)
			if at < 0 {
				c.doors.Values = append(c.doors.Values, d)
				continue
			}
			c.doors.Values[at] = d
		}
	}}
}

// Open returns the value of the type V for the subject s, from the door of
// that type of the planted defect of the check, or from the option [Supplies]
// of the application. Open calls the function of the door as a factory builds
// a subject, so a read of the clock of the handle in the function is a read by
// a subject. Without the door it ends the check with a fault that wraps
// [ErrOption], and it returns the zero value to a seat that takes the fault
// and goes on.
func Open[V, S any](h *Handle[S], s S) V {
	h.Helper()
	of := reflect.TypeFor[V]()
	at := slices.IndexFunc(h.config.doors.Values, func(d Supplied[S]) bool { return d.of == of })
	if at < 0 {
		h.lacksValue(of)
		var zero V
		return zero
	}
	value, _ := h.opened(h.config.doors.Values[at], s).(V)
	return value
}

// opens reports whether the door other has the type of the door d.
func (d Supplied[S]) opens(other Supplied[S]) bool {
	return other.of == d.of
}

// opened returns the value of the door d for the subject s. A read of the
// clock while the function of d runs is a read by a subject.
func (h *Handle[S]) opened(d Supplied[S], s S) any {
	h.building.Add(1)
	defer h.building.Add(-1)
	return d.open(h, s)
}

// lacksValue ends the check with the fault of the door of a value of the type
// of, which the handle lacks. The fault of a handle on a planted defect
// contains the door of the check, and the fault of any other handle contains
// the option of the application.
func (h *Handle[S]) lacksValue(of reflect.Type) {
	h.Helper()
	subjects := reflect.TypeFor[S]()
	door := fmt.Sprintf("conform.Supply(func(h *conform.Handle[%[1]v], s %[1]v) %[2]v { … })", subjects, of)
	if h.config.defect {
		assert.Fault(h, fmt.Errorf("%w: %s opens a value of the type %v on its planted defect, and the check has no "+
			"door of the type. Give the check the door:\n\tconform.Doors[%v]{Values: []conform.Supplied[%v]{%s}}",
			ErrOption, h.check.ID, of, subjects, subjects, door))
		return
	}
	assert.Fault(h, fmt.Errorf("%w: %s opens a value of the type %v without the option conform.Supplies. "+
		"Pass the option to the application:\n\tconform.Supplies(%s)", ErrOption, h.check.ID, of, door))
}

// repeated returns the type of the first door of values whose type an earlier
// door of values has, and false where each door has a type of its own.
func repeated[S any](values []Supplied[S]) (reflect.Type, bool) {
	for i, d := range values {
		if slices.ContainsFunc(values[:i], d.opens) {
			return d.of, true
		}
	}
	return nil, false
}
