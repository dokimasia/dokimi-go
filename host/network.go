// Copyright Dokimasia B.V. 2026
// SPDX-License-Identifier: MIT

package host

import (
	"context"
	"errors"
	"fmt"
	"math"
	"net"
	"slices"
	"strconv"
	"sync"
	"syscall"
	"time"

	"go.dokimi.dev/assert/prop"
	"go.dokimi.dev/assert/stateful"

	"go.dokimi.dev/medium"
)

// ErrReset is the error of a call on a stream that a reset ended.
var ErrReset = errors.New("host: the connection was reset")

// ErrNoNetwork is the error of a call of the network of a host without one.
var ErrNoNetwork = errors.New("host: the host has no network")

// defaultLatency is the highest latency of a link of a blocking network without
// the option Latency, the default of turmoil. rate is the rate of the
// exponential distribution from which the case chooses a link, as turmoil
// chooses the latency of a message.
const (
	defaultLatency = 100 * time.Millisecond
	rate           = 5
)

// firstPort is the first port that a dial takes for its end of a stream, the
// first port of the dynamic range of IANA.
const firstPort = 49152

// Network connects the hosts of a world through streams and datagrams.
//
// A network built with blocking delays the bytes of each stream after the
// latency of its link plus their size over its throughput, which the case
// chooses once per case for each pair of hosts from the ranges of [Latency]
// and [Throughput]. It delays the bytes of a
// stream across a cut until the repair, and makes a dial across a cut wait
// for the repair. A network without blocking delivers each byte at once,
// resets each stream across a cut, refuses each dial across it, and refuses
// Slow. Each read of a connection and each accept waits on a condition
// variable of the bubble, or parks its task under the scheduler, so a
// component that waits for its network waits durably.
//
// # Concurrency
//
// Its methods are safe for concurrent use.
type Network struct {
	c *prop.Case

	// w is the waiter of the hosts of the network, which the first host
	// sets.
	w *waiter

	// links contains the link of each pair of hosts, by the pair of their
	// names in order.
	links     map[[2]string]*link
	listeners map[addr]*listener
	packets   map[addr]*packetConn

	// hosts are the names of the hosts in the order of their joins, and
	// sides are the sides of the partition in force, nil without one.
	hosts []string
	sides [][]string

	// streams are the streams that no reset ended, and flight the datagrams
	// in flight in the order of their sends.
	streams []*stream
	flight  []*datagram

	mu sync.Mutex

	// lowest and highest are the latency range of the links. slowest is the
	// time of a byte at the lowest throughput of the option Throughput.
	lowest, highest, slowest time.Duration

	// ports is the last port that a dial took, and throughput the lowest
	// throughput of the option Throughput.
	ports, throughput int

	// blocking reports that the network delays the bytes of its streams, and
	// limited reports the option Throughput.
	blocking, limited bool
}

// NetworkOption is an option of [NewNetwork].
type NetworkOption struct{ set func(n *Network) }

// Latency states the latency range of a blocking network. The case chooses
// the latency of each link once per case as turmoil chooses the latency of a
// message: lowest plus highest − lowest times a value of the exponential
// distribution with the rate 5, at most highest. The choice shrinks to
// lowest. The range is 0 to 100 ms, the default of turmoil, without the
// option.
func Latency(lowest, highest time.Duration) NetworkOption {
	return NetworkOption{set: func(n *Network) { n.lowest, n.highest = lowest, highest }}
}

// Throughput states the lowest throughput of a link of a blocking network, in
// bytes per second. The case chooses the time of a byte on each link once per
// case in the way of Latency, from no time to the time of a byte at lowest,
// and the choice shrinks to no time. A link has no limit of throughput
// without the option.
func Throughput(lowest int) NetworkOption {
	return NetworkOption{set: func(n *Network) { n.throughput, n.limited = lowest, true }}
}

// link is the link between two hosts of a blocking network.
type link struct {
	// latency is the latency that the case chose, and slow the latency of
	// Slow, 0 without one.
	latency, slow time.Duration

	// perByte is the time of one byte of a stream on the link.
	perByte time.Duration
}

// NewNetwork returns a network of the case c without hosts, which delays the
// bytes of each stream for blocking, under the options opts. It fails the case
// for a latency range with a negative lowest or a highest below its lowest,
// and for a lowest throughput of 0 or less.
func NewNetwork(c *prop.Case, blocking bool, opts ...NetworkOption) *Network {
	n := &Network{
		c: c, blocking: blocking, ports: firstPort - 1, highest: defaultLatency,
		links: map[[2]string]*link{}, listeners: map[addr]*listener{}, packets: map[addr]*packetConn{},
	}
	for _, o := range opts {
		o.set(n)
	}
	switch {
	case n.lowest < 0 || n.highest < n.lowest:
		c.Fatalf("host: Latency(%v, %v) states no range of latencies", n.lowest, n.highest)
	case n.limited && n.throughput <= 0:
		c.Fatalf("host: Throughput(%d) states no throughput", n.throughput)
	case n.limited:
		n.slowest = time.Second / time.Duration(n.throughput)
	}
	return n
}

// chosen returns lowest plus highest − lowest times the value of the
// exponential distribution with the rate 5 at the quantile q, and at most
// highest, as turmoil chooses the latency of a message.
func chosen(q float64, lowest, highest time.Duration) time.Duration {
	value := -math.Log1p(-q) / rate
	//dokimi:mutate-skip ror-boundary: at the value 1 both branches return highest
	if value >= 1 {
		return highest
	}
	return lowest + time.Duration(value*float64(highest-lowest))
}

// pairOf returns the names a and b in order.
func pairOf(a, b string) [2]string {
	//dokimi:mutate-skip ror-boundary: two equal names swap into the same pair
	if b < a {
		a, b = b, a
	}
	return [2]string{a, b}
}

// Partition cuts every link between two sides until Repair. Two hosts exchange
// bytes and datagrams while one side contains both, so the sides of
// majorities-ring and bridge overlap, as the partitions of sim do. Each host
// exchanges bytes and datagrams with itself across every partition.
func (n *Network) Partition(sides ...[]string) {
	n.mu.Lock()
	n.sides = sides
	if !n.blocking {
		n.streams = slices.DeleteFunc(n.streams, func(s *stream) bool {
			if n.cut(s.a.local.host, s.b.local.host) {
				s.reset()
				//dokimi:mutate-skip sbr-zero: a reset stream that remains in the list resets again at a later reset, which no call observes
				return true
			}
			return false
		})
	}
	n.mu.Unlock()
	n.w.notify()
}

// Repair ends the partition.
func (n *Network) Repair() {
	n.mu.Lock()
	n.sides = nil
	n.mu.Unlock()
	n.w.notify()
}

// Reset fails every call on both ends of each stream between the hosts a and
// b with an error that wraps ErrReset.
func (n *Network) Reset(a, b string) {
	n.mu.Lock()
	pair := pairOf(a, b)
	n.streams = slices.DeleteFunc(n.streams, func(s *stream) bool {
		if pairOf(s.a.local.host, s.b.local.host) == pair {
			s.reset()
			//dokimi:mutate-skip sbr-zero: a reset stream that remains in the list resets again at a later reset, which no call observes
			return true
		}
		return false
	})
	n.mu.Unlock()
	n.w.notify()
}

// Slow raises the latency of the link between the hosts a and b to latency,
// until Restore. It fails the case for a network without blocking, and for a
// pair of hosts without a link.
func (n *Network) Slow(a, b string, latency time.Duration) {
	n.mu.Lock()
	l, found := n.links[pairOf(a, b)]
	if found {
		l.slow = latency
	}
	n.mu.Unlock()
	switch {
	case !n.blocking:
		n.c.Fatalf("host: Slow(%s, %s) of a network without blocking", a, b)
	case !found:
		n.c.Fatalf("host: Slow(%s, %s) of two hosts without a link", a, b)
	}
}

// Restore ends the slowness of the link between the hosts a and b.
func (n *Network) Restore(a, b string) {
	n.mu.Lock()
	defer n.mu.Unlock()
	if l, found := n.links[pairOf(a, b)]; found {
		l.slow = 0
	}
}

// join adds the host h under the scheduler s to the network, and returns the
// waiter of the network. A blocking network takes the link between h and each
// earlier host from the case.
func (n *Network) join(h *Host, s *stateful.Scheduler) *waiter {
	n.mu.Lock()
	defer n.mu.Unlock()
	if n.w == nil {
		n.w = newWaiter(s)
	} else if n.w.s != s {
		// The fatal record ends the goroutine, and the deferred unlock runs.
		n.c.Fatalf("host: New of %s under another scheduler than the hosts of its network", h.name)
	}
	if n.blocking {
		for _, other := range n.hosts {
			pair := pairOf(other, h.name)
			quantile := n.c.Draw(prop.Float(0.0, 1.0), "latency."+pair[0]+"."+pair[1])
			l := &link{latency: chosen(quantile, n.lowest, n.highest)}
			if n.limited {
				quantile = n.c.Draw(prop.Float(0.0, 1.0), "throughput."+pair[0]+"."+pair[1])
				l.perByte = chosen(quantile, 0, n.slowest)
			}
			n.links[pair] = l
		}
	}
	n.hosts = append(n.hosts, h.name)
	return n.w
}

// cut reports whether the partition in force cuts the hosts a and b apart.
func (n *Network) cut(a, b string) bool {
	return a != b && n.sides != nil && !slices.ContainsFunc(n.sides, func(side []string) bool {
		return slices.Contains(side, a) && slices.Contains(side, b)
	})
}

// delay returns the latency of the link between the hosts a and b, and the
// time of one byte on it. Two names of one host, and the hosts of a network
// without blocking, have no delay.
func (n *Network) delay(a, b string) (time.Duration, time.Duration) {
	l, found := n.links[pairOf(a, b)]
	switch {
	case !found:
		return 0, 0
	case l.slow > 0:
		return l.slow, l.perByte
	}
	return l.latency, l.perByte
}

// fence ends the network resources of the crashed incarnation i: it closes
// each listener and each connection of datagrams of i, and resets each stream
// of i.
func (n *Network) fence(i *Incarnation) {
	n.mu.Lock()
	defer n.mu.Unlock()
	for a, l := range n.listeners {
		if l.inc == i {
			l.closed = true
			delete(n.listeners, a)
		}
	}
	for a, p := range n.packets {
		if p.inc == i {
			p.closed = true
			delete(n.packets, a)
		}
	}
	n.streams = slices.DeleteFunc(n.streams, func(s *stream) bool {
		if s.a.inc == i || s.b.inc == i {
			s.reset()
			//dokimi:mutate-skip sbr-zero: a reset stream that remains in the list resets again at a later reset, which no call observes
			return true
		}
		return false
	})
}

// wake makes the waits of the network check their conditions at the instant
// at of the clock of the run, until the end of the case.
func (n *Network) wake(at time.Time) {
	n.w.wake(n.c.Context(), at)
}

// listen returns a listener of streams of the incarnation i on the port.
func (n *Network) listen(i *Incarnation, port int) (net.Listener, error) {
	a := addr{host: i.h.name, port: port, network: tcp}
	if n == nil {
		return nil, opError("listen", a, nil, ErrNoNetwork)
	}
	if !i.gate.enter() {
		return nil, opError("listen", a, nil, medium.ErrCrashed)
	}
	n.mu.Lock()
	defer n.mu.Unlock()
	if _, taken := n.listeners[a]; taken {
		return nil, opError("listen", a, nil, syscall.EADDRINUSE)
	}
	l := &listener{n: n, inc: i, addr: a}
	n.listeners[a] = l
	return l, nil
}

// dial connects the incarnation i to the listener at the address text, under
// ctx.
func (n *Network) dial(ctx context.Context, i *Incarnation, text string) (net.Conn, error) {
	to, err := parseAddr(text, tcp)
	if err != nil {
		return nil, opError("dial", addr{network: tcp}, nil, err)
	}
	if n == nil {
		return nil, opError("dial", to, nil, ErrNoNetwork)
	}
	from := i.h.name
	stop := context.AfterFunc(ctx, n.w.notify)
	//dokimi:mutate-skip sbr-delete: the stop releases the call of the context before it ends, which only the memory of a long incarnation observes
	defer stop()
	n.w.wait(func() bool {
		n.mu.Lock()
		defer n.mu.Unlock()
		return ctx.Err() != nil || i.gate.ended() || !n.blocking || !n.cut(from, to.host)
	})
	if !i.gate.enter() {
		return nil, opError("dial", to, nil, medium.ErrCrashed)
	}
	if err := ctx.Err(); err != nil {
		return nil, opError("dial", to, nil, err)
	}
	n.mu.Lock()
	l, found := n.listeners[to]
	if !found || n.cut(from, to.host) {
		n.mu.Unlock()
		return nil, opError("dial", to, nil, syscall.ECONNREFUSED)
	}
	n.ports++
	s := newStream(n, i, addr{host: from, port: n.ports, network: tcp}, l.inc, to)
	n.streams = append(n.streams, s)
	l.backlog = append(l.backlog, s.b)
	n.mu.Unlock()
	n.w.notify()
	return s.a, nil
}

// listenPacket returns a connection of datagrams of the incarnation i on the
// port.
func (n *Network) listenPacket(i *Incarnation, port int) (net.PacketConn, error) {
	a := addr{host: i.h.name, port: port, network: udp}
	if n == nil {
		return nil, opError("listen", a, nil, ErrNoNetwork)
	}
	if !i.gate.enter() {
		return nil, opError("listen", a, nil, medium.ErrCrashed)
	}
	n.mu.Lock()
	defer n.mu.Unlock()
	if _, taken := n.packets[a]; taken {
		return nil, opError("listen", a, nil, syscall.EADDRINUSE)
	}
	p := &packetConn{n: n, inc: i, addr: a}
	n.packets[a] = p
	return p, nil
}

// The networks of the addresses of a host.
const (
	tcp = "tcp"
	udp = "udp"
)

// addr is an address of a host: the name of the host and a port, of a stream
// for the network tcp and of a datagram for udp.
type addr struct {
	host    string
	network string
	port    int
}

// Network returns tcp or udp.
func (a addr) Network() string {
	return a.network
}

// String returns the name of the host and the port, such as node-2:7000.
func (a addr) String() string {
	return net.JoinHostPort(a.host, strconv.Itoa(a.port))
}

// parseAddr returns the address of the text, the name of a host and a port,
// on the network.
func parseAddr(text, network string) (addr, error) {
	name, port, err := net.SplitHostPort(text)
	if err != nil {
		return addr{}, fmt.Errorf("host: the address %s has no host and port: %w", text, err)
	}
	number, err := strconv.Atoi(port)
	if err != nil {
		return addr{}, fmt.Errorf("host: the port of %s is no number: %w", text, err)
	}
	return addr{host: name, port: number, network: network}, nil
}

// opError returns the error of the operation op of the network on the address
// to, from the address from or nil, which wraps err, as the net package
// returns it.
func opError(op string, to, from net.Addr, err error) error {
	return &net.OpError{Op: op, Net: to.Network(), Source: from, Addr: to, Err: err}
}
