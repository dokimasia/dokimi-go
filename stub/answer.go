// Copyright Dokimasia B.V. 2026
// SPDX-License-Identifier: MIT

package stub

// Arms are the sources of the results of one call of a method, which [Answer]
// tries in order. C is the type of the calls of the method, and R is the type
// of its results, which a generated double declares for each method because
// Go has no type of a tuple.
type Arms[C, R any] struct {
	// Invoke calls the function of the method, and is nil where the test set
	// none. Answer tells an unset function from a function that returns the
	// zero results by the nil.
	Invoke func() R
	// Fallback is the fixed result of the method, and nil where the test set
	// none.
	Fallback *R
	// Default returns the default results of the method, and is nil where
	// the default results are the zero results. A generated double sets it
	// where a result of the method has a builder, so each call gets a new
	// value from the builder.
	Default func() R
	// Fault returns the results with err in the error result of the method.
	// It is nil for a method without an error result.
	Fault func(err error) R
	// Stamp copies the results onto the recorded call, so the log contains
	// the results of each call.
	Stamp func(call *C, result R)
}

// Answer returns the results of one call of the method of s, and records the
// call. The results are those of the first arm that applies:
//
//  1. The fault that the test armed, where it fires.
//  2. The function of the method.
//  3. The fixed result of the method.
//  4. The default results of the method.
//  5. The zero results.
//
// Answer first sleeps the latency of s, so a test that drives a clock sees
// the call blocked where a real implementation is. It records the call on
// each arm, after it stamps the results onto it. A fault that fires on a
// method without an error result stops the test through
// [MethodStub.FailUnfaultable]. A call that the test did not configure
// stops the test in strict mode through [MethodStub.FailUnexpectedCall], and
// still returns the default or the zero results.
func Answer[C, R any](s *MethodStub[C], call *C, arms Arms[C, R]) R {
	s.SleepLatency()

	if fired, err := s.ShouldFaultFor(*call); fired {
		if arms.Fault != nil {
			return answer(s, call, arms, arms.Fault(err))
		}
		s.FailUnfaultable(*call, err)
	}
	if arms.Invoke != nil {
		return answer(s, call, arms, arms.Invoke())
	}
	if arms.Fallback != nil {
		return answer(s, call, arms, *arms.Fallback)
	}
	s.FailUnexpectedCall(*call)
	if arms.Default != nil {
		return answer(s, call, arms, arms.Default())
	}
	var zero R
	return answer(s, call, arms, zero)
}

// answer stamps result onto call, records the call and returns result.
func answer[C, R any](s *MethodStub[C], call *C, arms Arms[C, R], result R) R {
	if arms.Stamp != nil {
		arms.Stamp(call, result)
	}
	s.Record(*call)
	return result
}
