// Copyright Dokimasia B.V. 2026
// SPDX-License-Identifier: MIT

package sim

import (
	"context"
	"slices"

	"go.dokimi.dev/assert/history"
)

// Message is an inbound call that one process sends to another.
type Message[E any] struct {
	// Call returns the event of the call with the reply token reply.
	Call func(reply uint64) E
	// Result returns the event of the result of the effect from the value
	// and the error of the reply.
	Result func(value any, err error) E
	// To is the name of the process that takes the call.
	To string
}

// inbound is an open inbound call: the process that takes it, and either the
// call of the history of a client or the await of the sender of a message,
// with the function that builds the event of the message.
type inbound[E, F any] struct {
	call    *history.Call
	await   *await[E, F]
	build   func(reply uint64) E
	process *Process[E, F]
	client  int
}

// await is an effect of a process that waits for the reply to its message:
// the event of its result, the process and its epoch at the send, and whether
// the effect has its result.
type await[E, F any] struct {
	result  func(value any, err error) E
	process *Process[E, F]
	epoch   uint64
	done    bool
}

// send sends the message m of the effect f of the process p, and bounds the
// wait of p with the timeout of f. A message across a partition does not
// arrive. A message to a crashed process fails with ErrCrashed, and a
// message to a stopped process with ErrRunEnded. It fails the case for a
// name that no process has.
func (w *World[E, F]) send(p *Process[E, F], f F, m *Message[E]) {
	to := w.named(m.To)
	a := &await[E, F]{result: m.Result, process: p, epoch: p.epoch}
	if d := w.a.Timeout(f); d > 0 {
		w.at(p, d, func() { w.expire(a) })
	}
	switch {
	case w.cut(p, to):
	case to.state == crashed:
		w.resolve(a, to, nil, ErrCrashed)
	case to.state == stopped:
		w.resolve(a, to, nil, ErrRunEnded)
	default:
		w.request(to, m.Call, a)
	}
}

// request queues the call that build builds from a new reply token for the
// process to, as a message of the await a.
func (w *World[E, F]) request(to *Process[E, F], build func(reply uint64) E, a *await[E, F]) {
	token := w.token()
	w.calls[token] = inbound[E, F]{process: to, await: a, build: build}
	to.queue = append(to.queue, queued[E]{ev: build(token), epoch: to.epoch, message: token})
}

// resolve returns the reply with value and err of the process from to the
// sender of the await a, unless a has its result or a partition cuts the two
// processes apart.
func (w *World[E, F]) resolve(a *await[E, F], from *Process[E, F], value any, err error) {
	if a.done || w.cut(from, a.process) {
		return
	}
	a.complete(value, err)
}

// complete queues the event of the result of a with value and err for its
// process, in the epoch of the send.
func (a *await[E, F]) complete(value any, err error) {
	a.done = true
	a.process.queue = append(a.process.queue, queued[E]{ev: a.result(value, err), epoch: a.epoch})
}

// expire ends the await a at its timeout: its process takes the result with
// context.DeadlineExceeded, and the receiver of each open call of a takes
// the cancel of the call.
func (w *World[E, F]) expire(a *await[E, F]) {
	if a.done {
		return
	}
	a.complete(nil, context.DeadlineExceeded)
	for _, token := range w.tokens(func(in inbound[E, F]) bool { return in.await == a }) {
		w.cancel(token)
	}
}

// abandon ends the awaits of the process p, which crashed or stopped: the
// receiver of each open call that p sent takes the cancel of the call.
func (w *World[E, F]) abandon(p *Process[E, F]) {
	for _, token := range w.tokens(func(in inbound[E, F]) bool { return in.await != nil && in.await.process == p }) {
		w.calls[token].await.done = true
		w.cancel(token)
	}
}

// cancel closes the open call token, and queues its cancel for its process
// when the anatomy has a cancel event.
func (w *World[E, F]) cancel(token uint64) {
	in := w.calls[token]
	delete(w.calls, token)
	if w.a.Cancelled != nil {
		in.process.queue = append(in.process.queue, queued[E]{ev: w.a.Cancelled(token), epoch: in.process.epoch})
	}
}

// message returns the place in the queue of p of its oldest message in
// flight, and -1 when no message waits for it. A message is in flight while
// its call is open: a timeout, a crash or a stop of its sender closes it. An
// event that is no message has the token 0, which no call has. Each message
// in flight has the current epoch of p, because a crash empties the queue
// and a message to a crashed process fails at its sender.
func (w *World[E, F]) message(p *Process[E, F]) int {
	return slices.IndexFunc(p.queue, func(q queued[E]) bool { return w.calls[q.message].process == p })
}

// tokens returns the reply tokens of the open calls that match, in the order
// of the calls.
func (w *World[E, F]) tokens(match func(in inbound[E, F]) bool) []uint64 {
	var out []uint64
	for token, in := range w.calls {
		if match(in) {
			out = append(out, token)
		}
	}
	slices.Sort(out)
	return out
}
