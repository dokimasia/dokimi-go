// Copyright Dokimasia B.V. 2026
// SPDX-License-Identifier: MIT

package host_test

import (
	"context"
	"io"
	"net"
	"os"
	"strconv"
	"syscall"
	"testing"
	"testing/synctest"
	"time"

	"go.dokimi.dev/assert"
	"go.dokimi.dev/assert/prop"
	"go.dokimi.dev/assert/stateful"

	"go.dokimi.dev/host"
	"go.dokimi.dev/medium"
)

func TestStream(t *testing.T) {
	t.Parallel()

	t.Run("Read", func(t *testing.T) {
		t.Parallel()

		t.Run("returns io.EOF after the bytes in flight once the peer closes", func(t *testing.T) {
			t.Parallel()
			var got []byte
			var err error
			bubbled(t, targets, func(c *prop.Case, _ bool) {
				p := pairOf(c, true)
				client, server := connect(c, p)
				_, writeErr := client.Write([]byte("ab"))
				assert.NoError(c, writeErr, "node-1 writes")
				assert.NoError(c, client.Close(), "node-1 closes")
				got, err = io.ReadAll(server)
				p.end()
			})

			assert.NoError(t, err, "ReadAll stops at io.EOF")
			assert.Equal(t, string(got), "ab", "the bytes before the close")
		})

		t.Run("returns io.EOF after the latency of the close of the peer", func(t *testing.T) {
			t.Parallel()
			var err error
			var after time.Duration
			bubbled(t, slowLink, func(c *prop.Case, first bool) {
				p := pairOf(c, true, millisecond)
				client, server := connect(c, p)
				start := time.Now()
				assert.NoError(c, client.Close(), "node-1 closes")
				_, readErr := server.Read(make([]byte, 1))
				if elapsed := time.Since(start); first {
					err, after = readErr, elapsed
				}
				p.end()
			})

			assert.ErrorIs(t, err, io.EOF, "the close arrives")
			assert.Equal(t, after, time.Millisecond, "the close arrives after the latency")
		})

		t.Run("returns the bytes of a later write before the close of the peer", func(t *testing.T) {
			t.Parallel()
			var got []string
			bubbled(t, slowLink, func(c *prop.Case, first bool) {
				p := pairOf(c, true, millisecond)
				client, server := connect(c, p)
				_, err := client.Write([]byte("a"))
				assert.NoError(c, err, "node-1 writes a")
				time.Sleep(500 * time.Microsecond)
				_, err = client.Write([]byte("b"))
				assert.NoError(c, err, "node-1 writes b")
				buf := make([]byte, 1)
				parts := make([]string, 0, 2)
				for range 2 {
					n, readErr := server.Read(buf)
					assert.NoError(c, readErr, "node-2 reads")
					parts = append(parts, string(buf[:n]))
				}
				if first {
					got = parts
				}
				p.end()
			})

			assert.Equal(t, got, []string{"a", "b"}, "each write arrives as its own part")
		})

		t.Run("delays each write behind the earlier writes by its size over the throughput", func(t *testing.T) {
			t.Parallel()
			var after time.Duration
			bubbled(t, narrowStream, func(c *prop.Case, first bool) {
				p := pairOf(c, true, tenMegabytes)
				client, server := connect(c, p)
				start := time.Now()
				for range 2 {
					_, err := client.Write([]byte("0123456789"))
					assert.NoError(c, err, "node-1 writes ten bytes")
				}
				_, err := io.ReadFull(server, make([]byte, 20))
				assert.NoError(c, err, "node-2 reads twenty bytes")
				if elapsed := time.Since(start); first {
					after = elapsed
				}
				p.end()
			})

			assert.Equal(t, after, 2*time.Microsecond, "twenty bytes of 100 ns each")
		})

		t.Run("returns the bytes that fit the buffer, and the rest at the next read", func(t *testing.T) {
			t.Parallel()
			var parts []string
			bubbled(t, targets, func(c *prop.Case, _ bool) {
				p := pairOf(c, false)
				client, server := connect(c, p)
				_, err := client.Write([]byte("abc"))
				assert.NoError(c, err, "node-1 writes")
				buf := make([]byte, 2)
				n, _ := server.Read(buf)
				first := string(buf[:n])
				n, _ = server.Read(buf)
				parts = []string{first, string(buf[:n])}
				p.end()
			})

			assert.Equal(t, parts, []string{"ab", "c"}, "two reads of a buffer of two bytes")
		})

		t.Run("fails with os.ErrDeadlineExceeded once the read deadline passes", func(t *testing.T) {
			t.Parallel()
			var err error
			var waited time.Duration
			bubbled(t, targets, func(c *prop.Case, _ bool) {
				p := pairOf(c, false)
				_, server := connect(c, p)
				start := time.Now()
				assert.NoError(c, server.SetReadDeadline(start.Add(time.Second)), "the end sets its deadline")
				_, err = server.Read(make([]byte, 1))
				waited = time.Since(start)
				p.end()
			})

			assert.ErrorIs(t, err, os.ErrDeadlineExceeded, "the deadline ends the read")
			assert.Equal(t, waited, time.Second, "the read waits until the deadline")
		})

		t.Run("fails with net.ErrClosed once the end closes", func(t *testing.T) {
			t.Parallel()
			var err error
			bubbled(t, targets, func(c *prop.Case, _ bool) {
				p := pairOf(c, false)
				client, _ := connect(c, p)
				assert.NoError(c, client.Close(), "node-1 closes")
				_, err = client.Read(make([]byte, 1))
				p.end()
			})

			assert.ErrorIs(t, err, net.ErrClosed, "a closed end reads nothing")
		})

		t.Run("fails with ErrCrashed once its incarnation crashes", func(t *testing.T) {
			t.Parallel()
			var err error
			bubbled(t, targets, func(c *prop.Case, _ bool) {
				p := pairOf(c, false)
				client, _ := connect(c, p)
				done := make(chan error, 1)
				p.i1.Go(func() {
					_, readErr := client.Read(make([]byte, 1))
					done <- readErr
				})
				synctest.Wait()
				p.i1.Crash(host.ProcessCrash)
				err = <-done
				p.i2.Crash(host.ProcessCrash)
			})

			assert.ErrorIs(t, err, medium.ErrCrashed, "the crash ends the read")
		})
	})

	t.Run("Write", func(t *testing.T) {
		t.Parallel()

		t.Run("returns the count of the bytes that it sends", func(t *testing.T) {
			t.Parallel()
			var n int
			bubbled(t, targets, func(c *prop.Case, _ bool) {
				p := pairOf(c, false)
				client, _ := connect(c, p)
				var err error
				n, err = client.Write([]byte("abc"))
				assert.NoError(c, err, "node-1 writes")
				p.end()
			})

			assert.Equal(t, n, 3, "the write counts each byte")
		})

		tests := []struct {
			name string
			give func(c net.Conn)
			want error
		}{
			{
				name: "fails with net.ErrClosed once the end closes",
				give: func(c net.Conn) { _ = c.Close() },
				want: net.ErrClosed,
			},
			{
				name: "fails with os.ErrDeadlineExceeded once the write deadline passes",
				give: func(c net.Conn) { _ = c.SetWriteDeadline(time.Now().Add(-time.Second)) },
				want: os.ErrDeadlineExceeded,
			},
			{
				name: "fails with os.ErrDeadlineExceeded once the deadline passes",
				give: func(c net.Conn) { _ = c.SetDeadline(time.Now().Add(-time.Second)) },
				want: os.ErrDeadlineExceeded,
			},
		}
		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				t.Parallel()
				var err error
				bubbled(t, targets, func(c *prop.Case, _ bool) {
					p := pairOf(c, false)
					client, _ := connect(c, p)
					tt.give(client)
					_, err = client.Write([]byte("a"))
					p.end()
				})

				assert.ErrorIs(t, err, tt.want, "the write fails")
			})
		}

		t.Run("fails with ErrCrashed once its incarnation crashes", func(t *testing.T) {
			t.Parallel()
			var errs []error
			bubbled(t, targets, func(c *prop.Case, _ bool) {
				p := pairOf(c, false)
				client, _ := connect(c, p)
				p.i1.Crash(host.ProcessCrash)
				_, write := client.Write([]byte("a"))
				errs = []error{write, client.Close()}
				p.i2.Crash(host.ProcessCrash)
			})

			for _, err := range errs {
				assert.ErrorIs(t, err, medium.ErrCrashed, "the fence refuses the call")
			}
		})

		t.Run("fails with ErrReset after a reset", func(t *testing.T) {
			t.Parallel()
			var errs []error
			bubbled(t, targets, func(c *prop.Case, _ bool) {
				p := pairOf(c, false)
				client, _ := connect(c, p)
				p.n.Reset("node-1", "node-2")
				_, write := client.Write([]byte("a"))
				errs = []error{write, client.Close()}
				p.end()
			})

			assert.ErrorIs(t, errs[0], host.ErrReset, "the write fails")
			assert.NoError(t, errs[1], "the end closes after the reset")
		})
	})

	t.Run("Close", func(t *testing.T) {
		t.Parallel()

		t.Run("fails a second close with net.ErrClosed", func(t *testing.T) {
			t.Parallel()
			var err error
			bubbled(t, targets, func(c *prop.Case, _ bool) {
				p := pairOf(c, false)
				client, _ := connect(c, p)
				assert.NoError(c, client.Close(), "the first close")
				err = client.Close()
				p.end()
			})

			assert.ErrorIs(t, err, net.ErrClosed, "the end closed already")
		})
	})

	t.Run("LocalAddr", func(t *testing.T) {
		t.Parallel()

		t.Run("returns the address of each end and of its peer", func(t *testing.T) {
			t.Parallel()
			var got []string
			bubbled(t, targets, func(c *prop.Case, _ bool) {
				p := pairOf(c, false)
				client, server := connect(c, p)
				got = []string{
					client.LocalAddr().String(), client.RemoteAddr().String(),
					server.LocalAddr().String(), server.RemoteAddr().String(), client.LocalAddr().Network(),
				}
				p.end()
			})

			assert.Equal(t, got, []string{"node-1:49152", "node-2:7000", "node-2:7000", "node-1:49152", "tcp"},
				"the dial takes the first port of the dynamic range")
		})
	})

	t.Run("SetReadDeadline", func(t *testing.T) {
		t.Parallel()

		t.Run("wakes a read at a later deadline", func(t *testing.T) {
			t.Parallel()
			var err error
			bubbled(t, targets, func(c *prop.Case, _ bool) {
				p := pairOf(c, false)
				_, server := connect(c, p)
				done := make(chan error, 1)
				p.i2.Go(func() {
					_, readErr := server.Read(make([]byte, 1))
					done <- readErr
				})
				synctest.Wait()
				assert.NoError(c, server.SetReadDeadline(time.Now()), "the end sets a deadline that passed")
				err = <-done
				p.end()
			})

			assert.ErrorIs(t, err, os.ErrDeadlineExceeded, "the deadline ends the read in progress")
		})
	})

	t.Run("SetDeadline", func(t *testing.T) {
		t.Parallel()

		t.Run("wakes a read at a later deadline", func(t *testing.T) {
			t.Parallel()
			var err error
			bubbled(t, targets, func(c *prop.Case, _ bool) {
				p := pairOf(c, false)
				_, server := connect(c, p)
				done := make(chan error, 1)
				p.i2.Go(func() {
					_, readErr := server.Read(make([]byte, 1))
					done <- readErr
				})
				synctest.Wait()
				assert.NoError(c, server.SetDeadline(time.Now()), "the end sets a deadline that passed")
				err = <-done
				p.end()
			})

			assert.ErrorIs(t, err, os.ErrDeadlineExceeded, "the deadline ends the read in progress")
		})
	})

	t.Run("Accept", func(t *testing.T) {
		t.Parallel()

		t.Run("returns the end of a dial that comes while it waits", func(t *testing.T) {
			t.Parallel()
			var remote string
			bubbled(t, targets, func(c *prop.Case, _ bool) {
				p := pairOf(c, false)
				l, err := p.e2.Listen(7000)
				assert.NoError(c, err, "node-2 listens")
				done := make(chan net.Conn, 1)
				p.i2.Go(func() {
					conn, acceptErr := l.Accept()
					assert.NoError(c, acceptErr, "node-2 accepts")
					done <- conn
				})
				synctest.Wait()
				_, err = p.e1.Dial(c.Context(), "node-2:7000")
				assert.NoError(c, err, "node-1 dials")
				remote = (<-done).RemoteAddr().String()
				p.end()
			})

			assert.Equal(t, remote, "node-1:49152", "the accept takes the stream of the dial")
		})

		t.Run("returns the end of each dial in the order of the dials", func(t *testing.T) {
			t.Parallel()
			var remotes []string
			bubbled(t, targets, func(c *prop.Case, _ bool) {
				p := pairOf(c, false)
				l, err := p.e2.Listen(7000)
				assert.NoError(c, err, "node-2 listens")
				for range 2 {
					_, err := p.e1.Dial(c.Context(), "node-2:7000")
					assert.NoError(c, err, "node-1 dials")
				}
				first, _ := l.Accept()
				second, _ := l.Accept()
				remotes = []string{first.RemoteAddr().String(), second.RemoteAddr().String(), l.Addr().String()}
				p.end()
			})

			assert.Equal(t, remotes, []string{"node-1:49152", "node-1:49153", "node-2:7000"}, "the order of the dials")
		})

		t.Run("fails with net.ErrClosed once the listener closes", func(t *testing.T) {
			t.Parallel()
			var errs []error
			bubbled(t, targets, func(c *prop.Case, _ bool) {
				p := pairOf(c, false)
				l, err := p.e2.Listen(7000)
				assert.NoError(c, err, "node-2 listens")
				done := make(chan error, 1)
				p.i2.Go(func() {
					_, acceptErr := l.Accept()
					done <- acceptErr
				})
				synctest.Wait()
				assert.NoError(c, l.Close(), "the listener closes")
				accept := <-done
				second := l.Close()
				_, dial := p.e1.Dial(c.Context(), "node-2:7000")
				errs = []error{accept, second, dial}
				p.end()
			})

			assert.ErrorIs(t, errs[0], net.ErrClosed, "the close ends the accept")
			assert.ErrorIs(t, errs[1], net.ErrClosed, "a second close fails")
			assert.ErrorIs(t, errs[2], syscall.ECONNREFUSED, "a dial of a closed listener is refused")
		})

		t.Run("fails with ErrCrashed once its incarnation crashes", func(t *testing.T) {
			t.Parallel()
			var err error
			bubbled(t, targets, func(c *prop.Case, _ bool) {
				p := pairOf(c, false)
				l, listenErr := p.e2.Listen(7000)
				assert.NoError(c, listenErr, "node-2 listens")
				p.i2.Crash(host.ProcessCrash)
				_, err = l.Accept()
				p.i1.Crash(host.ProcessCrash)
			})

			assert.ErrorIs(t, err, medium.ErrCrashed, "the fence refuses the accept")
		})
	})

	t.Run("Listen", func(t *testing.T) {
		t.Parallel()

		t.Run("refuses a port that a listener takes", func(t *testing.T) {
			t.Parallel()
			var err error
			bubbled(t, targets, func(c *prop.Case, _ bool) {
				p := pairOf(c, false)
				_, listenErr := p.e2.Listen(7000)
				assert.NoError(c, listenErr, "node-2 listens")
				_, err = p.e2.Listen(7000)
				p.end()
			})

			assert.ErrorIs(t, err, syscall.EADDRINUSE, "the port is taken")
		})

		t.Run("fails with ErrCrashed for a crashed incarnation", func(t *testing.T) {
			t.Parallel()
			var errs []error
			bubbled(t, targets, func(c *prop.Case, _ bool) {
				p := pairOf(c, false)
				p.i1.Crash(host.ProcessCrash)
				_, listen := p.e1.Listen(7000)
				_, dial := p.e1.Dial(c.Context(), "node-2:7000")
				_, packet := p.e1.ListenPacket(9000)
				errs = []error{listen, dial, packet}
				p.i2.Crash(host.ProcessCrash)
			})

			for _, err := range errs {
				assert.ErrorIs(t, err, medium.ErrCrashed, "the fence refuses the call")
			}
		})
	})

	t.Run("Dial", func(t *testing.T) {
		t.Parallel()

		t.Run("refuses an address without a listener", func(t *testing.T) {
			t.Parallel()
			var err error
			bubbled(t, targets, func(c *prop.Case, _ bool) {
				p := pairOf(c, false)
				_, err = p.e1.Dial(c.Context(), "node-2:7000")
				p.end()
			})

			assert.ErrorIs(t, err, syscall.ECONNREFUSED, "no listener takes the dial")
		})

		t.Run("refuses an address that does not parse", func(t *testing.T) {
			t.Parallel()
			var errs []error
			bubbled(t, targets, func(c *prop.Case, _ bool) {
				p := pairOf(c, false)
				_, noPort := p.e1.Dial(c.Context(), "node-2")
				_, word := p.e1.Dial(c.Context(), "node-2:http")
				errs = []error{noPort, word}
				p.end()
			})

			_ = assert.ErrorAs[*net.AddrError](t, errs[0], "the address has no port")
			_ = assert.ErrorAs[*strconv.NumError](t, errs[1], "the port is no number")
		})

		t.Run("fails with the error of its context", func(t *testing.T) {
			t.Parallel()
			var err error
			bubbled(t, targets, func(c *prop.Case, _ bool) {
				p := pairOf(c, true)
				p.n.Partition([]string{"node-1"}, []string{"node-2"})
				ctx, cancel := context.WithCancel(c.Context())
				done := make(chan error, 1)
				p.i1.Go(func() {
					_, dialErr := p.e1.Dial(ctx, "node-2:7000")
					done <- dialErr
				})
				synctest.Wait()
				cancel()
				err = <-done
				p.end()
			})

			assert.ErrorIs(t, err, context.Canceled, "the end of the context ends the dial")
		})

		t.Run("fails with ErrCrashed once its incarnation crashes during the wait", func(t *testing.T) {
			t.Parallel()
			var err error
			bubbled(t, targets, func(c *prop.Case, _ bool) {
				p := pairOf(c, true)
				p.n.Partition([]string{"node-1"}, []string{"node-2"})
				done := make(chan error, 1)
				p.i1.Go(func() {
					_, dialErr := p.e1.Dial(c.Context(), "node-2:7000")
					done <- dialErr
				})
				synctest.Wait()
				p.i1.Crash(host.ProcessCrash)
				err = <-done
				p.i2.Crash(host.ProcessCrash)
			})

			assert.ErrorIs(t, err, medium.ErrCrashed, "the crash ends the dial")
		})
	})

	t.Run("delivers the bytes of a stream between two tasks of a scheduler", func(t *testing.T) {
		t.Parallel()
		var got string
		scheduled(t, slowLink, func(c *prop.Case, s *stateful.Scheduler, first bool) {
			n := host.NewNetwork(c, true, millisecond)
			h1 := host.New(c, "node-1", n, s)
			h2 := host.New(c, "node-2", n, s)
			_, e1 := h1.Start()
			_, e2 := h2.Start()
			l, err := e2.Listen(7000)
			assert.NoError(c, err, "node-2 listens")
			received := ""
			e2.Go(func() {
				conn, err := l.Accept()
				assert.NoError(c, err, "node-2 accepts")
				buf := make([]byte, 2)
				_, err = io.ReadFull(conn, buf)
				assert.NoError(c, err, "node-2 reads")
				received = string(buf)
			})
			e1.Go(func() {
				conn, err := e1.Dial(c.Context(), "node-2:7000")
				assert.NoError(c, err, "node-1 dials")
				_, err = conn.Write([]byte("ab"))
				assert.NoError(c, err, "node-1 writes")
			})
			s.Advance(time.Millisecond)
			if first {
				got = received
			}
		})

		assert.Equal(t, got, "ab", "the bytes arrive within the latency of the link")
	})
}
