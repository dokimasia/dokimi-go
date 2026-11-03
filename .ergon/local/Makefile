# Copyright Dokimasia B.V. 2026
# SPDX-License-Identifier: MIT

# The settings of the repository of dokimi-go, which ergon init appends to the Makefile.

# The tests of the gate run their properties under the profile ci of assert, which derives the
# seed of each run from the contract of its property. The hooks and CI then run the same cases,
# so a gate that passes before a push passes in CI, and a failure in CI repeats on a workstation.
# A value of DOKIMI_ASSERT_PROP_PROFILE in the environment takes precedence, such as default for
# new random seeds.
test-go race-go: export DOKIMI_ASSERT_PROP_PROFILE ?= ci
