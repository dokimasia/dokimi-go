// Copyright Dokimasia B.V. 2026
// SPDX-License-Identifier: MIT

package host_test

import (
	"net"
	"os"
	"syscall"
	"testing"
	"testing/synctest"
	"time"

	"go.dokimi.dev/assert"
	"go.dokimi.dev/assert/prop"

	"go.dokimi.dev/host"
	"go.dokimi.dev/medium"
)

// datagrams returns a connection of datagrams of node-1 on the port 9000, and
// one of node-2 on the port 9001.
func datagrams(tb assert.TB, p pair) (net.PacketConn, net.PacketConn) {
	tb.Helper()

	a, err := p.e1.ListenPacket(9000)
	assert.NoError(tb, err, "node-1 listens for datagrams")
	b, err := p.e2.ListenPacket(9001)
	assert.NoError(tb, err, "node-2 listens for datagrams")
	return a, b
}

// narrowLink are the draws of a blocking network of two hosts without drift
// whose link takes the time of a byte at the quantile 1. Under millisecond and
// tenMegabytes the link has a latency of one millisecond and takes 100 ns for
// each byte.
const narrowLink = `[{"label": "drift.node-1", "value": {"type": "int", "value": 0}},` +
	`{"label": "drift.node-2", "value": {"type": "int", "value": 0}},` +
	`{"label": "latency.node-1.node-2", "value": {"type": "float", "value": 0.0}},` +
	`{"label": "throughput.node-1.node-2", "value": {"type": "float", "value": 1.0}}]`

// unparsed is an address without a port, which no host has.
var unparsed = &net.UnixAddr{Name: "sock", Net: "unix"}

// hostAddr is the address of a host of the network udp, as a net.Addr of
// another type than the addresses of the network.
type hostAddr string

// Network returns udp.
func (hostAddr) Network() string { return "udp" }

// String returns the address.
func (a hostAddr) String() string { return string(a) }

// received returns the data of each datagram that b reads within a second of
// the clock of the bubble, in order.
func received(b net.PacketConn) []string {
	var out []string
	_ = b.SetReadDeadline(time.Now().Add(time.Second))
	for {
		buf := make([]byte, 8)
		n, _, err := b.ReadFrom(buf)
		if err != nil {
			return out
		}
		out = append(out, string(buf[:n]))
	}
}

func TestDatagram(t *testing.T) {
	t.Parallel()

	t.Run("ReadFrom", func(t *testing.T) {
		t.Parallel()

		t.Run("returns the datagram and the address of its sender", func(t *testing.T) {
			t.Parallel()
			var got, from string
			bubbled(t, targets, func(c *prop.Case, _ bool) {
				p := pairOf(c, false)
				a, b := datagrams(c, p)
				_, err := a.WriteTo([]byte("ab"), hostAddr("node-2:9001"))
				assert.NoError(c, err, "node-1 sends")
				buf := make([]byte, 8)
				n, addr, err := b.ReadFrom(buf)
				assert.NoError(c, err, "node-2 reads")
				got, from = string(buf[:n]), addr.String()
				p.end()
			})

			assert.Equal(t, []string{got, from}, []string{"ab", "node-1:9000"}, "the datagram and its sender")
		})

		t.Run("returns a datagram after the latency of its link and its size over the throughput", func(t *testing.T) {
			t.Parallel()
			var sent int
			var after time.Duration
			bubbled(t, narrowLink, func(c *prop.Case, first bool) {
				p := pairOf(c, true, millisecond, tenMegabytes)
				a, b := datagrams(c, p)
				start := time.Now()
				n, err := a.WriteTo([]byte("0123456789"), hostAddr("node-2:9001"))
				assert.NoError(c, err, "node-1 sends")
				_, _, err = b.ReadFrom(make([]byte, 16))
				assert.NoError(c, err, "node-2 reads")
				if elapsed := time.Since(start); first {
					sent, after = n, elapsed
				}
				p.end()
			})

			assert.Equal(t, sent, 10, "the send counts each byte")
			assert.Equal(t, after, time.Millisecond+time.Microsecond, "one millisecond and ten bytes of 100 ns")
		})

		t.Run("wakes a read at a deadline that the reader sets while it reads", func(t *testing.T) {
			t.Parallel()
			var errs []error
			bubbled(t, targets, func(c *prop.Case, _ bool) {
				p := pairOf(c, false)
				_, b := datagrams(c, p)
				done := make(chan error, 2)
				got := make([]error, 0, 2)
				for _, set := range []func(time.Time) error{b.SetReadDeadline, b.SetDeadline} {
					assert.NoError(c, set(time.Time{}), "node-2 clears its deadline")
					p.i2.Go(func() {
						_, _, err := b.ReadFrom(make([]byte, 1))
						done <- err
					})
					synctest.Wait()
					assert.NoError(c, set(time.Now()), "node-2 sets a deadline that passed")
					got = append(got, <-done)
				}
				errs = got
				p.end()
			})

			for _, err := range errs {
				assert.ErrorIs(t, err, os.ErrDeadlineExceeded, "the deadline ends the read in progress")
			}
		})

		t.Run("cuts a datagram that is longer than the buffer", func(t *testing.T) {
			t.Parallel()
			var got string
			bubbled(t, targets, func(c *prop.Case, _ bool) {
				p := pairOf(c, false)
				a, b := datagrams(c, p)
				_, err := a.WriteTo([]byte("abcd"), hostAddr("node-2:9001"))
				assert.NoError(c, err, "node-1 sends")
				buf := make([]byte, 2)
				n, _, err := b.ReadFrom(buf)
				assert.NoError(c, err, "node-2 reads")
				got = string(buf[:n])
				p.end()
			})

			assert.Equal(t, got, "ab", "the first bytes of the datagram")
		})

		t.Run("fails with os.ErrDeadlineExceeded once the read deadline passes", func(t *testing.T) {
			t.Parallel()
			var err error
			bubbled(t, targets, func(c *prop.Case, _ bool) {
				p := pairOf(c, false)
				_, b := datagrams(c, p)
				assert.NoError(c, b.SetDeadline(time.Now().Add(time.Second)), "node-2 sets its deadline")
				_, _, err = b.ReadFrom(make([]byte, 1))
				p.end()
			})

			assert.ErrorIs(t, err, os.ErrDeadlineExceeded, "the deadline ends the read")
		})

		t.Run("fails with net.ErrClosed once the connection closes", func(t *testing.T) {
			t.Parallel()
			var errs []error
			bubbled(t, targets, func(c *prop.Case, _ bool) {
				p := pairOf(c, false)
				_, b := datagrams(c, p)
				done := make(chan error, 1)
				p.i2.Go(func() {
					_, _, err := b.ReadFrom(make([]byte, 1))
					done <- err
				})
				synctest.Wait()
				assert.NoError(c, b.Close(), "node-2 closes")
				read := <-done
				closeErr := b.Close()
				_, write := b.WriteTo([]byte("a"), hostAddr("node-2:9001"))
				errs = []error{read, closeErr, write}
				p.end()
			})

			for _, err := range errs {
				assert.ErrorIs(t, err, net.ErrClosed, "the connection is closed")
			}
		})

		t.Run("fails with ErrCrashed once its incarnation crashes", func(t *testing.T) {
			t.Parallel()
			var errs []error
			bubbled(t, targets, func(c *prop.Case, _ bool) {
				p := pairOf(c, false)
				_, b := datagrams(c, p)
				done := make(chan error, 1)
				p.i2.Go(func() {
					_, _, err := b.ReadFrom(make([]byte, 1))
					done <- err
				})
				synctest.Wait()
				p.i2.Crash(host.ProcessCrash)
				_, write := b.WriteTo([]byte("a"), hostAddr("node-2:9001"))
				errs = []error{<-done, write}
				p.i1.Crash(host.ProcessCrash)
			})

			for _, err := range errs {
				assert.ErrorIs(t, err, medium.ErrCrashed, "the crash ends the call")
			}
		})
	})

	t.Run("WriteTo", func(t *testing.T) {
		t.Parallel()

		t.Run("loses a datagram to an address without a connection", func(t *testing.T) {
			t.Parallel()
			var got []string
			bubbled(t, targets, func(c *prop.Case, _ bool) {
				p := pairOf(c, false)
				a, b := datagrams(c, p)
				_, err := a.WriteTo([]byte("lost"), &net.UDPAddr{Port: 9002})
				assert.NoError(c, err, "node-1 sends to a port without a connection")
				got = received(b)
				p.end()
			})

			assert.Empty(t, got, "no datagram arrives")
		})

		t.Run("loses a datagram across a cut", func(t *testing.T) {
			t.Parallel()
			var got []string
			bubbled(t, targets, func(c *prop.Case, _ bool) {
				p := pairOf(c, true)
				a, b := datagrams(c, p)
				p.n.Partition([]string{"node-1"}, []string{"node-2"})
				_, err := a.WriteTo([]byte("lost"), hostAddr("node-2:9001"))
				assert.NoError(c, err, "node-1 sends across the cut")
				got = received(b)
				p.end()
			})

			assert.Empty(t, got, "no datagram crosses the cut")
		})

		t.Run("fails with os.ErrDeadlineExceeded once the write deadline passes", func(t *testing.T) {
			t.Parallel()
			var err error
			bubbled(t, targets, func(c *prop.Case, _ bool) {
				p := pairOf(c, false)
				a, _ := datagrams(c, p)
				assert.NoError(c, a.SetWriteDeadline(time.Now()), "node-1 sets a deadline that passed")
				_, err = a.WriteTo([]byte("a"), hostAddr("node-2:9001"))
				p.end()
			})

			assert.ErrorIs(t, err, os.ErrDeadlineExceeded, "the deadline refuses the write")
		})

		t.Run("refuses an address that does not parse", func(t *testing.T) {
			t.Parallel()
			var err error
			bubbled(t, targets, func(c *prop.Case, _ bool) {
				p := pairOf(c, false)
				a, _ := datagrams(c, p)
				_, err = a.WriteTo([]byte("a"), unparsed)
				p.end()
			})

			_ = assert.ErrorAs[*net.OpError](t, err, "the error of the write")
		})
	})

	t.Run("Close", func(t *testing.T) {
		t.Parallel()

		t.Run("frees the port of the connection", func(t *testing.T) {
			t.Parallel()
			var err error
			bubbled(t, targets, func(c *prop.Case, _ bool) {
				p := pairOf(c, false)
				a, _ := datagrams(c, p)
				assert.NoError(c, a.Close(), "node-1 closes")
				_, err = p.e1.ListenPacket(9000)
				p.end()
			})

			assert.NoError(t, err, "the port is free again")
		})
	})

	t.Run("ListenPacket", func(t *testing.T) {
		t.Parallel()

		t.Run("loses a datagram that arrived at the port before the connection", func(t *testing.T) {
			t.Parallel()
			var got []string
			bubbled(t, targets, func(c *prop.Case, _ bool) {
				p := pairOf(c, true)
				a, err := p.e1.ListenPacket(9000)
				assert.NoError(c, err, "node-1 listens for datagrams")
				_, err = a.WriteTo([]byte("early"), hostAddr("node-2:9001"))
				assert.NoError(c, err, "node-1 sends to a port without a connection")
				b, err := p.e2.ListenPacket(9001)
				assert.NoError(c, err, "node-2 listens for datagrams")
				got = received(b)
				p.end()
			})

			assert.Empty(t, got, "no connection was at the port at the arrival")
		})

		t.Run("refuses a port that a connection takes", func(t *testing.T) {
			t.Parallel()
			var err error
			var local string
			bubbled(t, targets, func(c *prop.Case, _ bool) {
				p := pairOf(c, false)
				a, _ := datagrams(c, p)
				local = a.LocalAddr().String() + "/" + a.LocalAddr().Network()
				_, err = p.e1.ListenPacket(9000)
				p.end()
			})

			assert.ErrorIs(t, err, syscall.EADDRINUSE, "the port is taken")
			assert.Equal(t, local, "node-1:9000/udp", "the address of the connection")
		})
	})

	t.Run("Drop", func(t *testing.T) {
		t.Parallel()

		t.Run("drops the oldest datagram in flight to the host", func(t *testing.T) {
			t.Parallel()
			var dropped, none bool
			var got []string
			bubbled(t, slowLink, func(c *prop.Case, first bool) {
				p := pairOf(c, true, millisecond)
				a, b := datagrams(c, p)
				for _, d := range []string{"a", "b"} {
					_, err := a.WriteTo([]byte(d), hostAddr("node-2:9001"))
					assert.NoError(c, err, "node-1 sends "+d)
				}
				gone := p.n.Drop("node-2")
				missing := p.n.Drop("node-1")
				if arrived := received(b); first {
					dropped, none, got = gone, missing, arrived
				}
				p.end()
			})

			assert.Equal(t, []bool{dropped, none}, []bool{true, false}, "a datagram was in flight to node-2 alone")
			assert.Equal(t, got, []string{"b"}, "the second datagram arrives")
		})
	})

	t.Run("Delay", func(t *testing.T) {
		t.Parallel()

		t.Run("moves the oldest datagram in flight behind the next", func(t *testing.T) {
			t.Parallel()
			var delayed, alone bool
			var got []string
			bubbled(t, slowLink, func(c *prop.Case, first bool) {
				p := pairOf(c, true, millisecond)
				a, b := datagrams(c, p)
				_, err := a.WriteTo([]byte("x"), hostAddr("node-1:9000"))
				assert.NoError(c, err, "node-1 sends to itself")
				for _, d := range []string{"a", "b"} {
					_, err := a.WriteTo([]byte(d), hostAddr("node-2:9001"))
					assert.NoError(c, err, "node-1 sends "+d)
				}
				moved := p.n.Delay("node-2")
				single := p.n.Delay("node-1")
				if arrived := received(b); first {
					delayed, alone, got = moved, single, arrived
				}
				p.end()
			})

			assert.Equal(t, []bool{delayed, alone}, []bool{true, false},
				"two datagrams were in flight to node-2, and one to node-1")
			assert.Equal(t, got, []string{"b", "a"}, "the first datagram arrives second")
		})

		t.Run("swaps the instants of arrival of two datagrams that another one parts", func(t *testing.T) {
			t.Parallel()
			var got []string
			var at time.Duration
			bubbled(t, slowLink, func(c *prop.Case, first bool) {
				p := pairOf(c, true, millisecond)
				a, b := datagrams(c, p)
				start := time.Now()
				_, err := a.WriteTo([]byte("a"), hostAddr("node-2:9001"))
				assert.NoError(c, err, "node-1 sends a")
				time.Sleep(500 * time.Microsecond)
				_, err = a.WriteTo([]byte("x"), hostAddr("node-1:9000"))
				assert.NoError(c, err, "node-1 sends x to itself")
				_, err = a.WriteTo([]byte("b"), hostAddr("node-2:9001"))
				assert.NoError(c, err, "node-1 sends b")
				assert.True(c, p.n.Delay("node-2"), "two datagrams are in flight to node-2")
				buf := make([]byte, 1)
				_, _, err = b.ReadFrom(buf)
				assert.NoError(c, err, "node-2 reads")
				arrived := time.Since(start)
				rest := received(b)
				if first {
					got, at = append([]string{string(buf)}, rest...), arrived
				}
				p.end()
			})

			assert.Equal(t, got, []string{"b", "a"}, "b arrives first")
			assert.Equal(t, at, time.Millisecond, "b arrives at the instant of a")
		})

		t.Run("delivers two swapped datagrams that arrive together in the order of the swap", func(t *testing.T) {
			t.Parallel()
			var got []string
			bubbled(t, slowLink, func(c *prop.Case, first bool) {
				p := pairOf(c, true, millisecond)
				a, b := datagrams(c, p)
				for _, d := range []struct{ data, to string }{{"a", "node-2:9001"}, {"x", "node-1:9000"}, {"b", "node-2:9001"}} {
					_, err := a.WriteTo([]byte(d.data), hostAddr(d.to))
					assert.NoError(c, err, "node-1 sends "+d.data)
				}
				assert.True(c, p.n.Delay("node-2"), "two datagrams are in flight to node-2")
				time.Sleep(2 * time.Millisecond)
				if arrived := received(b); first {
					got = arrived
				}
				p.end()
			})

			assert.Equal(t, got, []string{"b", "a"}, "b arrives first")
		})

		t.Run("reports false without a datagram in flight", func(t *testing.T) {
			t.Parallel()
			var delayed bool
			inCase(t, targets, func(c *prop.Case, _ bool) {
				p := pairOf(c, true)
				delayed = p.n.Delay("node-2")
			})

			assert.False(t, delayed, "no datagram is in flight")
		})

		t.Run("reports false for one datagram in flight to the host", func(t *testing.T) {
			t.Parallel()
			var delayed bool
			bubbled(t, slowLink, func(c *prop.Case, first bool) {
				p := pairOf(c, true, millisecond)
				a, b := datagrams(c, p)
				_, err := a.WriteTo([]byte("a"), hostAddr("node-2:9001"))
				assert.NoError(c, err, "node-1 sends")
				if moved := p.n.Delay("node-2"); first {
					delayed = moved
				}
				received(b)
				p.end()
			})

			assert.False(t, delayed, "no datagram is in flight behind the one")
		})
	})

	t.Run("Duplicate", func(t *testing.T) {
		t.Parallel()

		t.Run("sends the oldest datagram in flight once more", func(t *testing.T) {
			t.Parallel()
			var duplicated, none bool
			var got []string
			bubbled(t, slowLink, func(c *prop.Case, first bool) {
				p := pairOf(c, true, millisecond)
				a, b := datagrams(c, p)
				_, err := a.WriteTo([]byte("a"), hostAddr("node-2:9001"))
				assert.NoError(c, err, "node-1 sends")
				twice := p.n.Duplicate("node-2")
				missing := p.n.Duplicate("node-1")
				if arrived := received(b); first {
					duplicated, none, got = twice, missing, arrived
				}
				p.end()
			})

			assert.Equal(t, []bool{duplicated, none}, []bool{true, false}, "a datagram was in flight to node-2 alone")
			assert.Equal(t, got, []string{"a", "a"}, "the datagram arrives twice")
		})
	})

	t.Run("InFlight", func(t *testing.T) {
		t.Parallel()

		t.Run("counts the datagrams in flight to the host until they arrive", func(t *testing.T) {
			t.Parallel()
			var counts []int
			bubbled(t, slowLink, func(c *prop.Case, first bool) {
				p := pairOf(c, true, millisecond)
				a, b := datagrams(c, p)
				for _, d := range []struct{ data, to string }{{"a", "node-2:9001"}, {"x", "node-1:9000"}, {"b", "node-2:9001"}} {
					_, err := a.WriteTo([]byte(d.data), hostAddr(d.to))
					assert.NoError(c, err, "node-1 sends "+d.data)
				}
				toB, toA := p.n.InFlight("node-2"), p.n.InFlight("node-1")
				received(b)
				if first {
					counts = []int{toB, toA, p.n.InFlight("node-2")}
				}
				p.end()
			})

			assert.Equal(t, counts, []int{2, 1, 0}, "two datagrams fly to node-2 and one to node-1 until they arrive")
		})
	})
}
