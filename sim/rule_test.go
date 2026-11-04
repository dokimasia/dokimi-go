// Copyright Dokimasia B.V. 2026
// SPDX-License-Identifier: MIT

package sim_test

import (
	"testing"

	"go.dokimi.dev/assert"
	"go.dokimi.dev/assert/expect"
	"go.dokimi.dev/spec"

	"go.dokimi.dev/sim"
)

// script is a machine that returns the effects of each of its steps in turn,
// and has the error err.
type script struct {
	err   error
	steps [][]effect
	at    int
}

func (s *script) Step(_ event, out []effect) []effect {
	if s.at < len(s.steps) {
		out = append(out, s.steps[s.at]...)
	}
	s.at++
	return out
}

func (s *script) Err() error { return s.err }

// play steps the machine m on each event of events, and returns the effects
// of each step.
func play(m sim.Machine[event, effect], events ...event) [][]effect {
	out := make([][]effect, 0, len(events))
	for _, ev := range events {
		out = append(out, m.Step(ev, nil))
	}
	return out
}

func TestRule(t *testing.T) {
	t.Parallel()

	t.Run("Rule", func(t *testing.T) {
		t.Parallel()

		for _, rule := range rules {
			t.Run("proves "+rule.ID+" on the defect form "+string(rule.Form), func(t *testing.T) {
				t.Parallel()

				simulation().Prove(t, rule.ID)
			})
		}
	})

	t.Run("Plant", func(t *testing.T) {
		t.Parallel()

		tests := []struct {
			name      string
			giveForm  spec.DefectFormID
			giveSteps [][]effect
			giveOn    []event
			want      [][]effect
		}{
			{
				name:      "returns each reply of a step twice for replies-twice",
				giveForm:  spec.RepliesTwice,
				giveSteps: [][]effect{{reply{to: 1}, commit{token: 1}}},
				giveOn:    []event{ticked{}},
				want:      [][]effect{{reply{to: 1}, commit{token: 1}, reply{to: 1}}},
			},
			{
				name:      "drops the replies of the stop event alone for forgets-a-call-on-stop",
				giveForm:  spec.ForgetsACallOnStop,
				giveSteps: [][]effect{{reply{to: 1}}, {reply{to: 2}, commit{token: 1}}},
				giveOn:    []event{ticked{}, stopped{}},
				want:      [][]effect{{reply{to: 1}}, {commit{token: 1}}},
			},
			{
				name:      "returns the last effect with a token on the stop event for writes-after-stop",
				giveForm:  spec.WritesAfterStop,
				giveSteps: [][]effect{{commit{token: 1}}, {commit{token: 2}, reply{to: 1}}, {reply{to: 2}}, {}},
				giveOn:    []event{ticked{}, ticked{}, stopped{}, ticked{}},
				want: [][]effect{
					{commit{token: 1}}, {commit{token: 2}, reply{to: 1}}, {reply{to: 2}, commit{token: 2}}, nil,
				},
			},
			{
				name:     "returns the first effect with a token in each later step with one for reuses-a-token",
				giveForm: spec.ReusesAToken,
				giveSteps: [][]effect{
					{commit{token: 1}}, {reply{to: 1}}, {commit{token: 2}}, {commit{token: 3}},
				},
				giveOn: []event{ticked{}, ticked{}, ticked{}, ticked{}},
				want: [][]effect{
					{commit{token: 1}},
					{reply{to: 1}},
					{commit{token: 2}, commit{token: 1}},
					{commit{token: 3}, commit{token: 1}},
				},
			},
			{
				name:      "keeps the effects of every step for steps-after-error",
				giveForm:  spec.StepsAfterError,
				giveSteps: [][]effect{{commit{token: 1}}, {reply{to: 1}}, {commit{token: 2}}},
				giveOn:    []event{ticked{}, ticked{}, stopped{}},
				want:      [][]effect{{commit{token: 1}}, {reply{to: 1}}, {commit{token: 2}}},
			},
		}
		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				t.Parallel()

				m := sim.Plant[event, effect](&script{steps: tt.giveSteps}, anatomy, tt.giveForm)
				expect.Equal(t, play(m, tt.giveOn...), tt.want, "the planted defect changes the effects of its form")
			})
		}

		t.Run("returns an error from the second step on for steps-after-error", func(t *testing.T) {
			t.Parallel()

			m := sim.Plant[event, effect](&script{}, anatomy, spec.StepsAfterError)
			play(m, ticked{})
			assert.Nil(t, m.Err(), "the machine runs after its first step")
			play(m, ticked{})
			expect.NotNil(t, m.Err(), "the machine stops itself after its second step")
		})

		t.Run("leaves the effects and the error of the machine for another form", func(t *testing.T) {
			t.Parallel()

			m := sim.Plant[event, effect](&script{err: errEmpty, steps: [][]effect{{reply{to: 1}}}}, anatomy, "another")
			expect.Equal(t, play(m, stopped{}), [][]effect{{reply{to: 1}}}, "the machine keeps its effects")
			expect.ErrorIs(t, m.Err(), errEmpty, "the machine keeps its error")
		})
	})
}
