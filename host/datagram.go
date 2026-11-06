// Copyright Dokimasia B.V. 2026
// SPDX-License-Identifier: MIT

package host

import (
	"net"
	"os"
	"slices"
	"time"

	"go.dokimi.dev/medium"
)

// datagram is a datagram in flight, which arrives at the instant at.
type datagram struct {
	at       time.Time
	from, to addr
	data     []byte
}

// packetConn is a connection of datagrams of an incarnation, a net.PacketConn.
// Its fields are guarded by the lock of its network.
type packetConn struct {
	n   *Network
	inc *Incarnation

	// queue are the datagrams that arrived and that the connection did not
	// read, in order.
	queue []*datagram

	readDeadline, writeDeadline time.Time

	addr   addr
	closed bool
}

// The compiler checks that a packetConn is a net.PacketConn.
var _ net.PacketConn = (*packetConn)(nil)

// ReadFrom reads the next datagram that arrived at the connection, and
// returns the address of its sender. It waits until a datagram arrives, the
// connection closes, the incarnation crashes or the read deadline passes. A
// datagram that is longer than p loses its last bytes, as a datagram of the
// operating system does.
func (p *packetConn) ReadFrom(b []byte) (int, net.Addr, error) {
	n := p.n
	n.mu.Lock()
	deadline := p.readDeadline
	n.mu.Unlock()
	if !deadline.IsZero() {
		n.wake(deadline)
	}
	n.w.wait(func() bool {
		n.mu.Lock()
		defer n.mu.Unlock()
		n.arrive()
		return len(p.queue) > 0 || p.closed || p.expired(p.readDeadline)
	})
	if !p.inc.gate.enter() {
		return 0, nil, opError("read", p.addr, nil, medium.ErrCrashed)
	}
	n.mu.Lock()
	defer n.mu.Unlock()
	switch {
	case p.closed:
		return 0, nil, opError("read", p.addr, nil, net.ErrClosed)
	case len(p.queue) > 0:
		d := p.queue[0]
		p.queue = p.queue[1:]
		return copy(b, d.data), d.from, nil
	}
	return 0, nil, opError("read", p.addr, nil, os.ErrDeadlineExceeded)
}

// WriteTo sends the datagram b to the address to. On a blocking network it
// arrives after the latency of its link, and not across a cut. On a network
// without blocking it arrives at once. A datagram to an address without a
// connection is lost.
func (p *packetConn) WriteTo(b []byte, to net.Addr) (int, error) {
	dst, err := parseAddr(to.String(), udp)
	if err != nil {
		return 0, opError("write", p.addr, nil, err)
	}
	if !p.inc.gate.enter() {
		return 0, opError("write", dst, p.addr, medium.ErrCrashed)
	}
	n := p.n
	n.mu.Lock()
	switch {
	case p.closed:
		n.mu.Unlock()
		return 0, opError("write", dst, p.addr, net.ErrClosed)
	case p.expired(p.writeDeadline):
		n.mu.Unlock()
		return 0, opError("write", dst, p.addr, os.ErrDeadlineExceeded)
	}
	// A datagram of a network without blocking arrives at once, at the zero
	// time.
	d := &datagram{from: p.addr, to: dst, data: slices.Clone(b)}
	if n.blocking {
		latency, perByte := n.delay(p.addr.host, dst.host)
		d.at = n.w.now().Add(latency + time.Duration(len(b))*perByte)
	}
	n.flight = append(n.flight, d)
	n.mu.Unlock()
	n.wake(d.at)
	return len(b), nil
}

// Close closes the connection. A datagram to its address is lost.
func (p *packetConn) Close() error {
	n := p.n
	n.mu.Lock()
	if p.closed {
		n.mu.Unlock()
		return opError("close", p.addr, nil, net.ErrClosed)
	}
	p.closed = true
	delete(n.packets, p.addr)
	n.mu.Unlock()
	n.w.notify()
	return nil
}

// LocalAddr returns the address of the connection.
func (p *packetConn) LocalAddr() net.Addr {
	return p.addr
}

// SetDeadline sets the read deadline and the write deadline, instants of the
// clock of the run: the clock of the bubble, or the clock of the scheduler of
// the host.
func (p *packetConn) SetDeadline(t time.Time) error {
	p.n.mu.Lock()
	p.readDeadline, p.writeDeadline = t, t
	p.n.mu.Unlock()
	p.n.w.notify()
	return nil
}

// SetReadDeadline sets the read deadline, an instant of the clock of the run.
func (p *packetConn) SetReadDeadline(t time.Time) error {
	p.n.mu.Lock()
	p.readDeadline = t
	p.n.mu.Unlock()
	p.n.w.notify()
	return nil
}

// SetWriteDeadline sets the write deadline, an instant of the clock of the
// run.
func (p *packetConn) SetWriteDeadline(t time.Time) error {
	p.n.mu.Lock()
	p.writeDeadline = t
	p.n.mu.Unlock()
	return nil
}

// expired reports whether the deadline d has passed on the clock of the run,
// and false for the zero time.
func (p *packetConn) expired(d time.Time) bool {
	return !d.IsZero() && !p.n.w.now().Before(d)
}

// arrive moves each datagram in flight that has arrived to the connection at
// its address, in the order of the sends. A datagram across a cut, and a
// datagram to an address without a connection, is lost. The network is
// locked.
func (n *Network) arrive() {
	now := n.w.now()
	n.flight = slices.DeleteFunc(n.flight, func(d *datagram) bool {
		if d.at.After(now) {
			return false
		}
		if p, found := n.packets[d.to]; found && !n.cut(d.from.host, d.to.host) {
			p.queue = append(p.queue, d)
		}
		return true
	})
}

// Drop drops the oldest datagram in flight to the host named to, and reports
// whether one was in flight.
func (n *Network) Drop(to string) bool {
	n.mu.Lock()
	defer n.mu.Unlock()
	at := n.oldest(to)
	if at < 0 {
		return false
	}
	n.flight = slices.Delete(n.flight, at, at+1)
	return true
}

// Delay moves the oldest datagram in flight to the host named to behind the
// next datagram in flight to it, and reports whether two were in flight. The
// two datagrams swap their instants of arrival.
func (n *Network) Delay(to string) bool {
	n.mu.Lock()
	defer n.mu.Unlock()
	// Without a datagram to the host, first is -1 and no second one exists.
	first := n.oldest(to)
	second := slices.IndexFunc(n.flight[first+1:], func(d *datagram) bool { return d.to.host == to })
	if second < 0 {
		return false
	}
	a, b := n.flight[first], n.flight[first+1+second]
	a.at, b.at = b.at, a.at
	n.flight[first], n.flight[first+1+second] = b, a
	return true
}

// Duplicate sends the oldest datagram in flight to the host named to once
// more, to arrive with it, and reports whether one was in flight.
func (n *Network) Duplicate(to string) bool {
	n.mu.Lock()
	defer n.mu.Unlock()
	at := n.oldest(to)
	if at < 0 {
		return false
	}
	copied := *n.flight[at]
	n.flight = slices.Insert(n.flight, at+1, &copied)
	return true
}

// InFlight returns the number of the datagrams in flight to the host named to.
func (n *Network) InFlight(to string) int {
	n.mu.Lock()
	defer n.mu.Unlock()
	count := 0
	for _, d := range n.flight {
		if d.to.host == to {
			count++
		}
	}
	return count
}

// oldest returns the index of the oldest datagram in flight to the host named
// to, and -1 without one. The network is locked.
func (n *Network) oldest(to string) int {
	return slices.IndexFunc(n.flight, func(d *datagram) bool { return d.to.host == to })
}
