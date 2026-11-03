# go.dokimi.dev/spec

## 0.2.0

### Minor Changes

- 317d5ff: Add the claim `causal-history` with its probe and defect form.
- 0a9d402: Add the claim `computes-once` with its law, reason, probe and defect form.
- 0a9d402: Add the claim `linearizable` with its law, reason, probe and defect form.
- 0a9d402: Add the claim `one-holder-at-a-time` with its probe and defect form.
- 317d5ff: Add the claim `pins-the-snapshot-under-writes` with its probe and defect form.
- 0a9d402: Add the claim `reads-agree-under-concurrency` with its law, reason, probe and defect form.
- 0a9d402: Add the five claims of the rules of a runtime of the generator `sim`, with their laws, reasons, probes and defect forms.
- 0a9d402: Add the claims, laws, reasons, probes, gates and defect forms of deliveries, iterations, totals and failed calls.
- d2d7f23: Add the claims of the mixin `durable` with their laws, probes, reasons and defect forms.
- d2d7f23: Add the field `points` to the check record of the suite schema.
- d2d7f23: Add the reference forms of the contracts `persister`, `cache` and `appender`, and the gate `reference-role`.
- 0a9d402: Add the stance `gap` of the mixin `returns-on-error`.

## 0.1.0

### Minor Changes

- f68d5b8: Add the defect forms `ignores-cancel`, `reads-another-value` and `stamps-the-past` to the copy of the conformance spec.
- 4cf3c26: Add the defect forms `settles-twice` and `ignores-the-predicate` to the copy of the conformance spec.
- 9bfacac: Require a context or an input of the callable for `zero-on-error` and `zeroes-on-error` through the gate `context-or-input`.
- 9bfacac: Add the gate `exact-fidelity` of the claim `codec`, which refuses a lossy codec.
- 9bfacac: Refuse the claims of a single caller for a callable with the mixin `runs-until-cancelled` through the gate `returns-before-cancel`.
- 4cf3c26: Add the gate `returns-unaided` to the copy of the conformance spec.
- 9bfacac: Add the source `input` to the schema of the claims for a binding that takes a generated input by its position.
- e708642: Add the inputs `list`, `valid`, `outside` and `name` of the probes of enumerations to the copy of the conformance spec.
- 9421c56: Add `spec.Key`, the key form of a check identity, and the key vectors `keys.json` of the conformance spec.
- 3da277c: Add the module `go.dokimi.dev/spec` with the copy of the conformance spec of dokimi, a constant for each of its entries and the reasons of each probe.
- 3e3aa34: Add `schemas/suite.schema.json` to the copy of the conformance spec.
- 3e3aa34: Add `Probe.Vacuous` with the vacuous condition of each probe.
- 49e6f5f: Add the gate `write-returns-the-value` to the claim `writes-follow-reads`.
- 9bfacac: Replace the stances of the mixins `commutative` and `associative` with claims of their own.
- 9bfacac: Add the claims, laws, reasons, probes and defect forms of the machinery `call-that-fails-on-demand`.
- 49e6f5f: Add the claims, laws, reasons, probes, gates and defect forms of the generated sequences of a declaration.
- 49e6f5f: Add the claims, laws, reasons, probes and defect forms of the classifications without a claim.
- 9bfacac: Add `spec.InputSamples`, `spec.ClassificationParams`, `spec.InterfaceHandle` and `spec.ImportableSamples`, and the field `gate` of each refusal of the suite record schema.

### Patch Changes

- e708642: Fix the claim `partition` to read its partner in the mode `reader`.
- e708642: Require `go.dokimi.dev/assert` v0.12.0.
