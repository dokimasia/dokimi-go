// Copyright Dokimasia B.V. 2026
// SPDX-License-Identifier: MIT

package conform

import (
	"go.dokimi.dev/fault"
	"go.dokimi.dev/host"
)

// ErrInjected is the error of a call of a dependency that an [Injector]
// failed, host.ErrInjected. The probes of the machinery
// call-that-fails-on-demand inject it.
var ErrInjected = host.ErrInjected

// Injector fails the calls of the dependencies of the subjects of one handle
// on the request of a probe, and records each call of a dependency. It is the
// injector of the host package, so the world of a subject fails the
// dependencies of an incarnation through the same type.
type Injector = host.Injector

// Dependency is one dependency of the subjects of an [Injector].
type Dependency = host.Dependency

// DependencyCall is one call of a dependency that an injector recorded.
type DependencyCall = host.DependencyCall

// FaultOf returns the fault strategy of the method callable of a double of the
// dependency d, as host.FaultOf does. The consumer arms it on the method
// through SetFault, so each call of the method calls d.Call:
//
//	store := inj.Dependency()
//	backing.OnGet.SetFault(conform.FaultOf[storetest.StoreGetCall](store, "Get"))
func FaultOf[C any](d *Dependency, callable string) fault.Fault[C] {
	return host.FaultOf[C](d, callable)
}
