// Copyright Dokimasia B.V. 2026
// SPDX-License-Identifier: MIT

package conform

import (
	"context"
	"slices"

	"go.dokimi.dev/assert/prop"
)

// Sink is a send channel type that the inputs of a suite contain. Each entry
// point of the runtime makes one channel of the type for its test, and states
// the channel as the generator of the type in every run of the test. A
// goroutine receives and drops each value that a subject sends, until the
// context of the test ends. The runtime does not close the channel, so a send
// after the end of the test blocks the goroutine that sends. A test without
// the method Context has no end that the runtime can read, so the goroutine
// of each of its sinks runs until the process ends. The zero Sink makes no
// channel.
//
// # Concurrency
//
// The channel of a sink takes the sends of any number of goroutines at once.
type Sink struct {
	// open makes the channel for the test of ctx and starts the goroutine
	// that drains it until ctx ends. It returns the option that states the
	// channel.
	open func(ctx context.Context) prop.Option
}

// SinkOf returns the sink of the send channel type C. A generated suite lists
// the sink of each send channel type that the inputs of its calls contain:
//
//	Sinks: []conform.Sink{conform.SinkOf[chan<- string]()},
func SinkOf[C ~chan<- T, T any]() Sink {
	return Sink{open: func(ctx context.Context) prop.Option {
		ch := make(chan T)
		go drain(ctx, ch)
		return prop.Using(prop.Just(C(ch)))
	}}
}

// drain receives and drops each value of ch until ctx ends.
func drain[T any](ctx context.Context, ch <-chan T) {
	for {
		select {
		case <-ch:
		case <-ctx.Done():
			return
		}
	}
}

// overrides returns the overrides of the suite for an entry point on the test
// tb: the options of Using, then the option of each sink, which the sink opens
// on the context of tb. It does not write into the spare capacity of Using.
func (s Suite[S]) overrides(tb any) []prop.Option {
	opts := slices.Clip(s.Using)
	ctx := contextOf(tb)
	for _, sink := range s.Sinks {
		if sink.open != nil {
			opts = append(opts, sink.open(ctx))
		}
	}
	return opts
}

// contextOf returns the context of the test of an entry point: the context
// that its method Context returns, and context.Background() for a test
// without the method.
func contextOf(tb any) context.Context {
	if c, ok := tb.(interface{ Context() context.Context }); ok {
		return c.Context()
	}
	return context.Background()
}
