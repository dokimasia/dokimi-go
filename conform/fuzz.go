// Copyright Dokimasia B.V. 2026
// SPDX-License-Identifier: MIT

package conform

import (
	"fmt"
	"slices"
	"sync/atomic"
	"testing"

	"go.dokimi.dev/assert/prop"
)

// Fuzz registers on f the fuzz target of the property checks of the callable,
// for subjects of newSubject. A generated suite calls it in
// Fuzz<Subject><Callable>, and in Fuzz<Function> for the suite of a function.
//
// For each input of the fuzzer, the case draws the identity of one property
// check of the callable under the label check. The checks are those of the
// suite and those that [With] adds, without the checks that the options
// excuse. The case runs the property of the check on a handle of the case,
// with the overrides of the suite. A failing input shrinks and reports as a
// failing case of [prop.Fuzz] does. go test without -fuzz runs the seed
// corpus of f alone.
//
// Fuzz reports every problem of the checks and the options on f, and stops f
// when it finds one. It skips f when the callable has no property check that
// the options do not excuse. Fuzz runs no ceiling of a budget, because a
// count of allocations or a latency of the fuzzer measures its
// instrumentation.
func Fuzz[S any](
	f *testing.F, suite Suite[S], callable Callable[S], newSubject func(h *Handle[S]) S, opts ...Option[S],
) {
	f.Helper()
	cfg := configure(opts)
	cfg.using = suite.Using
	stopAt(f, validate("Fuzz"+suite.Declaration+"Suite", suite, cfg))
	var ids []ID[S]
	checks := make(map[ID[S]]*Check[S])
	for _, c := range append(slices.Clip(suite.Checks), cfg.checks...) {
		if _, excused := cfg.excused(c.ID); c.Property == nil || c.ID.callable() != callable || excused {
			continue
		}
		ids = append(ids, c.ID)
		checks[c.ID] = &c
	}
	if len(ids) == 0 {
		f.Skipf("%s has no property check that the application does not excuse", callable)
	}
	prop.Fuzz(f, fmt.Sprintf("%s passes each of its property checks", callable), func(pc *prop.Case) {
		c := checks[pc.Draw(prop.SampledFrom(ids...), "check")]
		c.Property(newHandle(pc, c, newSubject, cfg, new(atomic.Pointer[string])), pc)
	}, cfg.using...)
}
