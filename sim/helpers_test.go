// Copyright Dokimasia B.V. 2026
// SPDX-License-Identifier: MIT

package sim_test

import (
	"io"
	"os"
	"slices"
	"testing"
	"time"

	"go.dokimi.dev/assert"
	"go.dokimi.dev/assert/prop"

	"go.dokimi.dev/medium"
	"go.dokimi.dev/sim"
	"go.dokimi.dev/spec"
)

// mediumWith returns a medium in memory whose durable file f contains data,
// beside the empty durable file e. A new medium refuses none of the calls, so
// their errors are nil.
func mediumWith(data string) *medium.Memory {
	m := medium.NewMemory()
	f, _ := m.OpenFile("f", os.O_CREATE|os.O_WRONLY, 0o600)
	_, _ = io.WriteString(f, data)
	_ = f.Sync()
	_, _ = m.OpenFile("e", os.O_CREATE|os.O_WRONLY, 0o600)
	_ = m.SyncDir(".")
	return m
}

// write writes data to the file f of m without a sync, and returns the
// error of the write.
func write(tb assert.TB, m *medium.Memory, data string) error {
	tb.Helper()
	f, err := m.OpenFile("f", os.O_WRONLY|os.O_TRUNC, 0)
	assert.NoError(tb, err, "the medium opens the file")
	_, err = io.WriteString(f, data)
	return err
}

// contentOf returns the content of the file f of m.
func contentOf(tb assert.TB, m *medium.Memory) string {
	tb.Helper()
	f, err := m.OpenFile("f", os.O_RDONLY, 0)
	assert.NoError(tb, err, "the medium opens the file")
	data, err := io.ReadAll(f)
	assert.NoError(tb, err, "the medium reads the file")
	return string(data)
}

// event is an event of the machine of the specs.
type event interface{ event() }

// started is the start of a process, an inbound call with its reply token.
type started struct{ reply uint64 }

// stopped is the stop of a process.
type stopped struct{}

// cancelled ends the wait of the caller of the inbound call reply.
type cancelled struct{ reply uint64 }

// put is an inbound call that stores value under key on the disk, within
// timeout.
type put struct {
	disk    disk
	value   string
	key     string
	reply   uint64
	timeout time.Duration
}

// committed is the result of the commit token.
type committed struct {
	err   error
	token uint64
}

// ticked is the result of the timer token.
type ticked struct{ token uint64 }

// forward makes the store put key on the process to, within timeout.
type forward struct {
	to      string
	key     string
	token   uint64
	timeout time.Duration
}

// called is the result of the call token to another process.
type called struct {
	value any
	err   error
	token uint64
}

// subscribe makes the store wait for the items of feed.
type subscribe struct {
	feed  *feed
	token uint64
}

// fed is an item of the wait token.
type fed struct {
	err   error
	item  string
	token uint64
}

// park is an inbound call that the store replies to on its stop event alone.
type park struct{ reply uint64 }

func (started) event()   {}
func (stopped) event()   {}
func (cancelled) event() {}
func (put) event()       {}
func (committed) event() {}
func (ticked) event()    {}
func (forward) event()   {}
func (called) event()    {}
func (subscribe) event() {}
func (fed) event()       {}
func (park) event()      {}

// effect is an effect of the machine of the specs.
type effect interface{ effect() }

// reply is the reply to the inbound call to.
type reply struct {
	value any
	err   error
	to    uint64
}

// commit stores value under key on the disk, within timeout.
type commit struct {
	disk    disk
	key     string
	value   string
	token   uint64
	timeout time.Duration
}

// timer waits for the duration after on the clock of its process.
type timer struct {
	token uint64
	after time.Duration
}

// call puts key on the process to, within timeout.
type call struct {
	to      string
	key     string
	token   uint64
	timeout time.Duration
}

// watch waits for the items of feed.
type watch struct {
	feed  *feed
	token uint64
}

func (reply) effect()  {}
func (commit) effect() {}
func (timer) effect()  {}
func (call) effect()   {}
func (watch) effect()  {}

// disk is the dependency of a store: the value of each key that a commit
// applied.
type disk map[string]string

// feed is the stream of a wait that a spec fills: the items that the wait has
// not passed yet, and whether the stream has ended.
type feed struct {
	items []event
	ended bool
}

// next returns the next item of f without blocking.
func (f *feed) next() (event, bool, bool) {
	if len(f.items) == 0 {
		return nil, false, f.ended
	}
	ev := f.items[0]
	f.items = f.items[1:]
	return ev, true, false
}

// storeError is an error of the stores of the specs. Its kind is string, so
// the verb %+v of fmt writes its text from an unexported field.
type storeError string

// Error returns the text of e.
func (e storeError) Error() string { return string(e) }

// errStopped is the error of a reply to a put that the stop of its process
// ends.
const errStopped storeError = "store: stopped"

// errDisk is the error of a fault of the disk.
const errDisk storeError = "disk: the write failed"

// store is a machine that commits each put to its disk before it replies.
// A put without a value makes the machine stop itself with errEmpty: it
// replies to each call that waits in the same step, and ignores every later
// event.
type store struct {
	err     error
	waiting []pending
	tokens  uint64
}

// pending is a put that waits for its commit.
type pending struct {
	token uint64
	reply uint64
}

// errEmpty is the error with which a store stops itself.
const errEmpty storeError = "store: a put without a value"

// Step applies ev.
func (s *store) Step(ev event, out []effect) []effect {
	if s.err != nil {
		return out
	}
	switch ev := ev.(type) {
	case started:
		return append(out, reply{to: ev.reply})
	case put:
		if ev.value == "" {
			s.err = errEmpty
			out = append(out, reply{to: ev.reply, err: errEmpty})
			for _, p := range s.waiting {
				out = append(out, reply{to: p.reply, err: errEmpty})
			}
			s.waiting = nil
			return out
		}
		s.tokens++
		s.waiting = append(s.waiting, pending{token: s.tokens, reply: ev.reply})
		return append(out, commit{disk: ev.disk, token: s.tokens, key: ev.key, value: ev.value, timeout: ev.timeout})
	case committed:
		at := slices.IndexFunc(s.waiting, func(p pending) bool { return p.token == ev.token })
		if at < 0 {
			return out
		}
		to := s.waiting[at].reply
		s.waiting = slices.Delete(s.waiting, at, at+1)
		return append(out, reply{to: to, value: "stored", err: ev.err})
	case cancelled:
		s.waiting = slices.DeleteFunc(s.waiting, func(p pending) bool { return p.reply == ev.reply })
		return out
	case park:
		s.waiting = append(s.waiting, pending{reply: ev.reply})
		return out
	case forward:
		if ev.token == 0 {
			s.tokens++
			ev.token = s.tokens
		}
		return append(out, call(ev))
	case subscribe:
		return append(out, watch(ev))
	case stopped:
		for _, p := range s.waiting {
			out = append(out, reply{to: p.reply, err: errStopped})
		}
		s.waiting = nil
		return out
	default:
		return out
	}
}

// Err returns the error with which the store stopped itself.
func (s *store) Err() error { return s.err }

// newStore returns a store machine.
func newStore(*sim.Clock) sim.Machine[event, effect] { return &store{} }

// anatomy is how a world reads the events and the effects of a store.
var anatomy = sim.Anatomy[event, effect]{
	Started:   func(r uint64) event { return started{reply: r} },
	Stopped:   func() event { return stopped{} },
	Cancelled: func(r uint64) event { return cancelled{reply: r} },
	Call: func(ev event) (uint64, bool) {
		switch ev := ev.(type) {
		case started:
			return ev.reply, true
		case put:
			return ev.reply, true
		case park:
			return ev.reply, true
		default:
			return 0, false
		}
	},
	Reply: func(f effect) (uint64, any, error, bool) {
		r, ok := f.(reply)
		return r.to, r.value, r.err, ok
	},
	Token: func(f effect) (uint64, bool) {
		switch f := f.(type) {
		case commit:
			return f.token, true
		case call:
			return f.token, true
		case watch:
			return f.token, true
		default:
			return 0, false
		}
	},
	Dependency: func(f effect) (string, bool) {
		switch f.(type) {
		case commit:
			return "disk", true
		case watch:
			return "feed", true
		default:
			return "", false
		}
	},
	Fail: func(f effect, err error) (event, bool) {
		switch f := f.(type) {
		case commit:
			return committed{err: err, token: f.token}, true
		case call:
			return called{err: err, token: f.token}, true
		case watch:
			return fed{err: err, token: f.token}, true
		default:
			return nil, false
		}
	},
	Errors: func(f effect) []error {
		if _, ok := f.(commit); ok {
			return []error{errDisk}
		}
		return nil
	},
	Timeout: func(f effect) time.Duration {
		switch f := f.(type) {
		case commit:
			return f.timeout
		case call:
			return f.timeout
		default:
			return 0
		}
	},
	Perform: func(w *sim.World[event, effect], p *sim.Process[event, effect], f effect) sim.Outcome[event] {
		switch f := f.(type) {
		case commit:
			if f.disk != nil {
				f.disk[f.key] = f.value
			}
			return sim.Outcome[event]{Event: committed{token: f.token}}
		case timer:
			w.After(p, f.after, ticked{token: f.token})
		case call:
			return sim.Outcome[event]{Message: &sim.Message[event]{
				To:     f.to,
				Call:   func(r uint64) event { return put{key: f.key, value: "v", reply: r} },
				Result: func(value any, err error) event { return called{value: value, err: err, token: f.token} },
			}}
		case watch:
			return sim.Outcome[event]{Next: f.feed.next}
		}
		return sim.Outcome[event]{}
	},
}

// inCase runs body in one case of the property engine, without a store of
// the failing cases.
func inCase(t *testing.T, body func(c *prop.Case)) {
	t.Helper()

	prop.ForAll(t, "the world runs in a case", func(c *prop.Case) {
		c.Draw(prop.Boolean(), "case")
		body(c)
	}, prop.Cases(1), prop.Seed(1), prop.Store(""))
}

// failure runs body in one case of a property that fails, and returns the
// record of the failure of the case.
func failure(t *testing.T, body func(c *prop.Case)) assert.Failure {
	t.Helper()

	got := assert.Rejects(t, "the world fails the case", func(tb assert.TB) {
		prop.ForAll(tb, "the world fails the case", func(c *prop.Case) {
			c.Draw(prop.Boolean(), "case")
			body(c)
		}, prop.Cases(1), prop.Seed(1), prop.Hermetic(), prop.Store(""), prop.Shrink(0))
	})
	assert.Length(t, got, 1, "the property fails once")
	inner, ok := got[0].CaseFailure()
	assert.True(t, ok, "the record of the property contains the failure of its case")
	return inner
}

// rules are the checks of the claims of sim for the machine of the specs.
var rules = []sim.Rule{
	{
		ID:        "Store/replies-once",
		Statement: "each machine of Store returns at most one reply to each inbound call",
		Claim:     spec.RepliesOnce,
		Probe:     spec.RepliesAtMostOnce,
		Reason:    spec.SecondReply,
		Form:      spec.RepliesTwice,
	},
	{
		ID:        "Store/replies-before-stop",
		Statement: "on the stop event, each machine of Store replies to each inbound call that it has not replied to",
		Claim:     spec.RepliesBeforeStop,
		Probe:     spec.RepliesOnStop,
		Reason:    spec.CallWithoutReply,
		Form:      spec.ForgetsACallOnStop,
	},
	{
		ID:        "Store/quiet-after-stop",
		Statement: "from the stop event on, each machine of Store returns replies alone",
		Claim:     spec.QuietAfterStop,
		Probe:     spec.OnlyRepliesAfterStop,
		Reason:    spec.EffectAfterStop,
		Form:      spec.WritesAfterStop,
	},
	{
		ID:        "Store/quiet-after-error",
		Statement: "each machine of Store returns no effect after the step in which its error method returns an error",
		Claim:     spec.QuietAfterError,
		Probe:     spec.NoEffectsAfterError,
		Reason:    spec.EffectAfterError,
		Form:      spec.StepsAfterError,
	},
	{
		ID: "Store/fresh-tokens",
		Statement: "each effect of a machine of Store that starts a call or a wait has a token that no earlier " +
			"effect of the machine in its epoch has",
		Claim:  spec.FreshTokens,
		Probe:  spec.TokensDiffer,
		Reason: spec.ReusedToken,
		Form:   spec.ReusesAToken,
	},
}

// simulation returns the simulation of the stores a and b with the rules of
// a runtime. Its clients put a key on a store or park a call at a store, and
// the store a forwards a put to the store b.
func simulation() sim.Simulation[event, effect, int] {
	to := func(b any) string {
		if b.(bool) {
			return "b"
		}
		return "a"
	}
	store := func(c *prop.Case) any { return c.Draw(prop.Boolean(), "to b") }
	return sim.Simulation[event, effect, int]{
		Anatomy: anatomy,
		System: func(w *sim.World[event, effect]) {
			w.Spawn("a", newStore, mediumWith("data"))
			w.Spawn("b", newStore)
		},
		Actions: []sim.Action[event, effect]{{
			Name:  "put",
			Call:  true,
			Input: store,
			Run: func(w *sim.World[event, effect], client int, b any) {
				w.Call(client, to(b), "Put", nil, nil, func(r uint64) event {
					return put{key: "k", value: "v", reply: r}
				})
			},
		}, {
			Name:  "park",
			Call:  true,
			Input: store,
			Run: func(w *sim.World[event, effect], client int, b any) {
				w.Call(client, to(b), "Park", nil, nil, func(r uint64) event { return park{reply: r} })
			},
		}, {
			Name: "forward",
			Run: func(w *sim.World[event, effect], _ int, _ any) {
				w.Deliver("a", forward{to: "b", key: "k", timeout: time.Second})
			},
		}},
		Rules: rules,
	}
}

// refuted runs s in the cases of a property on a test that expects a failure,
// and returns the record of the failure of the smallest failing case.
func refuted(t *testing.T, s sim.Simulation[event, effect, int]) assert.Failure {
	t.Helper()

	got := assert.Rejects(t, "the simulation fails", func(tb assert.TB) {
		prop.ForAll(tb, "the simulation fails", func(c *prop.Case) { sim.Run(c, s) },
			prop.Hermetic(), prop.Seed(1), prop.Store(""))
	})
	inner, ok := got[0].CaseFailure()
	assert.True(t, ok, "the record of the property contains the failure of its case")
	return inner
}

// events returns the events of the steps of the process name in the log of w.
func events(w *sim.World[event, effect], name string) []event {
	var out []event
	for _, e := range w.Log() {
		if e.Process == name && !e.Dropped {
			out = append(out, e.Event)
		}
	}
	return out
}

// single returns a world with the started process store.
func single(c *prop.Case) *sim.World[event, effect] {
	w := sim.New(c, anatomy)
	w.Spawn("store", newStore)
	w.Step("store")
	return w
}

// pair returns a world with the started processes a and b.
func pair(c *prop.Case) *sim.World[event, effect] {
	w := sim.New(c, anatomy)
	w.Spawn("a", newStore)
	w.Spawn("b", newStore)
	w.Step("a")
	w.Step("b")
	return w
}

// drain steps each process with a pending event until no process has one.
func drain(w *sim.World[event, effect]) {
	for pending := w.Pending(); len(pending) > 0; pending = w.Pending() {
		w.Step(pending[0])
	}
}
