// Copyright Dokimasia B.V. 2026
// SPDX-License-Identifier: MIT

package conform_test

import (
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"testing/synctest"

	"go.dokimi.dev/assert"
	"go.dokimi.dev/assert/expect"

	"go.dokimi.dev/conform"
)

// errBacking and errStale are the errors that the specs of the injector arm on
// a dependency.
var (
	errBacking = errors.New("the backing store failed")
	errStale   = errors.New("the replica is stale")
)

// callErrors calls the callable Get of d n times and returns the error of
// each call.
func callErrors(d *conform.Dependency, n int) []error {
	out := make([]error, 0, n)
	for range n {
		out = append(out, d.Call("Get"))
	}
	return out
}

func TestInjector(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		giveArm  func(inj *conform.Injector)
		wantErrs []error
	}{
		{
			name:     "fails nothing before a failure is armed",
			giveArm:  func(*conform.Injector) {},
			wantErrs: []error{nil, nil, nil},
		},
		{
			name:     "fails the next call alone after FailNext",
			giveArm:  func(inj *conform.Injector) { inj.FailNext(errBacking) },
			wantErrs: []error{errBacking, nil, nil},
		},
		{
			name:     "fails the n-th call from now after FailAt",
			giveArm:  func(inj *conform.Injector) { inj.FailAt(2, errBacking) },
			wantErrs: []error{nil, errBacking, nil},
		},
		{
			name: "fails each call that FailNext and FailAt armed",
			giveArm: func(inj *conform.Injector) {
				inj.FailNext(errBacking)
				inj.FailAt(3, errStale)
			},
			wantErrs: []error{errBacking, nil, errStale},
		},
		{
			name:     "fails every call after FailUntilHealed",
			giveArm:  func(inj *conform.Injector) { inj.FailUntilHealed(errBacking) },
			wantErrs: []error{errBacking, errBacking, errBacking},
		},
		{
			name: "fails with the error of FailUntilHealed where FailAt armed another",
			giveArm: func(inj *conform.Injector) {
				inj.FailAt(1, errStale)
				inj.FailUntilHealed(errBacking)
			},
			wantErrs: []error{errBacking, errBacking, errBacking},
		},
		{
			name: "fails nothing after Heal",
			giveArm: func(inj *conform.Injector) {
				inj.FailNext(errBacking)
				inj.FailAt(3, errBacking)
				inj.FailUntilHealed(errBacking)
				inj.Heal()
			},
			wantErrs: []error{nil, nil, nil},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			inj := &conform.Injector{}
			d := inj.Dependency()
			tt.giveArm(inj)

			assert.Equal(t, callErrors(d, len(tt.wantErrs)), tt.wantErrs, "each call fails as the injector armed it")
		})
	}

	t.Run("Failures", func(t *testing.T) {
		t.Parallel()

		t.Run("counts the calls that the injector failed", func(t *testing.T) {
			t.Parallel()

			inj := &conform.Injector{}
			d := inj.Dependency()
			inj.FailAt(2, errBacking)
			callErrors(d, 3)
			inj.FailNext(errBacking)
			callErrors(d, 1)

			assert.Equal(t, inj.Failures(), 2, "two calls failed")
			assert.Equal(t, inj.Failures(), 2, "a second count counts the same calls")
		})
	})

	t.Run("Calls", func(t *testing.T) {
		t.Parallel()

		t.Run("records each call of a dependency in the order of the calls", func(t *testing.T) {
			t.Parallel()

			inj := &conform.Injector{}
			primary, replica := inj.Dependency(), inj.Dependency()
			inj.FailAt(2, errBacking)
			_ = primary.Call("Get")
			_ = replica.Call("Put")
			_ = primary.Call("Put")

			assert.Equal(t, inj.Calls(), []conform.DependencyCall{
				{Dependency: 1, Callable: "Get"},
				{Dependency: 2, Callable: "Put", Failed: true},
				{Dependency: 1, Callable: "Put"},
			}, "the injector records each call with its dependency and its callable")
		})

		t.Run("returns a copy of the record", func(t *testing.T) {
			t.Parallel()

			inj := &conform.Injector{}
			_ = inj.Dependency().Call("Get")
			calls := inj.Calls()
			calls[0].Callable = "Put"

			assert.Equal(t, inj.Calls()[0].Callable, "Get", "a change of the copy leaves the record")
		})
	})

	t.Run("Hold", func(t *testing.T) {
		t.Parallel()

		t.Run("keeps each call of a dependency waiting until Release", func(t *testing.T) {
			t.Parallel()

			synctest.Test(t, func(t *testing.T) {
				inj := &conform.Injector{}
				d := inj.Dependency()
				inj.Hold()
				var returned sync.WaitGroup
				var done []bool
				var mu sync.Mutex
				for range 2 {
					returned.Go(func() {
						_ = d.Call("Get")
						mu.Lock()
						done = append(done, true)
						mu.Unlock()
					})
				}
				synctest.Wait()

				mu.Lock()
				waiting := len(done)
				mu.Unlock()
				expect.Equal(t, waiting, 0, "no held call returns before Release")
				expect.Equal(t, len(inj.Calls()), 2, "the injector records each held call before it waits")
				inj.Release()
				returned.Wait()
				expect.Equal(t, len(done), 2, "each held call returns after Release")
			})
		})

		t.Run("lets every call pass without waiting after Release", func(t *testing.T) {
			t.Parallel()

			inj := &conform.Injector{}
			d := inj.Dependency()
			inj.Hold()
			inj.Release()

			assert.NoError(t, d.Call("Get"), "a call after Release returns")
		})

		t.Run("keeps one wait for two calls of Hold", func(t *testing.T) {
			t.Parallel()

			synctest.Test(t, func(t *testing.T) {
				inj := &conform.Injector{}
				d := inj.Dependency()
				inj.Hold()
				var returned sync.WaitGroup
				returned.Go(func() { _ = d.Call("Get") })
				synctest.Wait()
				inj.Hold()
				inj.Release()
				returned.Wait()
				assert.Length(t, inj.Calls(), 1, "one Release ends the wait of a call between two calls of Hold")
			})
		})

		t.Run("keeps the calls waiting again at a Hold after a Release", func(t *testing.T) {
			t.Parallel()

			synctest.Test(t, func(t *testing.T) {
				inj := &conform.Injector{}
				d := inj.Dependency()
				inj.Hold()
				inj.Release()
				inj.Hold()
				var returned atomic.Bool
				var calls sync.WaitGroup
				calls.Go(func() {
					_ = d.Call("Get")
					returned.Store(true)
				})
				synctest.Wait()
				expect.False(t, returned.Load(), "the call waits after the second Hold")
				inj.Release()
				calls.Wait()
				expect.True(t, returned.Load(), "the call returns after the second Release")
			})
		})

		t.Run("does nothing at a Release without a Hold", func(t *testing.T) {
			t.Parallel()

			inj := &conform.Injector{}
			inj.Release()

			assert.NoError(t, inj.Dependency().Call("Get"), "a call returns")
		})
	})

	t.Run("Dependency", func(t *testing.T) {
		t.Parallel()

		t.Run("numbers each dependency from 1", func(t *testing.T) {
			t.Parallel()

			inj := &conform.Injector{}
			for range 3 {
				_ = inj.Dependency().Call("Get")
			}

			assert.Equal(t, inj.Calls(), []conform.DependencyCall{
				{Dependency: 1, Callable: "Get"},
				{Dependency: 2, Callable: "Get"},
				{Dependency: 3, Callable: "Get"},
			}, "each dependency has a number of its own")
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

		t.Run("keeps the armed failure at Reset", func(t *testing.T) {
			t.Parallel()

			inj := &conform.Injector{}
			f := conform.FaultOf[string](inj.Dependency(), "Get")
			inj.FailUntilHealed(errBacking)
			f.Reset()
			failed, _ := f.ShouldTrigger("key", nil)

			assert.True(t, failed, "the call after Reset fails")
		})
	})

	t.Run("records the calls of concurrent dependencies", func(t *testing.T) {
		t.Parallel()

		inj := &conform.Injector{}
		inj.FailUntilHealed(errBacking)
		var calls sync.WaitGroup
		for range 8 {
			d := inj.Dependency()
			calls.Go(func() { _ = d.Call("Get") })
		}
		calls.Wait()

		assert.Equal(t, inj.Failures(), 8, "the injector counts each concurrent call")
	})
}
