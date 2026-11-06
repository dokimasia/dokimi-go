// Copyright Dokimasia B.V. 2026
// SPDX-License-Identifier: MIT

package host

import (
	"io"
	"net"
	"os"
	"slices"
	"time"

	"go.dokimi.dev/medium"
)

// stream is a stream between two incarnations: the end a of the dialer, and
// the end b of the listener.
type stream struct {
	a, b *end
}

// newStream returns the stream of the network n from the incarnation dialer
// at from to the incarnation listener at to.
func newStream(n *Network, dialer *Incarnation, from addr, listener *Incarnation, to addr) *stream {
	a := &end{n: n, inc: dialer, local: from, remote: to}
	b := &end{n: n, inc: listener, local: to, remote: from, peer: a}
	a.peer = b
	return &stream{a: a, b: b}
}

// reset ends both ends of the stream with ErrReset, and drops the bytes that
// they did not read. The network is locked.
func (s *stream) reset() {
	for _, e := range []*end{s.a, s.b} {
		e.reset, e.in, e.buf = true, nil, nil
	}
}

// end is one end of a stream, a net.Conn of its incarnation. Its fields are
// guarded by the lock of its network.
type end struct {
	// readDeadline and writeDeadline are the deadlines of the end, the zero
	// time for none. busy is the instant at which the link of the end is free
	// for its next write.
	readDeadline, writeDeadline, busy time.Time

	n    *Network
	inc  *Incarnation
	peer *end

	// in are the segments in flight to the end, in order, and buf the bytes
	// that arrived and that the end did not read.
	in  []segment
	buf []byte

	local, remote addr

	// eof reports that the close of the peer arrived, closed that the end
	// closed, and reset that a reset ended the stream.
	eof, closed, reset bool
}

// segment is a write in flight to an end, or the close of the peer for fin.
type segment struct {
	at   time.Time
	data []byte
	fin  bool
}

// The compiler checks that an end is a net.Conn.
var _ net.Conn = (*end)(nil)

// Read reads the bytes that arrived at the end. It waits until bytes arrive,
// the close of the peer arrives, the stream resets, the end closes or the read
// deadline passes. The crash of the incarnation resets the stream, and Read
// then fails with medium.ErrCrashed. It returns io.EOF once the close of the
// peer arrived after the last byte.
func (e *end) Read(p []byte) (int, error) {
	n := e.n
	n.mu.Lock()
	deadline := e.readDeadline
	n.mu.Unlock()
	if !deadline.IsZero() {
		n.wake(deadline)
	}
	n.w.wait(func() bool {
		n.mu.Lock()
		defer n.mu.Unlock()
		e.arrive()
		return len(e.buf) > 0 || e.eof || e.reset || e.closed || e.expired(e.readDeadline)
	})
	if !e.inc.gate.enter() {
		return 0, opError("read", e.remote, e.local, medium.ErrCrashed)
	}
	n.mu.Lock()
	defer n.mu.Unlock()
	switch {
	case e.closed:
		return 0, opError("read", e.remote, e.local, net.ErrClosed)
	case e.reset:
		return 0, opError("read", e.remote, e.local, ErrReset)
	case len(e.buf) > 0:
		count := copy(p, e.buf)
		e.buf = e.buf[count:]
		return count, nil
	case e.eof:
		return 0, io.EOF
	}
	return 0, opError("read", e.remote, e.local, os.ErrDeadlineExceeded)
}

// Write sends the bytes of p to the peer. On a blocking network they arrive
// after the latency of the link plus their size over its throughput, after
// the bytes of the earlier writes, and not across a cut until the repair. On
// a network without blocking they arrive at once, and a cut resets the stream
// before any write.
func (e *end) Write(p []byte) (int, error) {
	if !e.inc.gate.enter() {
		return 0, opError("write", e.remote, e.local, medium.ErrCrashed)
	}
	n := e.n
	n.mu.Lock()
	switch {
	case e.closed:
		n.mu.Unlock()
		return 0, opError("write", e.remote, e.local, net.ErrClosed)
	case e.reset:
		n.mu.Unlock()
		return 0, opError("write", e.remote, e.local, ErrReset)
	case e.expired(e.writeDeadline):
		n.mu.Unlock()
		return 0, opError("write", e.remote, e.local, os.ErrDeadlineExceeded)
	}
	at := e.send(segment{data: slices.Clone(p)})
	n.mu.Unlock()
	n.wake(at)
	return len(p), nil
}

// Close closes the end. The peer reads the bytes in flight, and then io.EOF.
func (e *end) Close() error {
	if !e.inc.gate.enter() {
		return opError("close", e.remote, e.local, medium.ErrCrashed)
	}
	n := e.n
	n.mu.Lock()
	if e.closed {
		n.mu.Unlock()
		return opError("close", e.remote, e.local, net.ErrClosed)
	}
	e.closed = true
	at := e.send(segment{fin: true})
	n.mu.Unlock()
	n.wake(at)
	return nil
}

// LocalAddr returns the address of the end.
func (e *end) LocalAddr() net.Addr {
	return e.local
}

// RemoteAddr returns the address of the peer.
func (e *end) RemoteAddr() net.Addr {
	return e.remote
}

// SetDeadline sets the read deadline and the write deadline, instants of the
// clock of the run: the clock of the bubble, or the clock of the scheduler of
// the host.
func (e *end) SetDeadline(t time.Time) error {
	e.n.mu.Lock()
	e.readDeadline, e.writeDeadline = t, t
	e.n.mu.Unlock()
	e.n.w.notify()
	return nil
}

// SetReadDeadline sets the read deadline, an instant of the clock of the run.
func (e *end) SetReadDeadline(t time.Time) error {
	e.n.mu.Lock()
	e.readDeadline = t
	e.n.mu.Unlock()
	e.n.w.notify()
	return nil
}

// SetWriteDeadline sets the write deadline, an instant of the clock of the
// run.
func (e *end) SetWriteDeadline(t time.Time) error {
	e.n.mu.Lock()
	e.writeDeadline = t
	e.n.mu.Unlock()
	return nil
}

// send queues the segment s to the peer, and returns the instant at which it
// arrives, the zero time for a network without blocking, whose segments
// arrive at once. The network is locked.
func (e *end) send(s segment) time.Time {
	n := e.n
	if n.blocking {
		now := n.w.now()
		latency, perByte := n.delay(e.local.host, e.remote.host)
		if now.After(e.busy) {
			e.busy = now
		}
		e.busy = e.busy.Add(time.Duration(len(s.data)) * perByte)
		s.at = e.busy.Add(latency)
	}
	e.peer.in = append(e.peer.in, s)
	return s.at
}

// arrive moves each segment that has arrived at the end from in to buf, in
// order, unless a partition cuts the link. The network is locked.
func (e *end) arrive() {
	now := e.n.w.now()
	for len(e.in) > 0 && !e.in[0].at.After(now) && !e.n.cut(e.local.host, e.remote.host) {
		s := e.in[0]
		e.in = e.in[1:]
		if s.fin {
			e.eof = true
		}
		e.buf = append(e.buf, s.data...)
	}
}

// expired reports whether the deadline d has passed on the clock of the run,
// and false for the zero time.
func (e *end) expired(d time.Time) bool {
	return !d.IsZero() && !e.n.w.now().Before(d)
}

// listener is a listener of streams of an incarnation. Its fields are guarded
// by the lock of its network.
type listener struct {
	n   *Network
	inc *Incarnation

	// backlog are the ends of the streams that a dial connected and that no
	// accept took, in order.
	backlog []*end

	addr   addr
	closed bool
}

// The compiler checks that a listener is a net.Listener.
var _ net.Listener = (*listener)(nil)

// Accept waits for the next stream that a dial connected, and returns its end.
// It fails once the listener closes or its incarnation crashes.
func (l *listener) Accept() (net.Conn, error) {
	n := l.n
	n.w.wait(func() bool {
		n.mu.Lock()
		defer n.mu.Unlock()
		return len(l.backlog) > 0 || l.closed
	})
	if !l.inc.gate.enter() {
		return nil, opError("accept", l.addr, nil, medium.ErrCrashed)
	}
	n.mu.Lock()
	defer n.mu.Unlock()
	if l.closed {
		return nil, opError("accept", l.addr, nil, net.ErrClosed)
	}
	e := l.backlog[0]
	l.backlog = l.backlog[1:]
	return e, nil
}

// Close closes the listener. A later dial of its address is refused.
func (l *listener) Close() error {
	n := l.n
	n.mu.Lock()
	if l.closed {
		n.mu.Unlock()
		return opError("close", l.addr, nil, net.ErrClosed)
	}
	l.closed = true
	delete(n.listeners, l.addr)
	n.mu.Unlock()
	n.w.notify()
	return nil
}

// Addr returns the address of the listener.
func (l *listener) Addr() net.Addr {
	return l.addr
}
