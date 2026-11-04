// Copyright Dokimasia B.V. 2026
// SPDX-License-Identifier: MIT

package probe_test

import (
	"context"
	"slices"
	"strconv"
	"strings"
	"testing"

	"go.dokimi.dev/assert"
	"go.dokimi.dev/assert/prop"
	"go.dokimi.dev/assert/stateful"
	"go.dokimi.dev/spec"

	"go.dokimi.dev/conform"
	"go.dokimi.dev/conform/probe"
)

// item is a value of a ledger with its version. The first write of a key
// stores the version 0, and each later write raises it.
type item struct {
	Value   int
	Version int
}

// stamped is an entry of the log of a ledger with its stamp, the position of
// the entry in the log from 1.
type stamped struct {
	Stamp int
	Entry int
}

// ledger is the subject of the specs of sequences. broken names the defect of
// a planted ledger, which an operation shows from its fourth call on. It is
// empty for a correct ledger. last is the operation of the call before.
type ledger struct {
	broken string
	last   string
	calls  map[string]int
	items  map[string]item
	first  *item
	log    []int
	lists  map[string][]int
	ticks  int
	totals map[string]int
	states map[string]int
	set    []int
	ever   []int
	open   int
	dep    *conform.Dependency
}

// ledgers returns a factory of ledgers with the defect broken. Each ledger
// commits through a dependency of the injector of its handle.
func ledgers(broken string) func(*conform.Handle[*ledger]) *ledger {
	return func(h *conform.Handle[*ledger]) *ledger {
		return &ledger{
			broken: broken, calls: map[string]int{}, items: map[string]item{}, lists: map[string][]int{},
			totals: map[string]int{}, states: map[string]int{}, dep: h.Injector().Dependency(),
		}
	}
}

// keyed and valued list the operations of a ledger that take a key and a
// value.
var (
	keyed = map[string]bool{
		"Put": true, "Get": true, "Add": true, "Total": true, "Advance": true, "State": true, "Push": true,
		"Lists": true, "Rewind": true, "Save": true, "Set": true, "Grow": true, "Stage": true, "Transfer": true,
		"Commit": true, "Fetch": true,
	}
	valued = map[string]bool{
		"Put": true, "Append": true, "Insert": true, "Bit": true, "Push": true, "Has": true, "Broadcast": true,
		"Save": true, "Move": true, "Where": true, "Set": true, "Poke": true, "Peek": true, "Transfer": true,
		"Commit": true, "Erase": true,
	}
)

// operations are the operations of a ledger by name. Each takes the key and
// the value of its call, and whether the call is the fourth call of the
// operation or a later one.
var operations = map[string]func(l *ledger, key string, value int, late bool) (any, error){
	"Tick": (*ledger).tick, "Put": (*ledger).put, "Get": (*ledger).get, "Append": (*ledger).appendEntry,
	"List": (*ledger).list, "Take": (*ledger).take, "Replay": (*ledger).replay, "Recent": (*ledger).recent,
	"Pair": (*ledger).pair, "Push": (*ledger).push, "Lists": (*ledger).listOf, "Add": (*ledger).add,
	"Total": (*ledger).total, "Len": (*ledger).length, "Advance": (*ledger).advance, "State": (*ledger).state,
	"Insert": (*ledger).insert, "Bit": (*ledger).bit, "Members": (*ledger).members, "Has": (*ledger).has,
	"Broadcast": (*ledger).broadcast, "Rewind": (*ledger).rewind, "Save": (*ledger).save, "Move": (*ledger).move,
	"Where": (*ledger).where, "Set": (*ledger).setValue, "Grow": (*ledger).grow, "Stage": (*ledger).stage,
	"Poke": (*ledger).poke, "Peek": (*ledger).peek, "Transfer": (*ledger).transfer, "Sum": (*ledger).sum,
	"Commit": (*ledger).commit, "Fetch": (*ledger).fetch, "Erase": (*ledger).erase,
}

// do calls the operation op of the ledger with key and value.
func (l *ledger) do(op, key string, value int) (any, error) {
	l.calls[op]++
	v, err := operations[op](l, key, value, l.calls[op] > 3)
	l.last = op
	return v, err
}

// tick raises the clock of the ledger and returns it. A regress defect
// returns 1.
func (l *ledger) tick(_ string, _ int, late bool) (any, error) {
	l.ticks++
	if late && l.broken == "regress" {
		return 1, nil
	}
	return l.ticks, nil
}

// put stores the value under the key with the next version of the key, and
// returns the version. It refuses the value 0 with errOther. A drop defect
// stores nothing, and a stamp defect stores the version 0.
func (l *ledger) put(key string, value int, late bool) (any, error) {
	if value == 0 {
		return 0, errOther
	}
	if late && l.broken == "drop" {
		return 0, nil
	}
	version := 0
	if stored, found := l.items[key]; found {
		version = stored.Version + 1
	}
	if late && l.broken == "stamp" {
		version = 0
	}
	l.items[key] = item{Value: value, Version: version}
	return version, nil
}

// save stores the value under the key as put does, and returns the stored
// item. An unstamped defect returns the version 0 and stores the version that
// put stores.
func (l *ledger) save(key string, value int, late bool) (any, error) {
	version, err := l.put(key, value, late)
	if late && l.broken == "unstamped" {
		version = 0
	}
	return item{Value: value, Version: version.(int)}, err
}

// setValue stores the value under the key as put does, and returns no value.
func (l *ledger) setValue(key string, value int, late bool) (any, error) {
	_, err := l.put(key, value, late)
	return nil, err
}

// get returns the item under the key, and errMiss for a key without one. A
// lost defect fails each call right after a call of Put with errOther, and a
// stale defect returns the first item that the ledger returned.
func (l *ledger) get(key string, _ int, late bool) (any, error) {
	switch {
	case l.broken == "lost" && l.last == "Put":
		return item{}, errOther
	case late && l.broken == "stale" && l.first != nil:
		return *l.first, nil
	}
	got, found := l.items[key]
	if !found {
		return item{}, errMiss
	}
	if l.first == nil {
		l.first = &got
	}
	return got, nil
}

// appendEntry appends the value to the log and returns its position from 1.
// It refuses the value 0 with errOther. A descend defect returns the position
// 0, and a double defect appends the value twice.
func (l *ledger) appendEntry(_ string, value int, late bool) (any, error) {
	if value == 0 {
		return 0, errOther
	}
	l.log = append(l.log, value)
	if late && l.broken == "double" {
		l.log = append(l.log, value)
	}
	if late && l.broken == "descend" {
		return 0, nil
	}
	return len(l.log), nil
}

// list returns the entries of the log. A reverse defect reverses them, and a
// shrink defect drops the last entry.
func (l *ledger) list(_ string, _ int, late bool) (any, error) {
	entries := slices.Clone(l.log)
	switch {
	case !late || len(entries) == 0:
	case l.broken == "reverse":
		slices.Reverse(entries)
	case l.broken == "shrink":
		entries = entries[:len(entries)-1]
	}
	return entries, nil
}

// take returns the entries of the log and empties the log. A shrink defect
// drops the last entry.
func (l *ledger) take(_ string, _ int, late bool) (any, error) {
	entries := l.log
	l.log = nil
	if late && l.broken == "shrink" && len(entries) > 0 {
		entries = entries[:len(entries)-1]
	}
	return entries, nil
}

// stampsOf returns the entries of log with their stamps.
func stampsOf(log []int) []stamped {
	entries := make([]stamped, len(log))
	for i, entry := range log {
		entries[i] = stamped{Stamp: i + 1, Entry: entry}
	}
	return entries
}

// replay returns the entries of the log with their stamps, and errMiss for an
// empty log. An unordered defect reverses them.
func (l *ledger) replay(_ string, _ int, late bool) (any, error) {
	if len(l.log) == 0 {
		return []stamped(nil), errMiss
	}
	entries := stampsOf(l.log)
	if late && l.broken == "unordered" {
		slices.Reverse(entries)
	}
	return entries, nil
}

// recent returns the entries of the log with their stamps, the newest entry
// first.
func (l *ledger) recent(string, int, bool) (any, error) {
	entries := stampsOf(l.log)
	slices.Reverse(entries)
	return entries, nil
}

// pair returns the last two entries of the log with their stamps. An
// unordered defect reverses them.
func (l *ledger) pair(_ string, _ int, late bool) (any, error) {
	entries := stampsOf(l.log)
	entries = entries[max(0, len(entries)-2):]
	if late && l.broken == "unordered" {
		slices.Reverse(entries)
	}
	return entries, nil
}

// push appends the value to the list of the key and to the log, and returns
// the length of the list.
func (l *ledger) push(key string, value int, _ bool) (any, error) {
	l.lists[key] = append(l.lists[key], value)
	l.log = append(l.log, value)
	return len(l.lists[key]), nil
}

// broadcast appends the value to the lists of the keys a, b and c, and returns
// the number of the lists.
func (l *ledger) broadcast(_ string, value int, _ bool) (any, error) {
	for _, key := range []string{"a", "b", "c"} {
		l.lists[key] = append(l.lists[key], value)
	}
	return 3, nil
}

// listOf returns the list of the key. A shrink defect drops its last value.
func (l *ledger) listOf(key string, _ int, late bool) (any, error) {
	values := slices.Clone(l.lists[key])
	if late && l.broken == "shrink" && len(values) > 0 {
		values = values[:len(values)-1]
	}
	return values, nil
}

// add raises the total of the key by 1 and returns the total. It refuses the
// key c with errOther. A stuck defect leaves the total as it is.
func (l *ledger) add(key string, _ int, late bool) (any, error) {
	if key == "c" {
		return 0, errOther
	}
	if !late || l.broken != "stuck" {
		l.totals[key]++
	}
	return l.totals[key], nil
}

// total returns the total of the key.
func (l *ledger) total(key string, _ int, _ bool) (any, error) {
	return l.totals[key], nil
}

// length returns the number of keys with an item. An overflow defect returns
// 4, one above the three keys of a pool, and an uncountable defect fails with
// errOther.
func (l *ledger) length(_ string, _ int, late bool) (any, error) {
	switch {
	case late && l.broken == "overflow":
		return 4, nil
	case late && l.broken == "uncountable":
		return 0, errOther
	}
	return len(l.items), nil
}

// advance moves the state of the key from 0 to 1, from 1 to 2 and from 2 to
// 0, and returns the new state. A skip defect moves the state two steps.
func (l *ledger) advance(key string, _ int, late bool) (any, error) {
	step := 1
	if late && l.broken == "skip" {
		step = 2
	}
	l.states[key] = (l.states[key] + step) % 3
	return l.states[key], nil
}

// rewind moves the state of the key from 2 to 1, from 1 to 0 and from 0 to
// 2, against the graph of advance, and returns the new state.
func (l *ledger) rewind(key string, _ int, _ bool) (any, error) {
	l.states[key] = (l.states[key] + 2) % 3
	return l.states[key], nil
}

// poke stores the value under the key that the value names, as put does.
func (l *ledger) poke(_ string, value int, late bool) (any, error) {
	return l.put(strconv.Itoa(value), value, late)
}

// peek returns the item under the key that the value names, as get does.
func (l *ledger) peek(_ string, value int, late bool) (any, error) {
	return l.get(strconv.Itoa(value), 0, late)
}

// grow moves the stage of the key from 0 to 1 and from 1 to 2, and refuses
// the stage 2 with errOther. A skip defect moves the stage two steps, to 2 at
// most.
func (l *ledger) grow(key string, _ int, late bool) (any, error) {
	if l.states[key] >= 2 {
		return 0, errOther
	}
	step := 1
	if late && l.broken == "skip" {
		step = 2
	}
	l.states[key] = min(l.states[key]+step, 2)
	return l.states[key], nil
}

// stage returns the stage of the key.
func (l *ledger) stage(key string, _ int, _ bool) (any, error) {
	return l.states[key], nil
}

// move advances the state of the instance that the value names, as advance
// advances the state of a key.
func (l *ledger) move(_ string, value int, late bool) (any, error) {
	return l.advance(strconv.Itoa(value), 0, late)
}

// where returns the state of the instance that the value names, as state
// returns the state of a key.
func (l *ledger) where(_ string, value int, _ bool) (any, error) {
	return l.state(strconv.Itoa(value), 0, false)
}

// state returns the state of the key, and errMiss for the state 0.
func (l *ledger) state(key string, _ int, _ bool) (any, error) {
	if l.states[key] == 0 {
		return 0, errMiss
	}
	return l.states[key], nil
}

// insert adds the value to the set and to the values that the set ever had,
// and returns the size of the set.
func (l *ledger) insert(_ string, value int, _ bool) (any, error) {
	l.set = slices.Compact(slices.Sorted(slices.Values(append(l.set, value))))
	l.ever = slices.Compact(slices.Sorted(slices.Values(append(l.ever, value))))
	return len(l.set), nil
}

// bit adds the parity of the value to the set, and returns the size of the
// set.
func (l *ledger) bit(key string, value int, late bool) (any, error) {
	return l.insert(key, value%2, late)
}

// members returns the values of the set in ascending order, and errMiss for
// an empty set. A repeat defect replaces the last value with the first.
func (l *ledger) members(_ string, _ int, late bool) (any, error) {
	if len(l.set) == 0 {
		return []int(nil), errMiss
	}
	values := slices.Clone(l.set)
	if late && l.broken == "repeat" && len(values) > 1 {
		values[len(values)-1] = values[0]
	}
	return values, nil
}

// has reports whether the set has the value.
func (l *ledger) has(_ string, value int, _ bool) (any, error) {
	return slices.Contains(l.set, value), nil
}

// successors maps each key of a pool to the key that a transfer moves units
// to.
var successors = map[string]string{"a": "b", "b": "c", "c": "a"}

// transfer moves value units from the total of the key to the total of its
// successor, and returns no value. A leak defect moves one unit more into the
// successor than it takes from the key.
func (l *ledger) transfer(key string, value int, late bool) (any, error) {
	l.totals[key] -= value
	l.totals[successors[key]] += value
	if late && l.broken == "leak" {
		l.totals[successors[key]]++
	}
	return nil, nil
}

// sum returns the sum of the totals of every key. An unsummable defect fails
// with errOther.
func (l *ledger) sum(_ string, _ int, late bool) (any, error) {
	if late && l.broken == "unsummable" {
		return 0, errOther
	}
	total := 0
	for _, t := range l.totals {
		total += t
	}
	return total, nil
}

// commit stores the value under the key once the dependency of the ledger
// confirms the write, and returns no value. A presume defect stores the value
// before the dependency confirms it, an unguarded defect stores it without a
// call of the dependency, a stubborn defect stores it and fails without a call
// of the dependency, and a swallow defect returns no error for a write whose
// dependency failed.
func (l *ledger) commit(key string, value int, late bool) (any, error) {
	if late && l.broken == "presume" {
		l.items[key] = item{Value: value}
	}
	switch l.broken {
	case "unguarded":
		l.items[key] = item{Value: value}
		return nil, nil
	case "stubborn":
		l.items[key] = item{Value: value}
		return nil, errOther
	}
	if err := l.dep.Call("Put"); err != nil {
		if l.broken == "swallow" {
			return nil, nil
		}
		return nil, err
	}
	l.items[key] = item{Value: value}
	return nil, nil
}

// fetch returns the value under the key once the dependency of the ledger
// answers, and errMiss for a key without one.
func (l *ledger) fetch(key string, _ int, _ bool) (any, error) {
	if err := l.dep.Call("Get"); err != nil {
		return 0, err
	}
	got, found := l.items[key]
	if !found {
		return 0, errMiss
	}
	return got.Value, nil
}

// erase removes the value from the set, and returns the size of the set. It
// fails with errMiss for a value that the set lacks.
func (l *ledger) erase(_ string, value int, _ bool) (any, error) {
	if !slices.Contains(l.set, value) {
		return 0, errMiss
	}
	l.set = slices.DeleteFunc(l.set, func(v int) bool { return v == value })
	return len(l.set), nil
}

// iterate starts a pull iteration of the set, and counts it as open until it
// stops. Each value that next yields is the lowest value that the set has and
// the iteration has not yielded. Once next yields no value, the iteration has
// ended, as an iteration of iter.Pull ends. A copy defect iterates the set as
// it was at the start, and a ghost defect iterates every value that the set
// ever had. An unstreamable defect fails with errOther.
func (l *ledger) iterate() (next func() (int, bool), stop func(), err error) {
	source := func() []int { return l.set }
	switch l.broken {
	case "unstreamable":
		return nil, nil, errOther
	case "copy":
		snapshot := slices.Clone(l.set)
		source = func() []int { return snapshot }
	case "ghost":
		source = func() []int { return l.ever }
	}
	l.open++
	seen := map[int]bool{}
	ended := false
	next = func() (int, bool) {
		if ended {
			return 0, false
		}
		for _, v := range source() {
			if !seen[v] {
				seen[v] = true
				return v, true
			}
		}
		ended = true
		return 0, false
	}
	return next, func() { l.open-- }, nil
}

// sums returns the call of the sum of the totals of the ledger of h.
func sums(h *conform.Handle[*ledger]) probe.Call[int] {
	return func(context.Context) (int, error) {
		v, err := h.Subject().do("Sum", "", 0)
		return v.(int), err
	}
}

// iterates returns the pull iteration of the set of the ledger of h.
func iterates(h *conform.Handle[*ledger]) probe.Pull[int] {
	return func(context.Context) (func() (int, bool), func(), error) { return h.Subject().iterate() }
}

// merge adds the values of the set of other to the set. A detached defect
// adds nothing.
func (l *ledger) merge(other *ledger) {
	if l.broken != "detached" {
		l.set = slices.Compact(slices.Sorted(slices.Values(append(slices.Clone(l.set), other.set...))))
	}
}

// input is the input of a call of a ledger.
type input struct {
	key   string
	value int
}

// call returns the arguments and the keys of a call of the operation op with
// the input.
func (in input) call(op string) (args, keys []any) {
	if keyed[op] {
		args, keys = []any{in.key}, []any{in.key}
	}
	if valued[op] {
		args = append(args, in.value)
	}
	return args, keys
}

// decoded returns the input of a call of the operation op with args.
func decoded(op string, args []any) input {
	var in input
	if keyed[op] {
		in.key, args = args[0].(string), args[1:]
	}
	if valued[op] {
		in.value = args[0].(int)
	}
	return in
}

// actionsOf returns the actions of the operations ops of a ledger. The input
// of an action is the list of the arguments of its call. The action of a
// keyed operation takes its key from a pool of one to three keys of the case,
// and the action of an operation without inputs requests none.
func actionsOf(ops ...string) probe.Actions[*ledger] {
	return func(c *prop.Case, subject func() *ledger) []stateful.Action[struct{}] {
		pool := c.Draw(prop.List(prop.SampledFrom("a", "b", "c"), prop.Unique(), prop.MinSize(1), prop.MaxSize(3)),
			"keys")
		actions := make([]stateful.Action[struct{}], len(ops))
		for i, op := range ops {
			actions[i] = stateful.Action[struct{}]{
				Name: op,
				Run: func(c *prop.Case, client int, x any) {
					var args []any
					if keyed[op] || valued[op] {
						args = x.([]any)
					}
					in := decoded(op, args)
					_, keys := in.call(op)
					call := c.History().Invoke(client, op, args, keys...)
					v, err := subject().do(op, in.key, in.value)
					if v == nil {
						conform.Complete(c.Context(), call, err)
						return
					}
					conform.Complete(c.Context(), call, err, v)
				},
			}
			if keyed[op] || valued[op] {
				actions[i].Input = func(c *prop.Case, _ struct{}) any {
					var in input
					if keyed[op] {
						in.key = c.Draw(prop.SampledFrom(pool...), op+".key")
					}
					if valued[op] {
						in.value = c.Draw(prop.Integer(0, 9), op+".value")
					}
					args, _ := in.call(op)
					return args
				}
			}
		}
		return actions
	}
}

// applying returns the Apply of the operation op of the ledger of h.
func applying(h *conform.Handle[*ledger], op string) probe.Apply {
	return func(_ context.Context, args []any) conform.Output {
		in := decoded(op, args)
		v, err := h.Subject().do(op, in.key, in.value)
		return conform.OutputOf(err, v)
	}
}

// stored returns the Apply of the value under the key of the ledger of h.
func stored(h *conform.Handle[*ledger]) probe.Apply {
	return func(_ context.Context, args []any) conform.Output {
		v, err := h.Subject().do("Get", args[0].(string), 0)
		return conform.OutputOf(err, v.(item).Value)
	}
}

// backing returns the Apply of the item under the key of the ledger of h,
// which no defect of the ledger changes.
func backing(h *conform.Handle[*ledger]) probe.Apply {
	return func(_ context.Context, args []any) conform.Output {
		got, found := h.Subject().items[args[0].(string)]
		if !found {
			return conform.OutputOf(errMiss)
		}
		return conform.OutputOf(nil, got)
	}
}

// lengths returns the call of the number of keys of the ledger of h.
func lengths(h *conform.Handle[*ledger]) probe.Call[int] {
	return func(context.Context) (int, error) {
		v, err := h.Subject().do("Len", "", 0)
		return v.(int), err
	}
}

// referenced returns the function that applies an operation to the ledger r
// by its name.
func referenced(r *ledger) func(ctx context.Context, op string, args []any) conform.Output {
	return func(_ context.Context, op string, args []any) conform.Output {
		if op == "Merge" {
			r.merge(args[0].(*ledger))
			return conform.OutputOf(nil)
		}
		in := decoded(op, args)
		v, err := r.do(op, in.key, in.value)
		return conform.OutputOf(err, v)
	}
}

// versionOf returns the version of an item.
func versionOf(it item) int { return it.Version }

// entryOf returns the entry that a call of Append writes.
func entryOf(args []any) int { return args[0].(int) }

// valueOf returns the value that a call of Put or Push writes.
func valueOf(args []any) int { return args[1].(int) }

// stampOf returns the stamp of an entry.
func stampOf(e stamped) int { return e.Stamp }

// cycle is the graph of the states of a ledger.
var cycle = map[int][]int{0: {1}, 1: {2}, 2: {0}}

// property is the property of a check over ledgers.
type property = func(h *conform.Handle[*ledger], c *prop.Case)

// unengaged applies a property check of the probe p to a ledger with the
// defect broken, and fails t unless the check fails once for the vacuous
// condition of p.
func unengaged(t *testing.T, p spec.ProbeID, broken string, check property) {
	t.Helper()
	r := assert.NewRecorder()
	conform.Check[*ledger]{
		ID: "Ledger.Call/claim", Statement: "the probe holds", Probe: p, Property: check,
	}.Run(r, ledgers(broken), conform.Cases[*ledger](10), conform.Reference(ledgers("")))
	entry, _ := spec.ProbeOf(p)
	condition := strings.ToLower(entry.Vacuous[:1]) + strings.TrimSuffix(entry.Vacuous[1:], ".")
	assert.Equal(t, r.Messages(), []string{"Ledger.Call/claim never engaged: " + condition + ". Pass a subject " +
		"that engages the check, or excuse the check with the directive excuse or conform.Excuse and a reason."},
		"the check fails for the vacuous condition of its probe")
}

func TestSequence(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name         string
		giveProbe    spec.ProbeID
		giveBreak    string
		giveProperty property
		want         spec.ReasonID
	}{
		{
			name: "ResultsNeverFall", giveProbe: spec.ResultsNeverFall, giveBreak: "regress", want: spec.Regressed,
			giveProperty: func(h *conform.Handle[*ledger], c *prop.Case) {
				probe.ResultsNeverFall[*ledger, int](h, c, actionsOf("Tick"), "Tick")
			},
		},
		{
			name: "ReadVersionsNeverFall", giveProbe: spec.ReadVersionsNeverFall, giveBreak: "stale",
			want: spec.Regressed,
			giveProperty: func(h *conform.Handle[*ledger], c *prop.Case) {
				probe.ReadVersionsNeverFall(h, c, actionsOf("Put", "Get"), "Get", versionOf)
			},
		},
		{
			name: "DrainsAgreeBetweenWrites", giveProbe: spec.DrainsAgreeBetweenWrites, giveBreak: "reverse",
			want: spec.ReorderedDrain,
			giveProperty: func(h *conform.Handle[*ledger], c *prop.Case) {
				probe.DrainsAgreeBetweenWrites(h, c, actionsOf("Append", "List"), "List")
			},
		},
		{
			name: "WrittenVersionsRise", giveProbe: spec.WrittenVersionsRise, giveBreak: "stamp",
			want: spec.UnobservedWrite,
			giveProperty: func(h *conform.Handle[*ledger], c *prop.Case) {
				probe.WrittenVersionsRise(h, c, actionsOf("Put", "Get"), "Put", applying(h, "Get"), versionOf)
			},
		},
		{
			name:      "WrittenVersionsRise reads back the version of a write without a value",
			giveProbe: spec.WrittenVersionsRise, giveBreak: "stamp", want: spec.UnobservedWrite,
			giveProperty: func(h *conform.Handle[*ledger], c *prop.Case) {
				probe.WrittenVersionsRise(h, c, actionsOf("Set"), "Set", applying(h, "Get"), versionOf)
			},
		},
		{
			name:      "WrittenVersionsRise reads back the version after a write that returns a value",
			giveProbe: spec.WrittenVersionsRise, giveBreak: "stamp", want: spec.UnobservedWrite,
			giveProperty: func(h *conform.Handle[*ledger], c *prop.Case) {
				probe.WrittenVersionsRise(h, c, actionsOf("Save"), "Save", applying(h, "Get"), versionOf)
			},
		},
		{
			name:      "ReadsSeeTheLastWrite reads back the version of a write without a value",
			giveProbe: spec.ReadsSeeTheLastWrite, giveBreak: "stale", want: spec.UnobservedWrite,
			giveProperty: func(h *conform.Handle[*ledger], c *prop.Case) {
				probe.ReadsSeeTheLastWrite(h, c, actionsOf("Set", "Get"), "Get", "Set", applying(h, "Get"), versionOf)
			},
		},
		{
			name: "ReadsSeeTheLastWrite judges the version that a write returns", giveProbe: spec.ReadsSeeTheLastWrite,
			giveBreak: "stale", want: spec.UnobservedWrite,
			giveProperty: func(h *conform.Handle[*ledger], c *prop.Case) {
				probe.ReadsSeeTheLastWrite(h, c, actionsOf("Save", "Get"), "Get", "Save", applying(h, "Get"), versionOf)
			},
		},
		{
			name: "WritesRiseAboveReads judges the version that a write returns", giveProbe: spec.WritesRiseAboveReads,
			giveBreak: "unstamped", want: spec.UnorderedWrite,
			giveProperty: func(h *conform.Handle[*ledger], c *prop.Case) {
				probe.WritesRiseAboveReads(h, c, actionsOf("Save", "Get"), "Save", "Get", applying(h, "Get"), versionOf)
			},
		},
		{
			name:      "StatesMoveAlongTheGraph judges the first call of a key",
			giveProbe: spec.StatesMoveAlongTheGraph, giveBreak: "skip", want: spec.ForbiddenTransition,
			giveProperty: func(h *conform.Handle[*ledger], c *prop.Case) {
				probe.StatesMoveAlongTheGraph(h, c, actionsOf("Grow", "Stage"), "Grow", applying(h, "Stage"),
					map[int][]int{0: {1}, 1: {2}})
			},
		},
		{
			name:      "StatesMoveAlongTheGraph judges a call without keys by its arguments",
			giveProbe: spec.StatesMoveAlongTheGraph, giveBreak: "skip", want: spec.ForbiddenTransition,
			giveProperty: func(h *conform.Handle[*ledger], c *prop.Case) {
				probe.StatesMoveAlongTheGraph(h, c, actionsOf("Move", "Where"), "Move", applying(h, "Where"), cycle)
			},
		},
		{
			name: "WrittenVersionsRise fails a read that fails after a write", giveProbe: spec.WrittenVersionsRise,
			giveBreak: "lost", want: spec.UnobservedWrite,
			giveProperty: func(h *conform.Handle[*ledger], c *prop.Case) {
				probe.WrittenVersionsRise(h, c, actionsOf("Put"), "Put", applying(h, "Get"), versionOf)
			},
		},
		{
			name: "ReadsSeeTheLastWrite", giveProbe: spec.ReadsSeeTheLastWrite, giveBreak: "stale",
			want: spec.UnobservedWrite,
			giveProperty: func(h *conform.Handle[*ledger], c *prop.Case) {
				probe.ReadsSeeTheLastWrite(h, c, actionsOf("Put", "Get", "Add"), "Get", "Put", applying(h, "Get"),
					versionOf)
			},
		},
		{
			name: "ReadsSeeTheLastWrite fails a readback that fails", giveProbe: spec.ReadsSeeTheLastWrite,
			giveBreak: "lost", want: spec.UnobservedWrite,
			giveProperty: func(h *conform.Handle[*ledger], c *prop.Case) {
				probe.ReadsSeeTheLastWrite(h, c, actionsOf("Put", "Get"), "Get", "Put", applying(h, "Get"), versionOf)
			},
		},
		{
			name: "WritesRiseAboveReads", giveProbe: spec.WritesRiseAboveReads, giveBreak: "stamp",
			want: spec.UnorderedWrite,
			giveProperty: func(h *conform.Handle[*ledger], c *prop.Case) {
				probe.WritesRiseAboveReads(h, c, actionsOf("Put", "Get", "Add"), "Put", "Get", applying(h, "Get"),
					versionOf)
			},
		},
		{
			name: "WritesRiseAboveReads fails a readback that fails", giveProbe: spec.WritesRiseAboveReads,
			giveBreak: "lost", want: spec.UnorderedWrite,
			giveProperty: func(h *conform.Handle[*ledger], c *prop.Case) {
				probe.WritesRiseAboveReads(h, c, actionsOf("Put", "Get"), "Put", "Get", applying(h, "Get"), versionOf)
			},
		},
		{
			name: "PositionsRise", giveProbe: spec.PositionsRise, giveBreak: "descend", want: spec.NotAscending,
			giveProperty: func(h *conform.Handle[*ledger], c *prop.Case) {
				probe.PositionsRise[*ledger, int](h, c, actionsOf("Append"), "Append")
			},
		},
		{
			name: "ReplaysFollowTheStamps", giveProbe: spec.ReplaysFollowTheStamps, giveBreak: "unordered",
			want: spec.UnorderedHistory,
			giveProperty: func(h *conform.Handle[*ledger], c *prop.Case) {
				probe.ReplaysFollowTheStamps(h, c, actionsOf("Append", "Replay", "Recent"), "Replay", stampOf)
			},
		},
		{
			name: "ReplaysFollowTheStamps judges a replay of two entries", giveProbe: spec.ReplaysFollowTheStamps,
			giveBreak: "unordered", want: spec.UnorderedHistory,
			giveProperty: func(h *conform.Handle[*ledger], c *prop.Case) {
				probe.ReplaysFollowTheStamps(h, c, actionsOf("Append", "Pair"), "Pair", stampOf)
			},
		},
		{
			name: "DrainsWithoutRepeats", giveProbe: spec.DrainsWithoutRepeats, giveBreak: "repeat",
			want: spec.RepeatedElement,
			giveProperty: func(h *conform.Handle[*ledger], c *prop.Case) {
				probe.DrainsWithoutRepeats[*ledger, int](h, c, actionsOf("Insert", "Members", "Append", "List"),
					"Members")
			},
		},
		{
			name: "DrainsWithoutRepeats judges a drain of two elements", giveProbe: spec.DrainsWithoutRepeats,
			giveBreak: "repeat", want: spec.RepeatedElement,
			giveProperty: func(h *conform.Handle[*ledger], c *prop.Case) {
				probe.DrainsWithoutRepeats[*ledger, int](h, c, actionsOf("Bit", "Members"), "Members")
			},
		},
		{
			name: "DrainsContainTheWrites", giveProbe: spec.DrainsContainTheWrites, giveBreak: "shrink",
			want: spec.ShrunkDrain,
			giveProperty: func(h *conform.Handle[*ledger], c *prop.Case) {
				probe.DrainsContainTheWrites(h, c, actionsOf("Append", "List", "Tick"), "List", "Append", entryOf)
			},
		},
		{
			name:      "DrainsContainTheWrites gains over a drain that takes its elements",
			giveProbe: spec.DrainsContainTheWrites, giveBreak: "shrink", want: spec.ShrunkDrain,
			giveProperty: func(h *conform.Handle[*ledger], c *prop.Case) {
				probe.DrainsContainTheWrites(h, c, actionsOf("Append", "Take"), "Take", "Append", entryOf)
			},
		},
		{
			name:      "DrainsContainTheWrites meets the writes of the keys of a drain",
			giveProbe: spec.DrainsContainTheWrites, giveBreak: "shrink", want: spec.ShrunkDrain,
			giveProperty: func(h *conform.Handle[*ledger], c *prop.Case) {
				probe.DrainsContainTheWrites(h, c, actionsOf("Push", "Lists"), "Lists", "Push", valueOf)
			},
		},
		{
			name:      "DrainsContainTheWrites meets a write without keys in a drain of a key",
			giveProbe: spec.DrainsContainTheWrites, giveBreak: "shrink", want: spec.ShrunkDrain,
			giveProperty: func(h *conform.Handle[*ledger], c *prop.Case) {
				probe.DrainsContainTheWrites(h, c, actionsOf("Broadcast", "Lists"), "Lists", "Broadcast", entryOf)
			},
		},
		{
			name:      "DrainsContainTheWrites meets the writes of every key in a drain without keys",
			giveProbe: spec.DrainsContainTheWrites, giveBreak: "shrink", want: spec.ShrunkDrain,
			giveProperty: func(h *conform.Handle[*ledger], c *prop.Case) {
				probe.DrainsContainTheWrites(h, c, actionsOf("Push", "List"), "List", "Push", valueOf)
			},
		},
		{
			name: "DrainsGainTheWrites", giveProbe: spec.DrainsGainTheWrites, giveBreak: "double",
			want: spec.MiscountedDrain,
			giveProperty: func(h *conform.Handle[*ledger], c *prop.Case) {
				probe.DrainsGainTheWrites(h, c, actionsOf("Append", "List", "Tick"), "List", "Append", entryOf)
			},
		},
		{
			name: "EachCallMovesTheObservation", giveProbe: spec.EachCallMovesTheObservation, giveBreak: "stuck",
			want: spec.Uncompounded,
			giveProperty: func(h *conform.Handle[*ledger], c *prop.Case) {
				probe.EachCallMovesTheObservation(h, c, actionsOf("Add", "Total"), "Add", applying(h, "Total"))
			},
		},
		{
			name: "ReadsReturnTheLastWrite", giveProbe: spec.ReadsReturnTheLastWrite, giveBreak: "stale",
			want: spec.UnobservedWrite,
			giveProperty: func(h *conform.Handle[*ledger], c *prop.Case) {
				probe.ReadsReturnTheLastWrite(h, c, actionsOf("Put", "Get"), "Put", stored(h), valueOf)
			},
		},
		{
			name: "ReadsReturnTheLastWrite fails a readback that fails", giveProbe: spec.ReadsReturnTheLastWrite,
			giveBreak: "lost", want: spec.UnobservedWrite,
			giveProperty: func(h *conform.Handle[*ledger], c *prop.Case) {
				probe.ReadsReturnTheLastWrite(h, c, actionsOf("Put"), "Put", stored(h), valueOf)
			},
		},
		{
			name: "ReadsAgreeWithTheBacking", giveProbe: spec.ReadsAgreeWithTheBacking, giveBreak: "stale",
			want: spec.CacheDisagrees,
			giveProperty: func(h *conform.Handle[*ledger], c *prop.Case) {
				probe.ReadsAgreeWithTheBacking(h, c, actionsOf("Put", "Get"), "Get", backing(h))
			},
		},
		{
			name: "SequencesAgreeWithTheReference", giveProbe: spec.SequencesAgreeWithTheReference,
			giveBreak: "stale", want: spec.DivergesFromTheReference,
			giveProperty: func(h *conform.Handle[*ledger], c *prop.Case) {
				probe.SequencesAgreeWithTheReference(h, c, actionsOf("Put", "Get"), referenced)
			},
		},
		{
			name: "UseIsWithinTheBounds", giveProbe: spec.UseIsWithinTheBounds, giveBreak: "overflow",
			want: spec.ExceededBound,
			giveProperty: func(h *conform.Handle[*ledger], c *prop.Case) {
				probe.UseIsWithinTheBounds(h, c, actionsOf("Put"), lengths(h), 0, 3)
			},
		},
		{
			name: "UseIsWithinTheBounds fails an observation that fails", giveProbe: spec.UseIsWithinTheBounds,
			giveBreak: "uncountable", want: spec.ExceededBound,
			giveProperty: func(h *conform.Handle[*ledger], c *prop.Case) {
				probe.UseIsWithinTheBounds(h, c, actionsOf("Put"), lengths(h), 0, 3)
			},
		},
		{
			name: "MergesCommute", giveProbe: spec.MergesCommute, giveBreak: "detached",
			want: spec.OrderDependentMerge,
			giveProperty: func(h *conform.Handle[*ledger], c *prop.Case) {
				probe.MergesCommute(h, c, actionsOf("Insert", "Members"), referenced, "Merge", "Insert", "Members")
			},
		},
		{
			name: "MergesCommute reads the merged replicas with one input", giveProbe: spec.MergesCommute,
			giveBreak: "detached", want: spec.OrderDependentMerge,
			giveProperty: func(h *conform.Handle[*ledger], c *prop.Case) {
				probe.MergesCommute(h, c, actionsOf("Insert", "Has"), referenced, "Merge", "Insert", "Has")
			},
		},
		{
			name: "StatesMoveAlongTheGraph", giveProbe: spec.StatesMoveAlongTheGraph, giveBreak: "skip",
			want: spec.ForbiddenTransition,
			giveProperty: func(h *conform.Handle[*ledger], c *prop.Case) {
				probe.StatesMoveAlongTheGraph(h, c, actionsOf("Advance", "State", "Rewind"), "Advance",
					applying(h, "State"), cycle)
			},
		},
		{
			name: "MovesKeepTheTotal", giveProbe: spec.MovesKeepTheTotal, giveBreak: "leak", want: spec.NotConserved,
			giveProperty: func(h *conform.Handle[*ledger], c *prop.Case) {
				probe.MovesKeepTheTotal(h, c, actionsOf("Transfer", "Sum"), "Transfer", sums(h))
			},
		},
		{
			name: "MovesKeepTheTotal fails an observation that fails", giveProbe: spec.MovesKeepTheTotal,
			giveBreak: "unsummable", want: spec.NotConserved,
			giveProperty: func(h *conform.Handle[*ledger], c *prop.Case) {
				probe.MovesKeepTheTotal(h, c, actionsOf("Transfer"), "Transfer", sums(h))
			},
		},
		{
			name: "FailedCallsLeaveNoTrace", giveProbe: spec.FailedCallsLeaveNoTrace, giveBreak: "presume",
			want: spec.PartialWrite,
			giveProperty: func(h *conform.Handle[*ledger], c *prop.Case) {
				probe.FailedCallsLeaveNoTrace(h, c, actionsOf("Fetch", "Commit"), "Commit", applying(h, "Fetch"))
			},
		},
		{
			name:      "FailedCallsLeaveNoTrace fails the first action of the list",
			giveProbe: spec.FailedCallsLeaveNoTrace, giveBreak: "presume", want: spec.PartialWrite,
			giveProperty: func(h *conform.Handle[*ledger], c *prop.Case) {
				probe.FailedCallsLeaveNoTrace(h, c, actionsOf("Commit", "Fetch"), "Commit", applying(h, "Fetch"))
			},
		},
		{
			name: "IterationsSeeTheMutations", giveProbe: spec.IterationsSeeTheMutations, giveBreak: "copy",
			want: spec.StaleIteration,
			giveProperty: func(h *conform.Handle[*ledger], c *prop.Case) {
				probe.IterationsSeeTheMutations(h, c, actionsOf("Insert", "Erase"), iterates(h), "Insert", "Erase",
					entryOf, entryOf)
			},
		},
		{
			name:      "IterationsSeeTheMutations fails a rest that yields an erased value",
			giveProbe: spec.IterationsSeeTheMutations, giveBreak: "ghost", want: spec.StaleIteration,
			giveProperty: func(h *conform.Handle[*ledger], c *prop.Case) {
				probe.IterationsSeeTheMutations(h, c, actionsOf("Insert", "Erase", "Has"), iterates(h), "Insert",
					"Erase", entryOf, entryOf)
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			conform.Check[*ledger]{
				ID: "Ledger.Call/claim", Statement: "the probe holds", Claim: "claim", Probe: tt.giveProbe,
				Reason: tt.want, Property: tt.giveProperty, Defect: ledgers(tt.giveBreak),
			}.Run(t, ledgers(""), conform.Reference(ledgers("")))
		})
	}

	t.Run("WrittenVersionsRise passes a write that returns another version than a read", func(t *testing.T) {
		t.Parallel()

		conform.Check[*ledger]{
			ID: "Ledger.Call/claim", Statement: "the probe holds", Probe: spec.WrittenVersionsRise,
			Property: func(h *conform.Handle[*ledger], c *prop.Case) {
				probe.WrittenVersionsRise(h, c, actionsOf("Save"), "Save", applying(h, "Get"), versionOf)
			},
		}.Run(t, ledgers("unstamped"))
	})

	t.Run("ReadVersionsNeverFall compares the reads without keys by their arguments", func(t *testing.T) {
		t.Parallel()

		conform.Check[*ledger]{
			ID: "Ledger.Call/claim", Statement: "the probe holds", Probe: spec.ReadVersionsNeverFall,
			Property: func(h *conform.Handle[*ledger], c *prop.Case) {
				probe.ReadVersionsNeverFall(h, c, actionsOf("Poke", "Peek"), "Peek", versionOf)
			},
		}.Run(t, ledgers(""))
	})

	t.Run("PositionsRise compares the calls with the same keys", func(t *testing.T) {
		t.Parallel()

		conform.Check[*ledger]{
			ID: "Ledger.Call/claim", Statement: "the probe holds", Probe: spec.PositionsRise,
			Property: func(h *conform.Handle[*ledger], c *prop.Case) {
				probe.PositionsRise[*ledger, int](h, c, actionsOf("Push"), "Push")
			},
		}.Run(t, ledgers(""))
	})

	t.Run("IterationsSeeTheMutations stops the iteration that it keeps", func(t *testing.T) {
		t.Parallel()

		conform.Check[*ledger]{
			ID: "Ledger.Call/claim", Statement: "the probe holds", Probe: spec.IterationsSeeTheMutations,
			Property: func(h *conform.Handle[*ledger], c *prop.Case) {
				probe.IterationsSeeTheMutations(h, c, actionsOf("Insert", "Erase"), iterates(h), "Insert", "Erase",
					entryOf, entryOf)
				assert.Equal(h, h.Subject().open, 0, "the probe stops its iteration")
			},
		}.Run(t, ledgers(""))
	})

	t.Run("ResultsNeverFall compares the calls with the same arguments", func(t *testing.T) {
		t.Parallel()

		conform.Check[*ledger]{
			ID: "Ledger.Call/claim", Statement: "the probe holds", Probe: spec.ResultsNeverFall,
			Property: func(h *conform.Handle[*ledger], c *prop.Case) {
				probe.ResultsNeverFall[*ledger, int](h, c, actionsOf("Add", "Total"), "Total")
			},
		}.Run(t, ledgers(""))
	})

	ends := []struct {
		name         string
		giveProbe    spec.ProbeID
		giveBreak    string
		giveProperty property
	}{
		{
			name: "ResultsNeverFall is vacuous on sequences without a read", giveProbe: spec.ResultsNeverFall,
			giveProperty: func(h *conform.Handle[*ledger], c *prop.Case) {
				probe.ResultsNeverFall[*ledger, int](h, c, actionsOf("Put"), "Tick")
			},
		},
		{
			name: "ReadVersionsNeverFall is vacuous on sequences without a read", giveProbe: spec.ReadVersionsNeverFall,
			giveProperty: func(h *conform.Handle[*ledger], c *prop.Case) {
				probe.ReadVersionsNeverFall(h, c, actionsOf("Put"), "Get", versionOf)
			},
		},
		{
			name:      "DrainsAgreeBetweenWrites is vacuous on sequences without a drain",
			giveProbe: spec.DrainsAgreeBetweenWrites,
			giveProperty: func(h *conform.Handle[*ledger], c *prop.Case) {
				probe.DrainsAgreeBetweenWrites(h, c, actionsOf("Append"), "List")
			},
		},
		{
			name: "WrittenVersionsRise is vacuous on sequences without a write", giveProbe: spec.WrittenVersionsRise,
			giveProperty: func(h *conform.Handle[*ledger], c *prop.Case) {
				probe.WrittenVersionsRise(h, c, actionsOf("Get"), "Put", applying(h, "Get"), versionOf)
			},
		},
		{
			name: "ReadsSeeTheLastWrite is vacuous on sequences without a write", giveProbe: spec.ReadsSeeTheLastWrite,
			giveProperty: func(h *conform.Handle[*ledger], c *prop.Case) {
				probe.ReadsSeeTheLastWrite(h, c, actionsOf("Get"), "Get", "Put", applying(h, "Get"), versionOf)
			},
		},
		{
			name: "WritesRiseAboveReads is vacuous on sequences without a read", giveProbe: spec.WritesRiseAboveReads,
			giveProperty: func(h *conform.Handle[*ledger], c *prop.Case) {
				probe.WritesRiseAboveReads(h, c, actionsOf("Put"), "Put", "Get", applying(h, "Get"), versionOf)
			},
		},
		{
			name: "PositionsRise is vacuous on sequences without an append", giveProbe: spec.PositionsRise,
			giveProperty: func(h *conform.Handle[*ledger], c *prop.Case) {
				probe.PositionsRise[*ledger, int](h, c, actionsOf("List"), "Append")
			},
		},
		{
			name:      "ReplaysFollowTheStamps is vacuous on replays of an empty log",
			giveProbe: spec.ReplaysFollowTheStamps,
			giveProperty: func(h *conform.Handle[*ledger], c *prop.Case) {
				probe.ReplaysFollowTheStamps(h, c, actionsOf("Recent"), "Recent", stampOf)
			},
		},
		{
			name: "DrainsWithoutRepeats is vacuous on drains of an empty list", giveProbe: spec.DrainsWithoutRepeats,
			giveProperty: func(h *conform.Handle[*ledger], c *prop.Case) {
				probe.DrainsWithoutRepeats[*ledger, int](h, c, actionsOf("Lists"), "Lists")
			},
		},
		{
			name:      "DrainsContainTheWrites is vacuous on sequences without a write",
			giveProbe: spec.DrainsContainTheWrites,
			giveProperty: func(h *conform.Handle[*ledger], c *prop.Case) {
				probe.DrainsContainTheWrites(h, c, actionsOf("List"), "List", "Append", entryOf)
			},
		},
		{
			name: "DrainsGainTheWrites is vacuous on sequences without a write", giveProbe: spec.DrainsGainTheWrites,
			giveProperty: func(h *conform.Handle[*ledger], c *prop.Case) {
				probe.DrainsGainTheWrites(h, c, actionsOf("List"), "List", "Append", entryOf)
			},
		},
		{
			name:      "EachCallMovesTheObservation is vacuous on sequences without a call",
			giveProbe: spec.EachCallMovesTheObservation,
			giveProperty: func(h *conform.Handle[*ledger], c *prop.Case) {
				probe.EachCallMovesTheObservation(h, c, actionsOf("Total"), "Add", applying(h, "Total"))
			},
		},
		{
			name:      "ReadsReturnTheLastWrite is vacuous on sequences without a write",
			giveProbe: spec.ReadsReturnTheLastWrite,
			giveProperty: func(h *conform.Handle[*ledger], c *prop.Case) {
				probe.ReadsReturnTheLastWrite(h, c, actionsOf("Get"), "Put", stored(h), valueOf)
			},
		},
		{
			name:      "ReadsAgreeWithTheBacking is vacuous on sequences without a read",
			giveProbe: spec.ReadsAgreeWithTheBacking,
			giveProperty: func(h *conform.Handle[*ledger], c *prop.Case) {
				probe.ReadsAgreeWithTheBacking(h, c, actionsOf("Put"), "Get", backing(h))
			},
		},
		{
			name:      "SequencesAgreeWithTheReference is vacuous on sequences without a call",
			giveProbe: spec.SequencesAgreeWithTheReference,
			giveProperty: func(h *conform.Handle[*ledger], c *prop.Case) {
				probe.SequencesAgreeWithTheReference(h, c, actionsOf(), referenced)
			},
		},
		{
			name: "UseIsWithinTheBounds is vacuous on sequences without a call", giveProbe: spec.UseIsWithinTheBounds,
			giveProperty: func(h *conform.Handle[*ledger], c *prop.Case) {
				probe.UseIsWithinTheBounds(h, c, actionsOf(), lengths(h), 0, 3)
			},
		},
		{
			name: "MergesCommute is vacuous on sequences without a write", giveProbe: spec.MergesCommute,
			giveProperty: func(h *conform.Handle[*ledger], c *prop.Case) {
				probe.MergesCommute(h, c, actionsOf("Members"), referenced, "Merge", "Insert", "Members")
			},
		},
		{
			name:      "StatesMoveAlongTheGraph is vacuous on sequences without a call",
			giveProbe: spec.StatesMoveAlongTheGraph,
			giveProperty: func(h *conform.Handle[*ledger], c *prop.Case) {
				probe.StatesMoveAlongTheGraph(h, c, actionsOf("State"), "Advance", applying(h, "State"), cycle)
			},
		},
		{
			name: "MovesKeepTheTotal is vacuous on sequences without a call", giveProbe: spec.MovesKeepTheTotal,
			giveProperty: func(h *conform.Handle[*ledger], c *prop.Case) {
				probe.MovesKeepTheTotal(h, c, actionsOf("Sum"), "Transfer", sums(h))
			},
		},
		{
			name:      "FailedCallsLeaveNoTrace is vacuous on sequences without a call",
			giveProbe: spec.FailedCallsLeaveNoTrace,
			giveProperty: func(h *conform.Handle[*ledger], c *prop.Case) {
				probe.FailedCallsLeaveNoTrace(h, c, actionsOf("Fetch"), "Commit", applying(h, "Fetch"))
			},
		},
		{
			name:      "FailedCallsLeaveNoTrace is vacuous on a write without a dependency",
			giveProbe: spec.FailedCallsLeaveNoTrace, giveBreak: "unguarded",
			giveProperty: func(h *conform.Handle[*ledger], c *prop.Case) {
				probe.FailedCallsLeaveNoTrace(h, c, actionsOf("Commit"), "Commit", applying(h, "Fetch"))
			},
		},
		{
			name:      "FailedCallsLeaveNoTrace is vacuous on a write that fails without a dependency",
			giveProbe: spec.FailedCallsLeaveNoTrace, giveBreak: "stubborn",
			giveProperty: func(h *conform.Handle[*ledger], c *prop.Case) {
				probe.FailedCallsLeaveNoTrace(h, c, actionsOf("Commit"), "Commit", applying(h, "Fetch"))
			},
		},
		{
			name:      "FailedCallsLeaveNoTrace is vacuous on a write that hides a failed dependency",
			giveProbe: spec.FailedCallsLeaveNoTrace, giveBreak: "swallow",
			giveProperty: func(h *conform.Handle[*ledger], c *prop.Case) {
				probe.FailedCallsLeaveNoTrace(h, c, actionsOf("Commit"), "Commit", applying(h, "Fetch"))
			},
		},
		{
			name:      "IterationsSeeTheMutations is vacuous on sequences without a mutation",
			giveProbe: spec.IterationsSeeTheMutations,
			giveProperty: func(h *conform.Handle[*ledger], c *prop.Case) {
				probe.IterationsSeeTheMutations(h, c, actionsOf("Insert", "Has"), iterates(h), "Bit", "Erase",
					entryOf, entryOf)
			},
		},
		{
			name:      "IterationsSeeTheMutations is vacuous on a stream that fails",
			giveProbe: spec.IterationsSeeTheMutations, giveBreak: "unstreamable",
			giveProperty: func(h *conform.Handle[*ledger], c *prop.Case) {
				probe.IterationsSeeTheMutations(h, c, actionsOf("Insert", "Erase"), iterates(h), "Insert", "Erase",
					entryOf, entryOf)
			},
		},
	}
	for _, tt := range ends {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			unengaged(t, tt.giveProbe, tt.giveBreak, tt.giveProperty)
		})
	}
}
