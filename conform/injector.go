// Copyright Dokimasia B.V. 2026
// SPDX-License-Identifier: MIT

package conform

import (
	"errors"
	"slices"
	"sync"

	"go.dokimi.dev/fault"
)

// ErrInjected is the error of a call of a dependency that an [Injector]
// failed. The probes of the machinery call-that-fails-on-demand inject it.
var ErrInjected = errors.New("conform: an injected failure of a dependency")

// Injector fails the calls of the dependencies of the subjects of one handle
// on the request of a probe, and records each call of a dependency. The
// consumer builds each subject over dependencies of the injector: through
// [FaultOf] for a double, and through [Dependency.Call] for a dependency that
// the consumer writes. The zero Injector fails no call.
//
// # Concurrency
//
// Its methods are safe for concurrent use.
type Injector struct {
	// at contains the error of each call that FailNext and FailAt armed, by
	// the position of the call from 1.
	at map[int]error

	// until is the error of every call that FailUntilHealed armed, and nil
	// without one.
	until error

	// held is open while Hold keeps the calls waiting, and nil otherwise.
	held chan struct{}

	calls        []DependencyCall
	dependencies int
	mu           sync.Mutex
}

// DependencyCall is one call of a dependency that an injector recorded.
type DependencyCall struct {
	// Callable is the callable of the dependency, such as Get.
	Callable string

	// Dependency is the number of the dependency, from 1.
	Dependency int

	// Failed reports that the injector failed the call.
	Failed bool
}

// FailNext makes the next call of a dependency fail with err.
func (i *Injector) FailNext(err error) {
	i.FailAt(1, err)
}

// FailAt makes the n-th call of a dependency from now fail with err, so
// FailAt(1, err) arms the failure that FailNext(err) arms.
func (i *Injector) FailAt(n int, err error) {
	i.mu.Lock()
	defer i.mu.Unlock()
	if i.at == nil {
		i.at = map[int]error{}
	}
	i.at[len(i.calls)+n] = err
}

// FailUntilHealed makes every call of a dependency fail with err until Heal.
// It takes precedence over FailNext and FailAt.
func (i *Injector) FailUntilHealed(err error) {
	i.mu.Lock()
	defer i.mu.Unlock()
	i.until = err
}

// Heal clears every failure that FailNext, FailAt and FailUntilHealed armed.
func (i *Injector) Heal() {
	i.mu.Lock()
	defer i.mu.Unlock()
	i.at, i.until = nil, nil
}

// Hold makes each call of a dependency wait until Release. The injector
// records a call before the call waits.
func (i *Injector) Hold() {
	i.mu.Lock()
	defer i.mu.Unlock()
	if i.held == nil {
		i.held = make(chan struct{})
	}
}

// Release lets each call that Hold keeps waiting go on, and every later call
// pass without waiting.
func (i *Injector) Release() {
	i.mu.Lock()
	defer i.mu.Unlock()
	if i.held != nil {
		close(i.held)
		i.held = nil
	}
}

// Failures returns the number of calls of a dependency that the injector
// failed.
func (i *Injector) Failures() int {
	i.mu.Lock()
	defer i.mu.Unlock()
	failed := 0
	for _, c := range i.calls {
		if c.Failed {
			failed++
		}
	}
	return failed
}

// Calls returns each call of a dependency, in the order of the calls.
func (i *Injector) Calls() []DependencyCall {
	i.mu.Lock()
	defer i.mu.Unlock()
	return slices.Clone(i.calls)
}

// Dependency returns a new dependency of the injector, such as one instance
// behind a balancer. Each dependency has a number of its own, from 1.
func (i *Injector) Dependency() *Dependency {
	i.mu.Lock()
	defer i.mu.Unlock()
	i.dependencies++
	return &Dependency{injector: i, number: i.dependencies}
}

// call records a call of the callable of the dependency with the number, and
// returns the error that the injector armed for it. It waits until Release
// after it records a call that Hold keeps waiting.
func (i *Injector) call(dependency int, callable string) error {
	i.mu.Lock()
	position := len(i.calls) + 1
	err := i.until
	if err == nil {
		err = i.at[position]
	}
	i.calls = append(i.calls, DependencyCall{Dependency: dependency, Callable: callable, Failed: err != nil})
	held := i.held
	i.mu.Unlock()
	if held != nil {
		<-held
	}
	return err
}

// Dependency is one dependency of the subjects of an [Injector].
type Dependency struct {
	injector *Injector
	number   int
}

// Call records a call of the callable of the dependency, such as Get, and
// returns the error that the call fails with, or nil for a call that
// succeeds. A dependency that the consumer writes calls it at the start of
// each call.
func (d *Dependency) Call(callable string) error {
	return d.injector.call(d.number, callable)
}

// FaultOf returns the fault strategy of the method callable of a double of the
// dependency d. The consumer arms it on the method through SetFault, so each
// call of the method calls d.Call:
//
//	store := inj.Dependency()
//	backing.OnGet.SetFault(conform.FaultOf[storetest.StoreGetCall](store, "Get"))
func FaultOf[C any](d *Dependency, callable string) fault.Fault[C] {
	return injected[C]{dependency: d, callable: callable}
}

// injected is the fault strategy of one method of a double of a dependency.
type injected[C any] struct {
	dependency *Dependency
	callable   string
}

// ShouldTrigger records the call of the method, and reports whether the
// injector fails it, with its error.
func (f injected[C]) ShouldTrigger(C, fault.Clock) (bool, error) {
	err := f.dependency.Call(f.callable)
	return err != nil, err
}

// Reset does nothing, because the injector keeps the state of every fault.
func (injected[C]) Reset() {}
