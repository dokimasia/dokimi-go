// Copyright Dokimasia B.V. 2026
// SPDX-License-Identifier: MIT

package conform

// Suite contains the checks of one declaration whose subjects have the type
// S.
type Suite[S any] struct {
	// Declaration is the name of the declaration, such as Store.
	Declaration string

	// Checks are the checks of the declaration.
	Checks []Check[S]
}

// Option configures an application of a suite. The zero Option configures
// nothing.
type Option[S any] struct {
	set func(c *config[S])
}

// With adds the checks to an application. The application falsifies each
// added check before it applies the check to the subject.
func With[S any](checks ...Check[S]) Option[S] {
	return Option[S]{set: func(c *config[S]) { c.checks = append(c.checks, checks...) }}
}

// Excuse excuses the check with the identity id from an application. The
// application skips the check with the reason. It fails when the identity
// matches no check of the application, and when the reason is empty.
func Excuse[S any](id ID[S], reason string) Option[S] {
	return Option[S]{set: func(c *config[S]) { c.excusals = append(c.excusals, excusal[S]{id: id, reason: reason}) }}
}

// Reference supplies the reference implementations of an application.
// [Handle.Reference] returns a fresh one from newReference.
func Reference[S any](newReference func(h *Handle[S]) S) Option[S] {
	return Option[S]{set: func(c *config[S]) { c.reference = newReference }}
}

// Second supplies the second subjects of an application. [Handle.Second]
// returns a fresh one from newSecond.
func Second[S any](newSecond func(h *Handle[S]) S) Option[S] {
	return Option[S]{set: func(c *config[S]) { c.second = newSecond }}
}

// Restartable supplies the restart of the subjects of an application.
// [Handle.Restart] returns the subject that restart returns.
func Restartable[S any](restart func(h *Handle[S], s S) S) Option[S] {
	return Option[S]{set: func(c *config[S]) { c.restart = restart }}
}

// config is the configuration that the options of an application set.
type config[S any] struct {
	// reference, second and restart are the doors of the handle. A door
	// without its option is nil.
	reference func(h *Handle[S]) S
	second    func(h *Handle[S]) S
	restart   func(h *Handle[S], s S) S

	// checks are the checks that With adds.
	checks []Check[S]

	// excusals are the excusals of Excuse in the order of the options.
	excusals []excusal[S]
}

// excusal is the excusal of the check with the identity id.
type excusal[S any] struct {
	id     ID[S]
	reason string
}

// configure returns the configuration that opts set.
func configure[S any](opts []Option[S]) *config[S] {
	c := &config[S]{}
	for _, o := range opts {
		if o.set != nil {
			o.set(c)
		}
	}
	return c
}
