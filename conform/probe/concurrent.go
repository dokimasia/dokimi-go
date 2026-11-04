// Copyright Dokimasia B.V. 2026
// SPDX-License-Identifier: MIT

package probe

import (
	"cmp"
	"reflect"
	"slices"
	"sync"
	"sync/atomic"
	"testing/synctest"

	"go.dokimi.dev/assert"
	"go.dokimi.dev/assert/history"
	"go.dokimi.dev/assert/prop"
	"go.dokimi.dev/assert/stateful"
	"go.dokimi.dev/spec"

	"go.dokimi.dev/conform"
)

// outcomeField is the field of the record of a check of history.Linearizable
// that states how the check ended, as a history.Verdict.
const outcomeField = "outcome"

// sectionMean is the mean number of steps of a concurrent section of
// [PinnedUnderConcurrentWrites] and [VersionsFollowTheCausalOrder]. A racy
// read shows in those probes only where reads of two keys overlap, and the
// default mean of 5 steps lets few reads overlap.
const sectionMean = 16

// LinearizableHistory runs generated sequences of the actions on the subject
// of h, with a concurrent section on the clients 0 to clients that the
// schedule of h runs. Each case keeps every action, so each case can show a
// race between two actions. After the last step it checks the history of c
// once with history.Linearizable and opts, against the spec that
// history.SpecFrom builds over fresh references of h through reference. The
// check splits the history by the keys of its calls. It fails when the calls
// have no order in which the reference returns the same results. A history
// without a call, and a history that the checker cannot decide within its
// budget, do not engage h. A fault of the checker, such as a reference that
// panics, ends the check with that fault.
func LinearizableHistory[S any](
	h *conform.Handle[S], c *prop.Case, actions Actions[S], reference Reference[S], clients int,
	opts ...history.Option,
) {
	h.Helper()
	stateful.Steps(c, stateful.Machine[struct{}]{Actions: actions(c, h.Subject)}, stateful.Clients(clients),
		h.Schedule(c), stateful.Swarm(false))
	sequential := history.SpecFrom(func() history.Subject {
		apply := reference(h.Reference())
		return func(operation string, args []any) any { return apply(h.Context(), operation, args) }
	})
	r := assert.NewRecorder().WithContext(h.Context())
	history.Linearizable(r, c.History(), sequential, h.Statement(), opts...)
	if faults := r.Faults(); len(faults) > 0 {
		assert.Fault(h, faults[0])
		return
	}
	failures := r.Failures()
	if len(c.History().Events()) == 0 || len(failures) > 0 && failures[0].Detail[outcomeField] == history.Undecided {
		return
	}
	h.Engage()
	h.Expect(spec.NotLinearizable)
	for _, f := range failures {
		h.Report(f, true)
	}
}

// ConcurrentReadsAgree runs generated sequences of the actions on the subject
// of h on client 0, so the subject has state, and then a concurrent section of
// the action read alone on the clients 0 to clients, which the schedule of h
// runs. Each case keeps every action. After the section it calls again with
// the arguments of each read that a client other than 0 completed, and fails
// when the output differs. Each other action runs on client 0 alone, because
// Steps leaves an action with Enabled out of a concurrent section. A case
// without a completed read on a client other than 0 does not engage h.
func ConcurrentReadsAgree[S any](
	h *conform.Handle[S], c *prop.Case, actions Actions[S], read string, again Apply, clients int,
) {
	h.Helper()
	steps := actions(c, h.Subject)
	alone(steps, read)
	stateful.Steps(c, stateful.Machine[struct{}]{Actions: steps}, stateful.Clients(clients), h.Schedule(c),
		stateful.Swarm(false))
	events := c.History().Events()
	for i := range events {
		e := &events[i]
		if e.Kind != history.OK || e.Client == 0 || events[e.Call].Operation != read {
			continue
		}
		h.Engage()
		h.Expect(spec.InconsistentConcurrentRead)
		got, _ := e.Output.(conform.Output)
		assert.Equal(h, got, again(h.Context(), events[e.Call].Args), h.Statement())
	}
}

// PinnedUnderConcurrentWrites runs generated sequences of the actions on the
// subject of h, with a concurrent section of the actions read and write on
// the clients 0 to clients, which the schedule of h runs. The section
// averages 16 steps. Each other action runs on client 0 alone. After the last
// step it compares each read that succeeds with the first read that succeeded
// with the same arguments, and fails when the outputs differ. A case without a
// read that succeeds after another read with the same arguments does not
// engage h.
func PinnedUnderConcurrentWrites[S any](
	h *conform.Handle[S], c *prop.Case, actions Actions[S], read, write string, clients int,
) {
	h.Helper()
	steps := actions(c, h.Subject)
	alone(steps, read, write)
	stateful.Steps(c, stateful.Machine[struct{}]{Actions: steps}, stateful.Clients(clients), h.Schedule(c),
		stateful.Swarm(false), stateful.ConcurrentMean(sectionMean))
	events := c.History().Events()
	var pins []*history.Event
	for i := range events {
		e := &events[i]
		got, _ := e.Output.(conform.Output)
		if e.Kind != history.OK || events[e.Call].Operation != read || got.Failed {
			continue
		}
		at := slices.IndexFunc(pins, func(p *history.Event) bool {
			return reflect.DeepEqual(events[p.Call].Args, events[e.Call].Args)
		})
		if at < 0 {
			pins = append(pins, e)
			continue
		}
		h.Engage()
		h.Expect(spec.UnpinnedRead)
		pinned, _ := pins[at].Output.(conform.Output)
		assert.Equal(h, got, pinned, h.Statement())
	}
}

// VersionsFollowTheCausalOrder runs generated sequences of the actions on the
// subject of h, with a concurrent section of the actions read and write on
// the clients 0 to clients, which the schedule of h runs. The section
// averages 16 steps. Each other action runs on client 0 alone. After the last
// step it orders the calls of read and write that succeed. A call follows each
// call of its client before it. A read follows the write with the same keys
// and the version that the read returns, where that write started before the
// read returned and no other write with the same keys has that version. The
// probe fails when a write returns a version that is not above the version of
// a call with the same keys that it follows, and when a read returns a version
// below it. It also fails when a read that follows a write with the same keys
// returns a version that no write with those keys returned. Each call of write
// returns the value that it wrote. A case without a call that follows a call
// with the same keys does not engage h.
func VersionsFollowTheCausalOrder[S, V any, O cmp.Ordered](
	h *conform.Handle[S], c *prop.Case, actions Actions[S], read, write string, version func(v V) O, clients int,
) {
	h.Helper()
	steps := actions(c, h.Subject)
	alone(steps, read, write)
	stateful.Steps(c, stateful.Machine[struct{}]{Actions: steps}, stateful.Clients(clients), h.Schedule(c),
		stateful.Swarm(false), stateful.ConcurrentMean(sectionMean))
	events := c.History().Events()
	versions := map[int]O{}
	for i := range events {
		e := &events[i]
		out, _ := e.Output.(conform.Output)
		if op := events[e.Call].Operation; e.Kind == history.OK && !out.Failed && (op == read || op == write) {
			versions[e.Call] = version(value[V](out))
		}
	}
	order := causalOrder[O]{last: map[int]int{}, pasts: map[int]*memo[mark[O]]{}}
	for i := range events {
		e := &events[i]
		v, judged := versions[e.Call]
		writes := events[e.Call].Operation == write
		if !judged || (e.Kind == history.Invoke) != writes {
			continue
		}
		group := groupOf(&completed{args: events[e.Call].Args, keys: events[e.Call].Keys})
		past, stamps := order.past(e.Client, group, v, writes)
		if before, found := past.at(group); found {
			h.Engage()
			h.Expect(spec.UnobservedWrite)
			in := atLeast[O]
			if writes {
				in = above[O]
			} else if before.written {
				assert.Contains(h, stamps, v, h.Statement())
			}
			assert.Pairwise(h, []O{before.version, v}, in, h.Statement())
		}
		raise(past, group, mark[O]{version: v, written: writes})
		if writes {
			order.started = append(order.started, stampedWrite[O]{group: group, version: v, past: past})
		}
		order.pasts[e.Call] = past
		order.last[e.Client] = e.Call
	}
}

// causalOrder is the state of VersionsFollowTheCausalOrder across the events
// of a history. started contains the writes that started. last contains the
// call of each client that the probe ordered last, and pasts contains the
// mark of each group of keys among the calls that each call follows, the
// call included.
type causalOrder[O cmp.Ordered] struct {
	last    map[int]int
	pasts   map[int]*memo[mark[O]]
	started []stampedWrite[O]
}

// mark is the highest version of a group of keys among the calls that a call
// follows, and whether one of those calls is a write.
type mark[O cmp.Ordered] struct {
	version O
	written bool
}

// stampedWrite is a write that started: its version, the mark of each group
// of keys among the calls that it follows, the write included, and its group.
type stampedWrite[O cmp.Ordered] struct {
	version O
	past    *memo[mark[O]]
	group   []any
}

// past returns the mark of each group among the calls that a call of client
// follows. A read with the group and the version v also follows the one write
// that started with them. For a read, past also returns the versions of the
// writes with the group that started.
func (o *causalOrder[O]) past(client int, group []any, v O, writes bool) (*memo[mark[O]], []O) {
	past := &memo[mark[O]]{}
	if before, found := o.last[client]; found {
		merge(past, o.pasts[before])
	}
	if writes {
		return past, nil
	}
	var stamps []O
	var seen []*memo[mark[O]]
	for _, w := range o.started {
		if !reflect.DeepEqual(w.group, group) {
			continue
		}
		stamps = append(stamps, w.version)
		if w.version == v {
			seen = append(seen, w.past)
		}
	}
	if len(seen) == 1 {
		merge(past, seen[0])
	}
	return past, stamps
}

// merge raises the mark of each list of m with the mark of the list in from.
func merge[O cmp.Ordered](m, from *memo[mark[O]]) {
	for i, list := range from.lists {
		raise(m, list, from.values[i])
	}
}

// raise sets the mark of list in m to next, joined with the mark of list
// that m has: the higher version, written where either mark is.
func raise[O cmp.Ordered](m *memo[mark[O]], list []any, next mark[O]) {
	if before, found := m.at(list); found {
		next = mark[O]{version: max(before.version, next.version), written: before.written || next.written}
	}
	m.put(list, next)
}

// alone gives each action of steps whose name is not in concurrent, and that
// has no Enabled of its own, an Enabled that returns true. stateful.Steps
// leaves such an action out of a concurrent section, so it runs on client 0
// alone.
func alone(steps []stateful.Action[struct{}], concurrent ...string) {
	for i := range steps {
		if !slices.Contains(concurrent, steps[i].Name) && steps[i].Enabled == nil {
			steps[i].Enabled = func(struct{}) bool { return true }
		}
	}
}

// ExclusiveUnderConcurrency runs generated sequences of the call on the
// subject of h, with a concurrent section on the clients 0 to clients that
// the schedule of h runs. The sequences run the call alone, so the lease that
// the first call to succeed takes stays taken, and each other call fails. It
// fails when more than one call succeeds, and when a call fails with an error
// that does not match sentinel. A case without a call that succeeds does not
// engage h.
func ExclusiveUnderConcurrency[S any](h *conform.Handle[S], c *prop.Case, call Act, sentinel error, clients int) {
	h.Helper()
	var mu sync.Mutex
	var errs []error
	acquire := stateful.Action[struct{}]{
		Name: acquireOperation,
		Run: func(c *prop.Case, client int, _ any) {
			invoked := c.History().Invoke(client, acquireOperation, nil)
			err := call(c.Context())
			conform.Complete(c.Context(), invoked, err)
			mu.Lock()
			defer mu.Unlock()
			errs = append(errs, err)
		},
	}
	stateful.Steps(c, stateful.Machine[struct{}]{Actions: []stateful.Action[struct{}]{acquire}},
		stateful.Clients(clients), h.Schedule(c), stateful.Swarm(false))
	granted := 0
	for _, err := range errs {
		if err == nil {
			granted++
		}
	}
	if granted == 0 {
		return
	}
	h.Engage()
	h.Expect(spec.UnguardedWrite)
	assert.Equal(h, granted, 1, h.Statement())
	for _, err := range errs {
		if err != nil {
			assert.ErrorIs(h, err, sentinel, h.Statement())
		}
	}
}

// acquireOperation is the operation of each call of ExclusiveUnderConcurrency,
// as the history records it.
const acquireOperation = "acquire"

// SharesOneComputation holds the dependencies of h, and the clients 0 to
// clients make the call at once, each on a goroutine of its own. Once every
// goroutine of the bubble of the check is blocked, it counts the calls that
// the dependencies recorded since the hold, releases the dependencies and
// waits for each call. It fails when the dependencies recorded more than one
// call, each call one computation. A check whose dependencies do not record a
// call does not engage h. The probe waits through testing/synctest, so its
// check runs inside a bubble that the field Around of the check sets. A call
// that panics fails the check with the value of the first panic once each
// call has returned.
func SharesOneComputation[S, V any](h *conform.Handle[S], call Call[V], clients int) {
	h.Helper()
	injector := h.Injector()
	before := len(injector.Calls())
	injector.Hold()
	var calls sync.WaitGroup
	var panicked atomic.Pointer[any]
	for range clients + 1 {
		calls.Go(func() {
			defer func() {
				if r := recover(); r != nil {
					panicked.CompareAndSwap(nil, &r)
				}
			}()
			_, _ = call(h.Context())
		})
	}
	synctest.Wait()
	computed := len(injector.Calls()) - before
	injector.Release()
	calls.Wait()
	if r := panicked.Load(); r != nil {
		h.Fatalf("%s: a call panics with %v", h.Statement(), *r)
		return
	}
	if computed == 0 {
		return
	}
	h.Engage()
	h.Expect(spec.RepeatedComputation)
	assert.Equal(h, computed, 1, h.Statement())
}
