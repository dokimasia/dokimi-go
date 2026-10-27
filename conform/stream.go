// Copyright Dokimasia B.V. 2026
// SPDX-License-Identifier: MIT

package conform

import "go.dokimi.dev/assert/prop"

// streamLabel is the label of the draw of the elements of a stream.
const streamLabel = "elements"

// Stream returns the generator of the channel type C whose values are
// streams. Each stream is a closed channel with a list of elements in its
// buffer, which elements generates. Its receives return the elements in order
// and then report that the channel is closed, so they never block. A send to
// a stream panics. A failure shrinks towards a stream without elements.
//
// A generated suite states a stream as the generator of each receive channel
// and each bidirectional channel that the inputs of its calls contain:
//
//	prop.Using(conform.Stream[<-chan time.Time](prop.Of[time.Time]()))
func Stream[C ~<-chan T | ~chan T, T any](elements prop.Generator[T]) prop.Generator[C] {
	return prop.Composite(func(c *prop.Case) C {
		list := c.Draw(prop.List(elements), streamLabel)
		ch := make(chan T, len(list))
		for _, v := range list {
			ch <- v
		}
		close(ch)
		return C(ch)
	})
}

// Closed returns a closed channel of the type C without elements. A generated
// function returns it for a channel result after its last drawn values, so a
// caller that receives until the channel closes stops.
func Closed[C ~<-chan T | ~chan T, T any]() C {
	ch := make(chan T)
	close(ch)
	return C(ch)
}
