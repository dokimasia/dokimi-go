// Copyright Dokimasia B.V. 2026
// SPDX-License-Identifier: MIT

package sim_test

import (
	"context"
	"testing"
	"testing/synctest"
	"time"

	"go.dokimi.dev/assert"
	"go.dokimi.dev/assert/history"
	"go.dokimi.dev/assert/prop"

	"go.dokimi.dev/host"
	"go.dokimi.dev/medium"
	"go.dokimi.dev/sim"
)

func TestComponent(t *testing.T) {
	t.Parallel()

	t.Run("SpawnComponent", func(t *testing.T) {
		t.Parallel()

		t.Run("opens the first incarnation of each component on a host of its own", func(t *testing.T) {
			t.Parallel()
			var names []string
			var numbers []int
			inBubble(t, still, func(c *prop.Case, _ bool) {
				w, a, b := cells(c)
				names = []string{a.Value().env.Name(), b.Value().env.Name()}
				numbers = []int{a.Value().env.Incarnation(), b.Value().env.Incarnation()}
				w.Stop()
			})

			assert.Equal(t, names, []string{"a", "b"}, "each component runs on the host of its name")
			assert.Equal(t, numbers, []int{1, 1}, "each component runs its first incarnation")
		})

		t.Run("connects the hosts of the components on one network", func(t *testing.T) {
			t.Parallel()
			var dialErr error
			inBubble(t, still, func(c *prop.Case, _ bool) {
				w, a, b := cells(c)
				l, err := a.Value().env.Listen(7000)
				assert.NoError(c, err, "a listens")
				done := make(chan struct{})
				b.Value().env.Go(func() {
					defer close(done)
					conn, err := b.Value().env.Dial(c.Context(), "a:7000")
					if dialErr = err; err == nil {
						_ = conn.Close()
					}
				})
				a.Value().env.Go(func() {
					if conn, err := l.Accept(); err == nil {
						_ = conn.Close()
					}
				})
				<-done
				w.Stop()
			})

			assert.NoError(t, dialErr, "b dials a over the network of the world")
		})

		t.Run("waits for an open within the bound of a component", func(t *testing.T) {
			t.Parallel()
			var at time.Duration
			inBubble(t, lone, func(c *prop.Case, first bool) {
				w := sim.New(c, anatomy)
				slow := sim.SpawnComponent(w, "a", func(env *host.Env) *cell {
					env.Clock().Sleep(500 * time.Millisecond)
					return openCell(env)
				})
				if first {
					at = slow.Value().env.Clock().Monotonic()
				}
				w.Stop()
			})

			assert.Equal(t, at, time.Second, "the world advances by the bound while the open sleeps")
		})

		t.Run("fails the case for an open that does not return within the bound of a component", func(t *testing.T) {
			t.Parallel()

			got := failsInBubble(t, lone, func(c *prop.Case) {
				sim.SpawnComponent(sim.New(c, anatomy), "a", func(env *host.Env) *cell {
					_ = host.Park(env.Lifetime())
					return openCell(env)
				})
			})

			assert.Equal(
				t,
				got.Contract,
				"sim: the open of the incarnation 1 of a did not return within 1s of its host",
				"the failure states the open and the bound",
			)
		})
	})

	t.Run("Value", func(t *testing.T) {
		t.Parallel()

		t.Run("fails the case while its component has crashed", func(t *testing.T) {
			t.Parallel()

			got := failsInBubble(t, still, func(c *prop.Case) {
				w, a, _ := cells(c)
				w.Crash("a")
				a.Value()
			})

			assert.Equal(t, got.Contract, `sim: the component "a" does not run`, "the failure names the component")
		})
	})

	t.Run("Invoke", func(t *testing.T) {
		t.Parallel()

		tests := []struct {
			give error
			name string
			want history.Kind
		}{
			{name: "completes a call that returns without an error as OK", want: history.OK},
			{
				name: "completes a call that fails with an error of the component as failed", give: errEmpty,
				want: history.Fail,
			},
			{name: "completes a call whose context ended as unknown", give: context.Canceled, want: history.Unknown},
			{
				name: "completes a call past its deadline as unknown", give: context.DeadlineExceeded,
				want: history.Unknown,
			},
			{name: "completes a call that a crash ended as unknown", give: medium.ErrCrashed, want: history.Unknown},
			{name: "completes a call on a reset stream as unknown", give: host.ErrReset, want: history.Unknown},
		}
		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				t.Parallel()
				var got []completion
				inBubble(t, still, func(c *prop.Case, _ bool) {
					w, _, _ := cells(c)
					w.Invoke(0, "a", "Get", nil, nil, func(context.Context) (any, error) { return "v", tt.give })
					synctest.Wait()
					got = completions(w.History().Events())
					w.Stop()
				})

				assert.Length(t, got, 1, "the call completes")
				assert.Equal(t, got[0].kind, tt.want, "the error decides the completion")
			})
		}

		t.Run("records the output of a call that returns without an error", func(t *testing.T) {
			t.Parallel()
			var got []completion
			inBubble(t, still, func(c *prop.Case, _ bool) {
				w, a, _ := cells(c)
				w.Invoke(0, "a", "Get", nil, nil, func(ctx context.Context) (any, error) {
					assert.NoError(c, a.Value().Set(ctx, "v"), "the cell writes")
					return a.Value().Get(ctx)
				})
				synctest.Wait()
				got = completions(w.History().Events())
				w.Stop()
			})

			assert.Equal(t, got, []completion{{kind: history.OK, output: "v"}}, "the history holds the output")
		})

		t.Run("completes a call of a crashed component as unknown with medium.ErrCrashed", func(t *testing.T) {
			t.Parallel()
			var got []completion
			inBubble(t, still, func(c *prop.Case, _ bool) {
				w, _, _ := cells(c)
				w.Crash("a")
				w.Invoke(0, "a", "Get", nil, nil, func(context.Context) (any, error) { return nil, nil })
				got = completions(w.History().Events())
				w.Stop()
			})

			assert.Equal(t, got, []completion{{kind: history.Unknown, err: medium.ErrCrashed}},
				"the call of the crashed component ends at once")
		})

		t.Run("completes a call of a stopped component as unknown with sim.ErrRunEnded", func(t *testing.T) {
			t.Parallel()
			var got []completion
			inBubble(t, still, func(c *prop.Case, _ bool) {
				w, _, _ := cells(c)
				w.Stop()
				w.Invoke(0, "a", "Get", nil, nil, func(context.Context) (any, error) { return nil, nil })
				got = completions(w.History().Events())
			})

			assert.Equal(t, got, []completion{{kind: history.Unknown, err: sim.ErrRunEnded}},
				"the call of the stopped component ends at once")
		})

		t.Run("completes an open call as unknown with medium.ErrCrashed at the crash of its component",
			func(t *testing.T) {
				t.Parallel()
				var got []completion
				inBubble(t, still, func(c *prop.Case, _ bool) {
					w, a, _ := cells(c)
					parked := a.Value()
					parked.parks = true
					w.Invoke(0, "a", "Get", nil, nil, func(ctx context.Context) (any, error) { return parked.Get(ctx) })
					w.Crash("a")
					got = completions(w.History().Events())
					w.Stop()
				})

				assert.Equal(t, got, []completion{{kind: history.Unknown, err: medium.ErrCrashed}},
					"the crash ends the parked call")
			})

		t.Run("completes an open call as unknown with sim.ErrRunEnded at the stop of the world", func(t *testing.T) {
			t.Parallel()
			var got []completion
			inBubble(t, still, func(c *prop.Case, _ bool) {
				w, a, _ := cells(c)
				parked := a.Value()
				parked.parks = true
				w.Invoke(0, "a", "Get", nil, nil, func(ctx context.Context) (any, error) { return parked.Get(ctx) })
				w.Stop()
				got = completions(w.History().Events())
			})

			assert.Equal(t, got, []completion{{kind: history.Unknown, err: sim.ErrRunEnded}},
				"the stop ends the parked call")
		})

		t.Run("fails the case for a process that is no component", func(t *testing.T) {
			t.Parallel()

			got := failsInBubble(t, `[{"label": "drift.m", "value": {"type": "int", "value": 0}}]`,
				func(c *prop.Case) {
					w := sim.New(c, anatomy)
					w.Spawn("m", newStore)
					w.Invoke(0, "m", "Get", nil, nil, func(context.Context) (any, error) { return nil, nil })
				})

			assert.Equal(t, got.Contract, `sim: the process "m" is no component`, "the failure names the process")
		})
	})
}
