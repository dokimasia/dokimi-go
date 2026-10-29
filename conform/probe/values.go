// Copyright Dokimasia B.V. 2026
// SPDX-License-Identifier: MIT

package probe

import (
	"encoding"
	"errors"
	"reflect"
	"strings"

	"go.dokimi.dev/assert"

	"go.dokimi.dev/conform"
	"go.dokimi.dev/spec"
)

// The probes of enumerations and errors call no subject. Each probe collects
// the names of the values at fault and asserts once that it found none. The
// contract of that assertion is the statement, followed by those names.

// ListsTheMembers fails unless list returns the values of members once each,
// in the order of members.
func ListsTheMembers[S, E any](h *conform.Handle[S], members []conform.Named[E], list func() []E) {
	h.Helper()
	want := make([]E, len(members))
	for i, m := range members {
		want[i] = m.Value
	}
	h.Expect(spec.MisorderedMembers)
	assert.Equal(h, list(), want, h.Statement())
}

// NamesDiffer fails when name returns one text for two members, or for a
// member and outside.
func NamesDiffer[S, E any](h *conform.Handle[S], members []conform.Named[E], name func(e E) string, outside E) {
	h.Helper()
	var faults []string
	seen := map[string]string{}
	for _, m := range members {
		text := name(m.Value)
		if first, shared := seen[text]; shared {
			faults = append(faults, first+" and "+m.Name)
			continue
		}
		seen[text] = m.Name
	}
	if member, shared := seen[name(outside)]; shared {
		faults = append(faults, member+" and the value outside the members")
	}
	judge(h, spec.SharedText, faults)
}

// JudgesValidity fails unless valid accepts each member and refuses outside.
func JudgesValidity[S, E any](h *conform.Handle[S], members []conform.Named[E], valid func(e E) bool, outside E) {
	h.Helper()
	var faults []string
	for _, m := range members {
		if !valid(m.Value) {
			faults = append(faults, m.Name)
		}
	}
	if valid(outside) {
		faults = append(faults, "the value outside the members")
	}
	judge(h, spec.MisjudgedValidity, faults)
}

// ParsesEachText fails unless parse returns each member for the text that
// name returns for it, and fails a text of no member.
func ParsesEachText[S, E any](
	h *conform.Handle[S], members []conform.Named[E], parse func(text string) (E, error), name func(e E) string,
) {
	h.Helper()
	var faults []string
	texts := make([]string, len(members))
	for i, m := range members {
		texts[i] = name(m.Value)
		if got, err := parse(texts[i]); err != nil || !reflect.DeepEqual(got, m.Value) {
			faults = append(faults, m.Name)
		}
	}
	unknown := unknownText(texts)
	if _, err := parse(unknown); err == nil {
		faults = append(faults, "the text "+unknown)
	}
	judge(h, spec.UnparsedText, faults)
}

// EncodesEachVariant fails unless each member encodes the text that name
// returns for it.
func EncodesEachVariant[S any, E encoding.TextMarshaler](
	h *conform.Handle[S], members []conform.Named[E], name func(e E) string,
) {
	h.Helper()
	var faults []string
	for _, m := range members {
		if text, err := m.Value.MarshalText(); err != nil || string(text) != name(m.Value) {
			faults = append(faults, m.Name)
		}
	}
	judge(h, spec.MismarshalledText, faults)
}

// DecodesEachText fails unless the text that name returns for each member
// decodes into a zero value as the member, and a text of no member fails to
// decode.
func DecodesEachText[S, E any, P interface {
	*E
	encoding.TextUnmarshaler
}](h *conform.Handle[S], members []conform.Named[E], name func(e E) string) {
	h.Helper()
	var faults []string
	texts := make([]string, len(members))
	for i, m := range members {
		texts[i] = name(m.Value)
		var got E
		if err := P(&got).UnmarshalText([]byte(texts[i])); err != nil || !reflect.DeepEqual(got, m.Value) {
			faults = append(faults, m.Name)
		}
	}
	unknown := unknownText(texts)
	var got E
	if P(&got).UnmarshalText([]byte(unknown)) == nil {
		faults = append(faults, "the text "+unknown)
	}
	judge(h, spec.UnmarshalledWrong, faults)
}

// MessagesDiffer fails unless the message of each value opens with opening,
// and no message opens with the whole message of another value. A message
// that equals another opens with it.
func MessagesDiffer[S any](h *conform.Handle[S], values []conform.Named[error], opening string) {
	h.Helper()
	var faults []string
	for i, v := range values {
		if !strings.HasPrefix(v.Value.Error(), opening) {
			faults = append(faults, v.Name)
		}
		for j, other := range values {
			if i != j && strings.HasPrefix(v.Value.Error(), other.Value.Error()) {
				faults = append(faults, v.Name+" and "+other.Name)
			}
		}
	}
	judge(h, spec.WrongMessage, faults)
}

// ErrorsDiffer fails when errors.Is matches a value with another value or
// with a neighbour.
func ErrorsDiffer[S any](h *conform.Handle[S], values, neighbours []conform.Named[error]) {
	h.Helper()
	var faults []string
	for i, v := range values {
		for j, other := range values {
			if i != j && errors.Is(v.Value, other.Value) {
				faults = append(faults, v.Name+" and "+other.Name)
			}
		}
		for _, n := range neighbours {
			if errors.Is(v.Value, n.Value) {
				faults = append(faults, v.Name+" and "+n.Name)
			}
		}
	}
	judge(h, spec.SharedIdentity, faults)
}

// TypeMessage fails unless the message of the value of each type opens with
// opening.
func TypeMessage[S any](h *conform.Handle[S], types []conform.Named[error], opening string) {
	h.Helper()
	var faults []string
	for _, t := range types {
		if !strings.HasPrefix(t.Value.Error(), opening) {
			faults = append(faults, t.Name)
		}
	}
	judge(h, spec.WrongMessage, faults)
}

// ComparesWithIdentity fails when the method Is of the value of a type
// disagrees with errors.Is on one of values. It passes over a type without
// the method Is.
func ComparesWithIdentity[S any](h *conform.Handle[S], types, values []conform.Named[error]) {
	h.Helper()
	var faults []string
	for _, t := range types {
		is, compares := t.Value.(interface{ Is(target error) bool })
		if !compares {
			continue
		}
		for _, v := range values {
			if is.Is(v.Value) != errors.Is(t.Value, v.Value) {
				faults = append(faults, t.Name+" and "+v.Name)
			}
		}
	}
	judge(h, spec.DisagreeingIs, faults)
}

// ReturnsTheCause fails unless each of causes makes an error that
// errors.Unwrap returns the cause of.
func ReturnsTheCause[S any](h *conform.Handle[S], causes []conform.Named[func(cause error) error]) {
	h.Helper()
	cause := errors.New("probe: the cause of the check")
	var faults []string
	for _, c := range causes {
		if !errors.Is(errors.Unwrap(c.Value(cause)), cause) {
			faults = append(faults, c.Name)
		}
	}
	judge(h, spec.LostCause, faults)
}

// TypesDiffer fails when errors.Is matches the value of one type with the
// value of another.
func TypesDiffer[S any](h *conform.Handle[S], types []conform.Named[error]) {
	h.Helper()
	var faults []string
	for i, t := range types {
		for j, other := range types {
			if i != j && errors.Is(t.Value, other.Value) {
				faults = append(faults, t.Name+" and "+other.Name)
			}
		}
	}
	judge(h, spec.SharedIdentity, faults)
}

// judge sets reason and fails the check when faults is not empty. The
// contract is the statement, followed by faults.
func judge[S any](h *conform.Handle[S], reason spec.ReasonID, faults []string) {
	h.Helper()
	h.Expect(reason)
	assert.Empty(h, faults, h.Statement()+": "+strings.Join(faults, ", "))
}

// unknownText returns a text that differs from each of texts, because it is
// longer than each of them.
func unknownText(texts []string) string {
	return strings.Join(texts, "|") + "|"
}
