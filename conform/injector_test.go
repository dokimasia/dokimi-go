// Copyright Dokimasia B.V. 2026
// SPDX-License-Identifier: MIT

package conform_test

import (
	"errors"
	"testing"

	"go.dokimi.dev/assert"
	"go.dokimi.dev/assert/expect"

	"go.dokimi.dev/conform"
	"go.dokimi.dev/host"
)

// errBacking is the error that the specs of the injector arm on a dependency.
var errBacking = errors.New("the backing store failed")

// TestInjector checks the names of the injector of conform, which are the
// names of the host package.
func TestInjector(t *testing.T) {
	t.Parallel()

	t.Run("ErrInjected", func(t *testing.T) {
		t.Parallel()

		t.Run("is the error of the host package", func(t *testing.T) {
			t.Parallel()

			assert.ErrorIs(t, conform.ErrInjected, host.ErrInjected, "one error of an injected failure")
		})
	})

	t.Run("FaultOf", func(t *testing.T) {
		t.Parallel()

		t.Run("fails the calls of the method that the injector fails", func(t *testing.T) {
			t.Parallel()

			inj := &conform.Injector{}
			f := conform.FaultOf[string](inj.Dependency(), "Get")
			inj.FailNext(errBacking)
			failed, err := f.ShouldTrigger("key", nil)
			passed, none := f.ShouldTrigger("key", nil)

			expect.True(t, failed, "the first call fails")
			expect.ErrorIs(t, err, errBacking, "the first call fails with the armed error")
			expect.False(t, passed, "the second call passes")
			expect.NoError(t, none, "the second call has no error")
			expect.Equal(t, inj.Calls(), []conform.DependencyCall{
				{Dependency: 1, Callable: "Get", Failed: true},
				{Dependency: 1, Callable: "Get"},
			}, "the injector records each call of the method")
		})
	})
}
