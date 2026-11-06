// Copyright Dokimasia B.V. 2026
// SPDX-License-Identifier: MIT

package host_test

import (
	"io"
	"os"
	"sync"
	"testing"
	"testing/synctest"
	"time"

	"go.dokimi.dev/assert"
	"go.dokimi.dev/assert/prop"
	"go.dokimi.dev/assert/stateful"

	"go.dokimi.dev/host"
	"go.dokimi.dev/medium"
)

// targets are the draws of a case whose every draw takes its target: a host
// without a rate error, links without delay, and a power loss that keeps no
// pending call.
const targets = "[]"

// slowLink are the draws of a blocking network of two hosts without drift. A
// network under millisecond gives their link a latency of one millisecond.
const slowLink = `[{"label": "drift.node-1", "value": {"type": "int", "value": 0}},` +
	`{"label": "drift.node-2", "value": {"type": "int", "value": 0}}]`

// narrowStream are the draws of a blocking network of two hosts without drift
// whose link takes the latency at the quantile 0 and the time of a byte at the
// quantile 1. Under tenMegabytes the link has no latency and takes 100 ns for
// each byte.
const narrowStream = `[{"label": "drift.node-1", "value": {"type": "int", "value": 0}},` +
	`{"label": "drift.node-2", "value": {"type": "int", "value": 0}},` +
	`{"label": "latency.node-1.node-2", "value": {"type": "float", "value": 0.0}},` +
	`{"label": "throughput.node-1.node-2", "value": {"type": "float", "value": 1.0}}]`

// millisecond is the option of a blocking network whose links have a latency
// of one millisecond. tenMegabytes is the option whose links have a lowest
// throughput of ten megabytes per second, which is 100 ns for each byte.
var (
	millisecond  = host.Latency(time.Millisecond, time.Millisecond)
	tenMegabytes = host.Throughput(10_000_000)
)

// inCase runs body in each case of a run of the property engine without a
// store of the failing cases. The first case of the run decodes the entries
// draws, and first is true for it alone. The later cases try the boundaries
// of each draw, so a spec whose result depends on a draw reads the result of
// the first case.
func inCase(t *testing.T, draws string, body func(c *prop.Case, first bool)) {
	t.Helper()

	var mu sync.Mutex
	cases := 0
	prop.ForAll(t, "the host runs in a case", func(c *prop.Case) {
		mu.Lock()
		cases++
		first := cases == 1
		mu.Unlock()
		body(c, first)
	}, prop.Cases(1), prop.Draws(draws), prop.Store(""))
}

// failure runs body in one case of a property that fails, and returns the
// record of the failure of the case.
func failure(t *testing.T, body func(c *prop.Case)) assert.Failure {
	t.Helper()

	got := assert.Rejects(t, "the host fails the case", func(tb assert.TB) {
		prop.ForAll(tb, "the host fails the case", body, prop.Cases(1), prop.Seed(1), prop.Hermetic(),
			prop.Store(""), prop.Shrink(0))
	})
	assert.Length(t, got, 1, "the property fails once")
	inner, ok := got[0].CaseFailure()
	assert.True(t, ok, "the record of the property contains the failure of its case")
	return inner
}

// bubbled runs body as inCase does, inside a testing/synctest bubble.
func bubbled(t *testing.T, draws string, body func(c *prop.Case, first bool)) {
	t.Helper()

	synctest.Test(t, func(t *testing.T) { inCase(t, draws, body) })
}

// scheduled runs body as inCase does, with a scheduler of each case under
// the strategy uniform.
func scheduled(t *testing.T, draws string, body func(c *prop.Case, s *stateful.Scheduler, first bool)) {
	t.Helper()

	inCase(t, draws, func(c *prop.Case, first bool) { body(c, stateful.NewScheduler(c, stateful.Uniform()), first) })
}

// write writes content to the file name of fsys, which it creates or
// truncates, syncs the file and closes it.
func write(tb assert.TB, fsys medium.FS, name, content string) {
	tb.Helper()

	f, err := fsys.OpenFile(name, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644)
	assert.NoError(tb, err, "the disk opens "+name+" for writing")
	_, err = io.WriteString(f, content)
	assert.NoError(tb, err, "the disk writes "+name)
	assert.NoError(tb, f.Sync(), "the disk syncs "+name)
	assert.NoError(tb, f.Close(), "the disk closes "+name)
}

// read returns the content of the file name of fsys.
func read(tb assert.TB, fsys medium.FS, name string) string {
	tb.Helper()

	f, err := fsys.OpenFile(name, os.O_RDONLY, 0)
	assert.NoError(tb, err, "the disk opens "+name+" for reading")
	content, err := io.ReadAll(f)
	assert.NoError(tb, err, "the disk reads "+name)
	assert.NoError(tb, f.Close(), "the disk closes "+name)
	return string(content)
}

// started returns a host named node-1 of the case c without a network and
// without a scheduler, with its first incarnation.
func started(c *prop.Case) (*host.Host, *host.Incarnation, *host.Env) {
	h := host.New(c, "node-1", nil, nil)
	i, env := h.Start()
	return h, i, env
}
