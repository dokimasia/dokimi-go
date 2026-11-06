// Copyright Dokimasia B.V. 2026
// SPDX-License-Identifier: MIT

package host_test

import (
	"io"
	"net"
	"syscall"
	"testing"
	"testing/synctest"
	"time"

	"go.dokimi.dev/assert"
	"go.dokimi.dev/assert/prop"

	"go.dokimi.dev/host"
	"go.dokimi.dev/medium"
)

// pair is a network of the two hosts node-1 and node-2, each with its first
// incarnation.
type pair struct {
	n      *host.Network
	h2     *host.Host
	i1, i2 *host.Incarnation
	e1, e2 *host.Env
}

// pairOf returns a network of the case c of two hosts without a scheduler,
// which delays bytes for blocking, under the options opts.
func pairOf(c *prop.Case, blocking bool, opts ...host.NetworkOption) pair {
	n := host.NewNetwork(c, blocking, opts...)
	h1 := host.New(c, "node-1", n, nil)
	h2 := host.New(c, "node-2", n, nil)
	i1, e1 := h1.Start()
	i2, e2 := h2.Start()
	return pair{n: n, h2: h2, i1: i1, i2: i2, e1: e1, e2: e2}
}

// connect returns the two ends of a stream from node-1 to a listener of
// node-2 on the port 7000.
func connect(tb assert.TB, p pair) (net.Conn, net.Conn) {
	tb.Helper()

	l, err := p.e2.Listen(7000)
	assert.NoError(tb, err, "node-2 listens")
	client, err := p.e1.Dial(p.e1.Lifetime(), "node-2:7000")
	assert.NoError(tb, err, "node-1 dials node-2")
	server, err := l.Accept()
	assert.NoError(tb, err, "node-2 accepts the stream")
	return client, server
}

// selfStream returns the end of a stream of node-1 to a listener of its own
// on the port 7001.
func selfStream(tb assert.TB, p pair) net.Conn {
	tb.Helper()

	l, err := p.e1.Listen(7001)
	assert.NoError(tb, err, "node-1 listens")
	client, err := p.e1.Dial(p.e1.Lifetime(), "node-1:7001")
	assert.NoError(tb, err, "node-1 dials itself")
	_, err = l.Accept()
	assert.NoError(tb, err, "node-1 accepts")
	return client
}

// waitingRead connects node-1 to node-2, and starts a read of node-2 that
// waits for a byte. It returns the end of node-1 and the error of the read.
func waitingRead(tb assert.TB, p pair) (net.Conn, <-chan error) {
	tb.Helper()

	client, server := connect(tb, p)
	done := make(chan error, 1)
	p.i2.Go(func() {
		_, err := server.Read(make([]byte, 1))
		done <- err
	})
	synctest.Wait()
	return client, done
}

// end crashes both incarnations of p, so their goroutines end.
func (p pair) end() {
	p.i1.Crash(host.ProcessCrash)
	p.i2.Crash(host.ProcessCrash)
}

// atQuantile returns the draws of a blocking network of two hosts without
// drift whose link takes the latency at the quantile q.
func atQuantile(q string) string {
	return `[{"label": "drift.node-1", "value": {"type": "int", "value": 0}},` +
		`{"label": "drift.node-2", "value": {"type": "int", "value": 0}},` +
		`{"label": "latency.node-1.node-2", "value": {"type": "float", "value": ` + q + `}}]`
}

// arrivalOf returns the time after which one byte of node-1 arrives at node-2
// on a blocking network under the options opts, in the first case of the
// draws.
func arrivalOf(t *testing.T, draws string, opts ...host.NetworkOption) time.Duration {
	t.Helper()

	var after time.Duration
	bubbled(t, draws, func(c *prop.Case, first bool) {
		p := pairOf(c, true, opts...)
		client, server := connect(c, p)
		start := time.Now()
		_, err := client.Write([]byte("a"))
		assert.NoError(c, err, "node-1 writes")
		_, err = server.Read(make([]byte, 1))
		assert.NoError(c, err, "node-2 reads")
		if elapsed := time.Since(start); first {
			after = elapsed
		}
		p.end()
	})
	return after
}

func TestNetwork(t *testing.T) {
	t.Parallel()

	t.Run("NewNetwork", func(t *testing.T) {
		t.Parallel()

		t.Run("delivers the bytes of a stream after the latency of the link of a blocking network", func(t *testing.T) {
			t.Parallel()
			var after time.Duration
			bubbled(t, slowLink, func(c *prop.Case, first bool) {
				p := pairOf(c, true, millisecond)
				client, server := connect(c, p)
				start := time.Now()
				_, err := client.Write([]byte("ab"))
				assert.NoError(c, err, "node-1 writes")
				buf := make([]byte, 2)
				_, err = io.ReadFull(server, buf)
				assert.NoError(c, err, "node-2 reads")
				if elapsed := time.Since(start); first {
					after = elapsed
				}
				p.end()
			})

			assert.Equal(t, after, time.Millisecond, "the bytes arrive after the latency")
		})

		t.Run("delivers each byte at once without blocking", func(t *testing.T) {
			t.Parallel()
			var after time.Duration
			bubbled(t, targets, func(c *prop.Case, _ bool) {
				p := pairOf(c, false)
				client, server := connect(c, p)
				start := time.Now()
				_, err := client.Write([]byte("ab"))
				assert.NoError(c, err, "node-1 writes")
				_, err = io.ReadFull(server, make([]byte, 2))
				assert.NoError(c, err, "node-2 reads")
				after = time.Since(start)
				p.end()
			})

			assert.Equal(t, after, time.Duration(0), "the bytes arrive at once")
		})

		t.Run("fails the case for a negative lowest latency", func(t *testing.T) {
			t.Parallel()
			got := failure(t, func(c *prop.Case) {
				host.NewNetwork(c, true, host.Latency(-time.Nanosecond, time.Millisecond))
			})

			assert.Equal(t, got.Contract, "host: Latency(-1ns, 1ms) states no range of latencies",
				"the failure names the option")
		})

		t.Run("fails the case for a highest latency below the lowest", func(t *testing.T) {
			t.Parallel()
			got := failure(t, func(c *prop.Case) {
				host.NewNetwork(c, true, host.Latency(5*time.Millisecond, 2*time.Millisecond))
			})

			assert.Equal(t, got.Contract, "host: Latency(5ms, 2ms) states no range of latencies",
				"the failure names the option")
		})

		t.Run("fails the case for a lowest throughput of zero", func(t *testing.T) {
			t.Parallel()
			got := failure(t, func(c *prop.Case) { host.NewNetwork(c, true, host.Throughput(0)) })

			assert.Equal(t, got.Contract, "host: Throughput(0) states no throughput", "the failure names the option")
		})
	})

	t.Run("Latency", func(t *testing.T) {
		t.Parallel()

		t.Run("gives a link the lowest latency of the range at the target of its draw", func(t *testing.T) {
			t.Parallel()

			assert.Equal(t, arrivalOf(t, targets, host.Latency(2*time.Millisecond, 5*time.Millisecond)),
				2*time.Millisecond, "the byte arrives after the lowest latency")
		})

		t.Run("gives a link the highest latency of the range at the quantile 1", func(t *testing.T) {
			t.Parallel()

			assert.Equal(t, arrivalOf(t, atQuantile("1.0"), host.Latency(2*time.Millisecond, 5*time.Millisecond)),
				5*time.Millisecond, "the byte arrives after the highest latency")
		})

		t.Run("gives a link the median of the exponential distribution with the rate 5 at the quantile 0.5",
			func(t *testing.T) {
				t.Parallel()

				after := arrivalOf(t, atQuantile("0.5"), host.Latency(10*time.Millisecond, 30*time.Millisecond))

				assert.InRange(t, after, float64(12772*time.Microsecond), float64(12773*time.Microsecond),
					"10 ms and 20 ms times ln 2 / 5 are 12.7726 ms")
			})

		t.Run("takes a range of 0 to 100 ms without the option", func(t *testing.T) {
			t.Parallel()

			assert.Equal(t, arrivalOf(t, atQuantile("1.0")), 100*time.Millisecond,
				"the byte arrives after the highest latency of the default range")
		})
	})

	t.Run("Throughput", func(t *testing.T) {
		t.Parallel()

		t.Run("gives a link the time of a byte at the lowest throughput at the quantile 1", func(t *testing.T) {
			t.Parallel()

			assert.Equal(t, arrivalOf(t, narrowStream, tenMegabytes), 100*time.Nanosecond,
				"one byte at ten megabytes per second takes 100 ns")
		})

		t.Run("gives a link no time for a byte at the target of its draw", func(t *testing.T) {
			t.Parallel()

			assert.Equal(t, arrivalOf(t, targets, tenMegabytes), time.Duration(0), "the byte arrives at once")
		})
	})

	t.Run("Partition", func(t *testing.T) {
		t.Parallel()

		t.Run("resets each stream across the cut of a network without blocking", func(t *testing.T) {
			t.Parallel()
			var err error
			bubbled(t, targets, func(c *prop.Case, _ bool) {
				p := pairOf(c, false)
				client, _ := connect(c, p)
				p.n.Partition([]string{"node-1"}, []string{"node-2"})
				_, err = client.Write([]byte("ab"))
				p.end()
			})

			assert.ErrorIs(t, err, host.ErrReset, "the cut resets the stream")
		})

		t.Run("wakes a read of a stream across the cut of a network without blocking", func(t *testing.T) {
			t.Parallel()
			var err error
			bubbled(t, targets, func(c *prop.Case, _ bool) {
				p := pairOf(c, false)
				_, server := waitingRead(c, p)
				p.n.Partition([]string{"node-1"}, []string{"node-2"})
				err = <-server
				p.end()
			})

			assert.ErrorIs(t, err, host.ErrReset, "the cut ends the read")
		})

		t.Run("keeps each stream of a network without blocking that one side contains", func(t *testing.T) {
			t.Parallel()
			var err error
			bubbled(t, targets, func(c *prop.Case, _ bool) {
				p := pairOf(c, false)
				client, server := connect(c, p)
				p.n.Partition([]string{"node-1", "node-2"})
				_, err = client.Write([]byte("a"))
				assert.NoError(c, err, "node-1 writes within its side")
				_, err = server.Read(make([]byte, 1))
				p.end()
			})

			assert.NoError(t, err, "the stream within a side runs")
		})

		t.Run("delays the bytes of a stream across the cut of a blocking network until Repair", func(t *testing.T) {
			t.Parallel()
			var during int
			bubbled(t, targets, func(c *prop.Case, _ bool) {
				p := pairOf(c, true)
				client, server := connect(c, p)
				p.n.Partition([]string{"node-1"}, []string{"node-2"})
				_, err := client.Write([]byte("ab"))
				assert.NoError(c, err, "node-1 writes across the cut")
				done := make(chan []byte, 1)
				p.i2.Go(func() {
					buf := make([]byte, 2)
					_, _ = io.ReadFull(server, buf)
					done <- buf
				})
				time.Sleep(time.Second)
				during = len(done)
				p.n.Repair()
				assert.Equal(c, string(<-done), "ab", "the bytes arrive after the repair")
				p.end()
			})

			assert.Equal(t, during, 0, "no byte crosses the cut")
		})

		t.Run("refuses a dial across the cut of a network without blocking", func(t *testing.T) {
			t.Parallel()
			var err error
			bubbled(t, targets, func(c *prop.Case, _ bool) {
				p := pairOf(c, false)
				_, listenErr := p.e2.Listen(7000)
				assert.NoError(c, listenErr, "node-2 listens")
				p.n.Partition([]string{"node-1"}, []string{"node-2"})
				_, err = p.e1.Dial(c.Context(), "node-2:7000")
				p.end()
			})

			assert.ErrorIs(t, err, syscall.ECONNREFUSED, "the cut refuses the dial")
		})

		t.Run("makes a dial across the cut of a blocking network wait for Repair", func(t *testing.T) {
			t.Parallel()
			var during int
			bubbled(t, targets, func(c *prop.Case, _ bool) {
				p := pairOf(c, true)
				_, listenErr := p.e2.Listen(7000)
				assert.NoError(c, listenErr, "node-2 listens")
				p.n.Partition([]string{"node-1"}, []string{"node-2"})
				done := make(chan error, 1)
				p.i1.Go(func() {
					_, err := p.e1.Dial(c.Context(), "node-2:7000")
					done <- err
				})
				synctest.Wait()
				during = len(done)
				p.n.Repair()
				assert.NoError(c, <-done, "the dial connects after the repair")
				p.end()
			})

			assert.Equal(t, during, 0, "the dial waits during the cut")
		})

		t.Run("keeps the bytes of a host to itself across every cut", func(t *testing.T) {
			t.Parallel()
			var got string
			bubbled(t, targets, func(c *prop.Case, _ bool) {
				p := pairOf(c, false)
				l, err := p.e1.Listen(7000)
				assert.NoError(c, err, "node-1 listens")
				p.n.Partition([]string{"node-2"})
				client, err := p.e1.Dial(c.Context(), "node-1:7000")
				assert.NoError(c, err, "node-1 dials itself")
				server, err := l.Accept()
				assert.NoError(c, err, "node-1 accepts")
				_, err = client.Write([]byte("ab"))
				assert.NoError(c, err, "node-1 writes to itself")
				buf := make([]byte, 2)
				_, err = io.ReadFull(server, buf)
				assert.NoError(c, err, "node-1 reads")
				got = string(buf)
				p.end()
			})

			assert.Equal(t, got, "ab", "the loopback crosses no cut")
		})
	})

	t.Run("Reset", func(t *testing.T) {
		t.Parallel()

		t.Run("wakes a read of a stream between the two hosts", func(t *testing.T) {
			t.Parallel()
			var err error
			bubbled(t, targets, func(c *prop.Case, _ bool) {
				p := pairOf(c, false)
				_, server := waitingRead(c, p)
				p.n.Reset("node-1", "node-2")
				err = <-server
				p.end()
			})

			assert.ErrorIs(t, err, host.ErrReset, "the reset ends the read")
		})

		t.Run("fails the calls of both ends of each stream between two hosts with ErrReset", func(t *testing.T) {
			t.Parallel()
			var errs []error
			var loopback error
			bubbled(t, targets, func(c *prop.Case, _ bool) {
				p := pairOf(c, false)
				client, server := connect(c, p)
				self := selfStream(c, p)
				p.n.Reset("node-2", "node-1")
				_, write := client.Write([]byte("ab"))
				_, read := server.Read(make([]byte, 1))
				errs = []error{write, read}
				_, loopback = self.Write([]byte("a"))
				p.end()
			})

			for _, err := range errs {
				assert.ErrorIs(t, err, host.ErrReset, "the reset ends the stream")
			}
			assert.NoError(t, loopback, "the stream of node-1 to itself runs on")
		})
	})

	t.Run("Slow", func(t *testing.T) {
		t.Parallel()

		t.Run("raises the latency of the link to the latency of the call", func(t *testing.T) {
			t.Parallel()
			var after time.Duration
			bubbled(t, targets, func(c *prop.Case, first bool) {
				p := pairOf(c, true)
				client, server := connect(c, p)
				p.n.Slow("node-1", "node-2", time.Second)
				start := time.Now()
				_, err := client.Write([]byte("a"))
				assert.NoError(c, err, "node-1 writes")
				_, err = server.Read(make([]byte, 1))
				assert.NoError(c, err, "node-2 reads")
				if elapsed := time.Since(start); first {
					after = elapsed
				}
				p.end()
			})

			assert.Equal(t, after, time.Second, "the byte arrives after the latency of Slow")
		})

		t.Run("fails the case for a network without blocking", func(t *testing.T) {
			t.Parallel()
			got := failure(t, func(c *prop.Case) {
				p := pairOf(c, false)
				p.n.Slow("node-1", "node-2", time.Second)
			})

			assert.Equal(t, got.Contract, "host: Slow(node-1, node-2) of a network without blocking",
				"the failure names the call")
		})

		t.Run("fails the case for two hosts without a link", func(t *testing.T) {
			t.Parallel()
			got := failure(t, func(c *prop.Case) {
				p := pairOf(c, true)
				p.n.Slow("node-1", "node-9", time.Second)
			})

			assert.Equal(t, got.Contract, "host: Slow(node-1, node-9) of two hosts without a link",
				"the failure names the call")
		})
	})

	t.Run("Restore", func(t *testing.T) {
		t.Parallel()

		t.Run("ends the latency of Slow", func(t *testing.T) {
			t.Parallel()
			var after time.Duration
			bubbled(t, targets, func(c *prop.Case, first bool) {
				p := pairOf(c, true)
				client, server := connect(c, p)
				p.n.Slow("node-1", "node-2", time.Second)
				p.n.Restore("node-2", "node-1")
				p.n.Restore("node-1", "node-9")
				start := time.Now()
				_, err := client.Write([]byte("a"))
				assert.NoError(c, err, "node-1 writes")
				_, err = server.Read(make([]byte, 1))
				assert.NoError(c, err, "node-2 reads")
				if elapsed := time.Since(start); first {
					after = elapsed
				}
				p.end()
			})

			assert.Equal(t, after, time.Duration(0), "the byte arrives without the latency of Slow")
		})
	})

	t.Run("Crash", func(t *testing.T) {
		t.Parallel()

		t.Run("resets the streams of a crashed incarnation for its peer", func(t *testing.T) {
			t.Parallel()
			var err, loopback error
			bubbled(t, targets, func(c *prop.Case, _ bool) {
				p := pairOf(c, false)
				client, _ := connect(c, p)
				self := selfStream(c, p)
				p.i2.Crash(host.ProcessCrash)
				_, err = client.Read(make([]byte, 1))
				_, loopback = self.Write([]byte("a"))
				p.i1.Crash(host.ProcessCrash)
			})

			assert.ErrorIs(t, err, host.ErrReset, "the peer reads a reset")
			assert.NoError(t, loopback, "the stream of node-1 to itself runs on")
		})

		t.Run("resets the streams of a crashed dialer for its peer", func(t *testing.T) {
			t.Parallel()
			var err error
			bubbled(t, targets, func(c *prop.Case, _ bool) {
				p := pairOf(c, false)
				_, server := connect(c, p)
				p.i1.Crash(host.ProcessCrash)
				_, err = server.Read(make([]byte, 1))
				p.i2.Crash(host.ProcessCrash)
			})

			assert.ErrorIs(t, err, host.ErrReset, "the peer reads a reset")
		})

		t.Run("keeps the listener and the connection of datagrams of another incarnation", func(t *testing.T) {
			t.Parallel()
			var dial, packet error
			bubbled(t, targets, func(c *prop.Case, _ bool) {
				p := pairOf(c, false)
				_, err := p.e1.Listen(7001)
				assert.NoError(c, err, "node-1 listens")
				conn, err := p.e1.ListenPacket(9000)
				assert.NoError(c, err, "node-1 listens for datagrams")
				p.i2.Crash(host.ProcessCrash)
				_, dial = p.e1.Dial(c.Context(), "node-1:7001")
				_, packet = conn.WriteTo([]byte("a"), hostAddr("node-1:9000"))
				if packet == nil {
					_, _, packet = conn.ReadFrom(make([]byte, 1))
				}
				p.i1.Crash(host.ProcessCrash)
			})

			assert.NoError(t, dial, "the listener of node-1 takes the dial")
			assert.NoError(t, packet, "the connection of node-1 reads its datagram")
		})

		t.Run("ends a wait of an accept of a crashed incarnation", func(t *testing.T) {
			t.Parallel()
			var err error
			bubbled(t, targets, func(c *prop.Case, _ bool) {
				p := pairOf(c, false)
				l, listenErr := p.e2.Listen(7000)
				assert.NoError(c, listenErr, "node-2 listens")
				done := make(chan error, 1)
				p.i2.Go(func() {
					_, acceptErr := l.Accept()
					done <- acceptErr
				})
				synctest.Wait()
				p.i2.Crash(host.ProcessCrash)
				err = <-done
				p.i1.Crash(host.ProcessCrash)
			})

			assert.ErrorIs(t, err, medium.ErrCrashed, "the crash ends the accept")
		})

		t.Run("closes the listener of a crashed incarnation", func(t *testing.T) {
			t.Parallel()
			var err error
			bubbled(t, targets, func(c *prop.Case, _ bool) {
				p := pairOf(c, false)
				_, listenErr := p.e2.Listen(7000)
				assert.NoError(c, listenErr, "node-2 listens")
				p.i2.Crash(host.ProcessCrash)
				_, err = p.e1.Dial(c.Context(), "node-2:7000")
				p.i1.Crash(host.ProcessCrash)
			})

			assert.ErrorIs(t, err, syscall.ECONNREFUSED, "no listener takes the dial")
		})

		t.Run("frees the ports of a crashed incarnation", func(t *testing.T) {
			t.Parallel()
			var errs []error
			bubbled(t, targets, func(c *prop.Case, _ bool) {
				p := pairOf(c, false)
				_, err := p.e2.ListenPacket(9000)
				assert.NoError(c, err, "node-2 listens for datagrams")
				_, err = p.e2.Listen(7000)
				assert.NoError(c, err, "node-2 listens for streams")
				p.i2.Crash(host.ProcessCrash)
				next, env := p.h2.Start()
				_, packet := env.ListenPacket(9000)
				_, stream := env.Listen(7000)
				errs = []error{packet, stream}
				next.Crash(host.ProcessCrash)
				p.i1.Crash(host.ProcessCrash)
			})

			assert.Equal(t, errs, []error{nil, nil}, "the next incarnation takes both ports")
		})
	})
}
