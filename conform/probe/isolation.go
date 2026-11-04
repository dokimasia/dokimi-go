// Copyright Dokimasia B.V. 2026
// SPDX-License-Identifier: MIT

package probe

import (
	"slices"

	"go.dokimi.dev/assert"
	"go.dokimi.dev/assert/history"
	"go.dokimi.dev/assert/prop"
	"go.dokimi.dev/assert/stateful"

	"go.dokimi.dev/conform"
	"go.dokimi.dev/spec"
)

// txnOperation is the name of the action of a transaction among the actions
// of a declaration, and the operation of each of its calls in the history.
const txnOperation = "txn"

// SerializableHistory runs generated sequences of the action txn of the
// actions on the subject of h, with a concurrent section on the clients 0 to
// clients that the schedule of h runs. After the last step it checks the
// transactions of the history of c with history.Serializable. It engages h
// when a transaction commits.
//
// The action txn of the actions of a declaration is never enabled, so every
// other probe of generated sequences leaves it out. The probe runs the action
// alone, without its Enabled.
func SerializableHistory[S any](h *conform.Handle[S], c *prop.Case, actions Actions[S], clients int) {
	h.Helper()
	isolated(h, c, actions, clients, spec.NotSerializable, history.Serializable)
}

// SnapshotHistory runs the sequences of SerializableHistory, and checks the
// transactions of the history of c with history.HasSnapshotIsolation.
func SnapshotHistory[S any](h *conform.Handle[S], c *prop.Case, actions Actions[S], clients int) {
	h.Helper()
	isolated(h, c, actions, clients, spec.NotSnapshotIsolated, history.HasSnapshotIsolation)
}

// isolated runs the sequences of the action txn of actions, and checks the
// history of c with check under the reason. A case without a committed
// transaction, and actions without the action txn, do not engage h.
func isolated[S any](
	h *conform.Handle[S], c *prop.Case, actions Actions[S], clients int, reason spec.ReasonID,
	check func(tb assert.TB, h *history.History, contract string),
) {
	h.Helper()
	steps := actions(c, h.Subject)
	at := slices.IndexFunc(steps, func(a stateful.Action[struct{}]) bool { return a.Name == txnOperation })
	if at < 0 {
		return
	}
	txn := steps[at]
	txn.Enabled = nil
	stateful.Steps(c, stateful.Machine[struct{}]{Actions: []stateful.Action[struct{}]{txn}}, stateful.Clients(clients),
		h.Schedule(c), stateful.Swarm(false))
	if !slices.ContainsFunc(c.History().Events(), func(e history.Event) bool { return e.Kind == history.OK }) {
		return
	}
	h.Engage()
	h.Expect(reason)
	check(h, c.History(), h.Statement())
}
