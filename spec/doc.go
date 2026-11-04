// Copyright Dokimasia B.V. 2026
// SPDX-License-Identifier: MIT

// Package spec contains the runtime contract of the conformance spec of
// dokimi: the laws, claims, gates, probes, reason codes, defect forms,
// machineries and stances, the key form of a check identity and the suite
// record.
//
// spec sync copies the files of the contract from dokimi-spec into the
// directory. go generate declares a constant for each identifier of
// naming.json, with the Go spelling that the table gives it, so a generated
// suite and the runtime spell each constant alike. The package embeds the
// files of the copy, and decodes each file on the first call of a function
// that reads it.
//
// Each kind of entry has a function that returns every entry in the order
// of its file, such as [Claims], and a function that returns one entry by
// its identifier, such as [ClaimOf].
package spec

//go:generate go run ./internal/gen/cmd
