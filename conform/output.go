// Copyright Dokimasia B.V. 2026
// SPDX-License-Identifier: MIT

package conform

import (
	"context"
	"errors"

	"go.dokimi.dev/assert/history"
)

// Output is the result of a call that the history of a case records. A
// failed call has no values in its Output, because a value beside an error
// has no meaning.
type Output struct {
	// Values are the values that the callable returned in order. They are nil
	// for a call that failed.
	Values []any

	// Failed reports a call that returned an error.
	Failed bool
}

// OutputOf returns the Output of a call that returned values and err.
func OutputOf(err error, values ...any) Output {
	if err != nil {
		return Output{Failed: true}
	}
	return Output{Values: values}
}

// Complete completes call in the history of a case. It records a call whose
// err wraps the error of the ended context ctx as Unknown. It records every
// other call as OK with the Output that [OutputOf] returns for err and
// values. An error that a call returns is an observation of the subject.
func Complete(ctx context.Context, call history.Call, err error, values ...any) {
	if err != nil && errors.Is(err, ctx.Err()) {
		call.Unknown(err)
		return
	}
	call.OK(OutputOf(err, values...))
}
