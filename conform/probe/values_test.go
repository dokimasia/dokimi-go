// Copyright Dokimasia B.V. 2026
// SPDX-License-Identifier: MIT

package probe_test

import (
	"encoding"
	"errors"
	"fmt"
	"testing"

	"go.dokimi.dev/assert"

	"go.dokimi.dev/conform"
	"go.dokimi.dev/conform/probe"
	"go.dokimi.dev/spec"
)

// color is the enumeration of the specs of the enumerations.
type color int

// The colors, and a value outside them.
const (
	red color = iota
	green
	blue
	outside color = 9
)

// colorTexts contains the text of each color.
var colorTexts = [...]string{"red", "green", "blue"}

// String returns the text of c.
func (c color) String() string {
	if c < red || c > blue {
		return fmt.Sprintf("color(%d)", int(c))
	}
	return colorTexts[c]
}

// MarshalText encodes the text of c.
func (c color) MarshalText() ([]byte, error) { return []byte(c.String()), nil }

// UnmarshalText decodes the color of text, and fails a text of no color.
func (c *color) UnmarshalText(text []byte) error {
	for i, t := range colorTexts {
		if string(text) == t {
			*c = color(i)
			return nil
		}
	}
	return errOther
}

// misencoded is a color that encodes blue as the text of green.
type misencoded struct{ color }

// MarshalText encodes the text of m, and the text of green for blue.
func (m misencoded) MarshalText() ([]byte, error) {
	if m.color == blue {
		return []byte("green"), nil
	}
	return m.color.MarshalText()
}

// unencodable is a color that encodes its text with an error.
type unencodable struct{ color }

// MarshalText encodes the text of u, and fails.
func (u unencodable) MarshalText() ([]byte, error) {
	text, _ := u.color.MarshalText()
	return text, errOther
}

// misdecoded is a color that decodes the text of blue as green.
type misdecoded struct{ color }

// UnmarshalText decodes the color of text, and green for the text of blue.
func (m *misdecoded) UnmarshalText(text []byte) error {
	err := m.color.UnmarshalText(text)
	if m.color == blue {
		m.color = green
	}
	return err
}

// undecodable is a color that decodes its text with an error.
type undecodable struct{ color }

// UnmarshalText decodes the color of text, and fails.
func (u *undecodable) UnmarshalText(text []byte) error {
	_ = u.color.UnmarshalText(text)
	return errOther
}

// lenient is a color that decodes a text of no color as red.
type lenient struct{ color }

// UnmarshalText decodes the color of text, and red for a text of no color.
func (l *lenient) UnmarshalText(text []byte) error {
	if l.color.UnmarshalText(text) != nil {
		l.color = red
	}
	return nil
}

// stainError is an error type whose message is its text.
type stainError struct{ text string }

// Error returns the text of e.
func (e *stainError) Error() string { return e.text }

// fadeError is an error type with a cause. Its method Is matches the cause,
// and matches nothing where blind is set.
type fadeError struct {
	cause error
	blind bool
}

// Error returns the message of e.
func (*fadeError) Error() string { return "palette: the paint fades" }

// Unwrap returns the cause of e.
func (e *fadeError) Unwrap() error { return e.cause }

// Is reports whether target is the cause of e.
func (e *fadeError) Is(target error) bool { return !e.blind && target == e.cause }

// palette is the subject of the specs of the enumerations and the errors. It
// contains the error values of a package, the values of a neighbouring
// package and a value of each error type. broken names the one call or value
// that a planted defect breaks, and is empty for a correct palette.
type palette struct {
	broken     string
	values     []conform.Named[error]
	neighbours []conform.Named[error]
	types      []conform.Named[error]
}

// palettes returns a factory of palettes whose call or value broken breaks.
func palettes(broken string) func(*conform.Handle[*palette]) *palette {
	return func(*conform.Handle[*palette]) *palette {
		dry, spilled := errors.New("palette: the paint is dry"), errors.New("mixer: the paint is spilled")
		wet := errors.New("palette: the paint is wet")
		var stain error = &stainError{text: "palette: the paint stains"}
		fade := &fadeError{cause: dry}
		switch broken {
		case "unopened":
			wet = errors.New("the paint is wet")
		case "echoed":
			wet = errors.New("palette: the paint is dry, and wet")
		case "aliased":
			wet = fmt.Errorf("palette: the paint is wet: %w", dry)
		case "neighbouring":
			wet = fmt.Errorf("palette: the paint is wet: %w", spilled)
		case "untitled":
			stain = &stainError{text: "the paint stains"}
		case "blind":
			fade.blind = true
		case "overlapping":
			fade.cause = stain
		}
		return &palette{
			broken:     broken,
			values:     []conform.Named[error]{{Value: dry, Name: "ErrDry"}, {Value: wet, Name: "ErrWet"}},
			neighbours: []conform.Named[error]{{Value: spilled, Name: "ErrSpilled"}},
			types:      []conform.Named[error]{{Value: stain, Name: "StainError"}, {Value: fade, Name: "FadeError"}},
		}
	}
}

// list returns the colors in their order. A broken misordered reverses them.
func (p *palette) list() []color {
	if p.broken == "misordered" {
		return []color{blue, green, red}
	}
	return []color{red, green, blue}
}

// name returns the text of c. A broken shared returns the text of red for
// green and blue, and a broken outside returns the text of blue for outside.
func (p *palette) name(c color) string {
	switch {
	case p.broken == "shared" && (c == green || c == blue):
		return red.String()
	case p.broken == "outside" && c == outside:
		return blue.String()
	}
	return c.String()
}

// valid reports whether c is a color. A broken strict refuses green, and a
// broken lax accepts outside.
func (p *palette) valid(c color) bool {
	switch p.broken {
	case "strict":
		return c == red || c == blue
	case "lax":
		return true
	}
	return c >= red && c <= blue
}

// parse returns the color of text, and fails a text of no color. A broken
// misparsed returns green for the text of blue, a broken unparsable fails
// each text, and a broken lenient returns red for a text of no color.
func (p *palette) parse(text string) (color, error) {
	var c color
	err := c.UnmarshalText([]byte(text))
	switch p.broken {
	case "misparsed":
		if c == blue {
			c = green
		}
	case "unparsable":
		err = errOther
	case "lenient":
		if err != nil {
			c, err = red, nil
		}
	}
	return c, err
}

// causes returns the function that makes a fadeError with a cause. A broken
// lost drops the cause.
func (p *palette) causes() []conform.Named[func(cause error) error] {
	fade := func(cause error) error { return &fadeError{cause: cause} }
	if p.broken == "lost" {
		fade = func(error) error { return &fadeError{} }
	}
	return []conform.Named[func(cause error) error]{{Value: fade, Name: "FadeError"}}
}

// members returns the colors, each as wrap makes it, named after its constant.
func members[E any](wrap func(c color) E) []conform.Named[E] {
	return []conform.Named[E]{
		{Value: wrap(red), Name: "Red"},
		{Value: wrap(green), Name: "Green"},
		{Value: wrap(blue), Name: "Blue"},
	}
}

// same returns c.
func same(c color) color { return c }

// encodes returns an application of EncodesEachVariant to the colors as wrap
// makes them.
func encodes[E interface {
	encoding.TextMarshaler
	fmt.Stringer
}](wrap func(c color) E) func(h *conform.Handle[*palette]) {
	return func(h *conform.Handle[*palette]) {
		probe.EncodesEachVariant(h, members(wrap), func(e E) string { return e.String() })
	}
}

// decodes returns an application of DecodesEachText to the colors as wrap
// makes them.
func decodes[E fmt.Stringer, P interface {
	*E
	encoding.TextUnmarshaler
}](wrap func(c color) E) func(h *conform.Handle[*palette]) {
	return func(h *conform.Handle[*palette]) {
		probe.DecodesEachText[*palette, E, P](h, members(wrap), func(e E) string { return e.String() })
	}
}

// either returns an application of good to a correct palette and of bad to a
// broken one.
func either(good, bad func(h *conform.Handle[*palette])) func(h *conform.Handle[*palette]) {
	return func(h *conform.Handle[*palette]) {
		if h.Subject().broken == "" {
			good(h)
			return
		}
		bad(h)
	}
}

func TestValues(t *testing.T) {
	t.Parallel()

	colors := members(same)
	lists := func(h *conform.Handle[*palette]) { probe.ListsTheMembers(h, colors, h.Subject().list) }
	names := func(h *conform.Handle[*palette]) { probe.NamesDiffer(h, colors, h.Subject().name, outside) }
	judges := func(h *conform.Handle[*palette]) { probe.JudgesValidity(h, colors, h.Subject().valid, outside) }
	parses := func(h *conform.Handle[*palette]) {
		probe.ParsesEachText(h, colors, h.Subject().parse, color.String)
	}
	messages := func(h *conform.Handle[*palette]) { probe.MessagesDiffer(h, h.Subject().values, "palette: ") }
	errs := func(h *conform.Handle[*palette]) {
		probe.ErrorsDiffer(h, h.Subject().values, h.Subject().neighbours)
	}
	typed := func(h *conform.Handle[*palette]) { probe.TypeMessage(h, h.Subject().types, "palette: ") }
	compares := func(h *conform.Handle[*palette]) {
		probe.ComparesWithIdentity(h, h.Subject().types, h.Subject().values)
	}
	tests := []struct {
		name         string
		giveProbe    spec.ProbeID
		giveApply    func(h *conform.Handle[*palette])
		giveBreak    string
		want         spec.ReasonID
		wantContract string
	}{
		{
			name: "ListsTheMembers fails a list in another order", giveProbe: spec.ListsTheMembers, giveApply: lists,
			giveBreak: "misordered", want: spec.MisorderedMembers, wantContract: "the probe holds",
		},
		{
			name: "NamesDiffer fails members with one text", giveProbe: spec.NamesDiffer, giveApply: names,
			giveBreak: "shared", want: spec.SharedText, wantContract: "the probe holds: Red and Green, Red and Blue",
		},
		{
			name: "NamesDiffer fails a member with the text of outside", giveProbe: spec.NamesDiffer, giveApply: names,
			giveBreak: "outside", want: spec.SharedText,
			wantContract: "the probe holds: Blue and the value outside the members",
		},
		{
			name: "JudgesValidity fails a refused member", giveProbe: spec.JudgesValidity, giveApply: judges,
			giveBreak: "strict", want: spec.MisjudgedValidity, wantContract: "the probe holds: Green",
		},
		{
			name: "JudgesValidity fails an accepted outside", giveProbe: spec.JudgesValidity, giveApply: judges,
			giveBreak: "lax", want: spec.MisjudgedValidity,
			wantContract: "the probe holds: the value outside the members",
		},
		{
			name: "ParsesEachText fails a text that parses to another member", giveProbe: spec.ParsesEachText,
			giveApply: parses, giveBreak: "misparsed", want: spec.UnparsedText, wantContract: "the probe holds: Blue",
		},
		{
			name: "ParsesEachText fails a parse that fails", giveProbe: spec.ParsesEachText, giveApply: parses,
			giveBreak: "unparsable", want: spec.UnparsedText, wantContract: "the probe holds: Red, Green, Blue",
		},
		{
			name: "ParsesEachText fails a text of no member that parses", giveProbe: spec.ParsesEachText,
			giveApply: parses, giveBreak: "lenient", want: spec.UnparsedText,
			wantContract: "the probe holds: the text red|green|blue|",
		},
		{
			name: "EncodesEachVariant fails a member that encodes another text", giveProbe: spec.EncodesEachVariant,
			giveApply: either(encodes(same), encodes(func(c color) misencoded { return misencoded{c} })),
			giveBreak: "misencoded", want: spec.MismarshalledText, wantContract: "the probe holds: Blue",
		},
		{
			name: "EncodesEachVariant fails an encoding that fails", giveProbe: spec.EncodesEachVariant,
			giveApply: either(encodes(same), encodes(func(c color) unencodable { return unencodable{c} })),
			giveBreak: "unencodable", want: spec.MismarshalledText, wantContract: "the probe holds: Red, Green, Blue",
		},
		{
			name: "DecodesEachText fails a text that decodes to another member", giveProbe: spec.DecodesEachText,
			giveApply: either(decodes(same), decodes(func(c color) misdecoded { return misdecoded{c} })),
			giveBreak: "misdecoded", want: spec.UnmarshalledWrong, wantContract: "the probe holds: Blue",
		},
		{
			name: "DecodesEachText fails a decoding that fails", giveProbe: spec.DecodesEachText,
			giveApply: either(decodes(same), decodes(func(c color) undecodable { return undecodable{c} })),
			giveBreak: "undecodable", want: spec.UnmarshalledWrong, wantContract: "the probe holds: Red, Green, Blue",
		},
		{
			name: "DecodesEachText fails a text of no member that decodes", giveProbe: spec.DecodesEachText,
			giveApply: either(decodes(same), decodes(func(c color) lenient { return lenient{c} })),
			giveBreak: "lenient", want: spec.UnmarshalledWrong,
			wantContract: "the probe holds: the text red|green|blue|",
		},
		{
			name:         "MessagesDiffer fails a message without the opening",
			giveProbe:    spec.MessagesDiffer,
			giveApply:    messages,
			giveBreak:    "unopened",
			want:         spec.WrongMessage,
			wantContract: "the probe holds: ErrWet",
		},
		{
			name: "MessagesDiffer fails a message that opens with another", giveProbe: spec.MessagesDiffer,
			giveApply: messages, giveBreak: "echoed", want: spec.WrongMessage,
			wantContract: "the probe holds: ErrWet and ErrDry",
		},
		{
			name: "ErrorsDiffer fails a value that matches another", giveProbe: spec.ErrorsDiffer, giveApply: errs,
			giveBreak: "aliased", want: spec.SharedIdentity, wantContract: "the probe holds: ErrWet and ErrDry",
		},
		{
			name:         "ErrorsDiffer fails a value that matches a neighbour",
			giveProbe:    spec.ErrorsDiffer,
			giveApply:    errs,
			giveBreak:    "neighbouring",
			want:         spec.SharedIdentity,
			wantContract: "the probe holds: ErrWet and ErrSpilled",
		},
		{
			name: "TypeMessage fails a message without the opening", giveProbe: spec.TypeMessage, giveApply: typed,
			giveBreak: "untitled", want: spec.WrongMessage, wantContract: "the probe holds: StainError",
		},
		{
			name: "ComparesWithIdentity fails an Is that disagrees", giveProbe: spec.ComparesWithIdentity,
			giveApply: compares, giveBreak: "blind", want: spec.DisagreeingIs,
			wantContract: "the probe holds: FadeError and ErrDry",
		},
		{
			name: "ReturnsTheCause fails a lost cause", giveProbe: spec.ReturnsTheCause,
			giveApply: func(h *conform.Handle[*palette]) { probe.ReturnsTheCause(h, h.Subject().causes()) },
			giveBreak: "lost", want: spec.LostCause, wantContract: "the probe holds: FadeError",
		},
		{
			name:         "TypesDiffer fails a type that matches another",
			giveProbe:    spec.TypesDiffer,
			giveApply:    func(h *conform.Handle[*palette]) { probe.TypesDiffer(h, h.Subject().types) },
			giveBreak:    "overlapping",
			want:         spec.SharedIdentity,
			wantContract: "the probe holds: FadeError and StainError",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			t.Run("passes a correct subject and fails a planted defect with its reason", func(t *testing.T) {
				t.Parallel()

				conform.Check[*palette]{
					ID: "Palette.Call/claim", Statement: "the probe holds", Claim: "claim", Probe: tt.giveProbe,
					Reason: tt.want, Apply: tt.giveApply, Defect: palettes(tt.giveBreak),
				}.Run(t, palettes(""))
			})

			t.Run("names the values at fault", func(t *testing.T) {
				t.Parallel()

				r := assert.NewRecorder()
				conform.Check[*palette]{
					ID: "Palette.Call/claim", Statement: "the probe holds", Probe: tt.giveProbe, Apply: tt.giveApply,
				}.Run(r, palettes(tt.giveBreak))
				failures := r.Failures()
				assert.NotEmpty(t, failures, "the probe fails the broken subject")
				assert.Equal(t, failures[0].Contract, tt.wantContract, "the contract names the values at fault")
			})
		})
	}

	t.Run("ComparesWithIdentity is vacuous on types without the method Is", func(t *testing.T) {
		t.Parallel()

		vacuous(t, spec.ComparesWithIdentity, func(h *conform.Handle[*palette]) {
			probe.ComparesWithIdentity(h, h.Subject().values, h.Subject().values)
		}, palettes(""), nil)
	})
}
