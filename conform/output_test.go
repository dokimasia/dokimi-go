// Copyright Dokimasia B.V. 2026
// SPDX-License-Identifier: MIT

package conform_test

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"go.dokimi.dev/assert"
	"go.dokimi.dev/assert/expect"
	"go.dokimi.dev/assert/history"

	"go.dokimi.dev/conform"
)

// errRefused is an error of a call that the context of the call does not
// cause.
var errRefused = errors.New("conform_test: refused")

func TestOutput(t *testing.T) {
	t.Parallel()

	t.Run("Complete", func(t *testing.T) {
		t.Parallel()

		tests := []struct {
			name          string
			giveCancelled bool
			giveErr       error
			give          []any
			wantKind      history.Kind
			wantOut       any
		}{
			{
				name: "records the values of a call that succeeded", give: []any{1, "a"},
				wantKind: history.OK, wantOut: conform.Output{Values: []any{1, "a"}},
			},
			{
				name: "records a call that failed without its values", giveErr: errRefused, give: []any{1},
				wantKind: history.OK, wantOut: conform.Output{Failed: true},
			},
			{
				name:          "records a call whose error wraps the error of its cancelled context as unknown",
				giveCancelled: true, giveErr: fmt.Errorf("get: %w", context.Canceled), give: []any{1},
				wantKind: history.Unknown,
			},
			{
				name:          "records a call under a cancelled context whose error wraps another error as failed",
				giveCancelled: true, giveErr: errRefused, give: []any{1},
				wantKind: history.OK, wantOut: conform.Output{Failed: true},
			},
			{
				name:    "records a call under a live context whose error wraps context.Canceled as failed",
				giveErr: fmt.Errorf("get: %w", context.Canceled), give: []any{1},
				wantKind: history.OK, wantOut: conform.Output{Failed: true},
			},
		}
		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				t.Parallel()
				ctx, cancel := context.WithCancel(t.Context())
				defer cancel()
				if tt.giveCancelled {
					cancel()
				}
				h := history.New()
				call := h.Invoke(0, "Get", nil)

				conform.Complete(ctx, call, tt.giveErr, tt.give...)

				events := h.Events()
				assert.Length(t, events, 2, "the history has the invocation and the completion of the call")
				expect.Equal(t, events[1].Kind, tt.wantKind, "the completion has the kind of the outcome")
				expect.Equal(t, events[1].Output, tt.wantOut, "the completion records the output of the call")
			})
		}
	})

	t.Run("OutputOf", func(t *testing.T) {
		t.Parallel()

		tests := []struct {
			name    string
			giveErr error
			give    []any
			want    conform.Output
		}{
			{
				name: "returns the values of a call that succeeded", give: []any{1, "a"},
				want: conform.Output{Values: []any{1, "a"}},
			},
			{
				name: "returns a failed output without the values of a call that failed", giveErr: errRefused,
				give: []any{1}, want: conform.Output{Failed: true},
			},
		}
		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				t.Parallel()

				got := conform.OutputOf(tt.giveErr, tt.give...)

				expect.Equal(t, got, tt.want, "the output records the outcome of the call")
			})
		}
	})
}
