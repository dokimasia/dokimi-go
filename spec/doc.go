// Copyright Dokimasia B.V. 2026
// SPDX-License-Identifier: MIT

// Package spec contains the part of the conformance spec of dokimi that the
// runtime shares with the binary: the identifier of each law, claim, gate,
// probe, reason code, defect form, machinery and stance, and the reason
// codes of each probe.
//
// spec sync copies the files of the spec from dokimi into the directory. go
// generate declares a constant for each identifier of naming.json, with the
// Go spelling that the table gives it, so a generated suite and the runtime
// spell each constant alike. The package embeds the version and the probes
// of the copy, and decodes them on the first call of a function that reads
// them.
package spec

//go:generate go run ./internal/gen/cmd
