// Copyright Dokimasia B.V. 2026
// SPDX-License-Identifier: MIT

package spec

import (
	"embed"
	"encoding/json"
	"strings"
	"sync"
)

// LawID identifies a law.
type LawID string

// ClaimID identifies a claim. It is the segment of each check of the claim.
type ClaimID string

// GateID identifies a gate.
type GateID string

// ProbeID identifies a probe.
type ProbeID string

// ReasonID identifies a reason code.
type ReasonID string

// DefectFormID identifies a form of a planted defect.
type DefectFormID string

// MachineryID identifies a machinery.
type MachineryID string

// StanceID identifies a stance.
type StanceID string

// Probe is the procedure of a check, with the reason codes that it reports.
// The package probe implements each probe of the spec.
type Probe struct {
	ID ProbeID `json:"id"`

	// Reasons lists the reason codes that the probe reports. The handle
	// fails a check whose probe sets another code.
	Reasons []ReasonID `json:"reasons"`
}

// files contains the version and the probes of the copy of the spec. The
// test of the package compares the probes that [Probes] returns with the
// probes that encoding/json reads from the same file, so a file that does
// not decode fails the test, and no function reports a decoding error.
//
//go:embed VERSION probes.json
var files embed.FS

// Version returns the version of the spec from the file VERSION, such as
// 1.0.0.
func Version() string {
	return version()
}

var version = sync.OnceValue(func() string {
	data, _ := files.ReadFile("VERSION")
	return strings.TrimSpace(string(data))
})

// probes contains the probes in the order of probes.json, and the position
// of each by its identifier.
var probes = sync.OnceValues(func() ([]Probe, map[ProbeID]int) {
	data, _ := files.ReadFile("probes.json")
	var doc struct {
		Probes []Probe `json:"probes"`
	}
	_ = json.Unmarshal(data, &doc)
	at := make(map[ProbeID]int, len(doc.Probes))
	for i, p := range doc.Probes {
		at[p.ID] = i
	}
	return doc.Probes, at
})

// Probes returns every probe in the order of probes.json. The slice is
// shared with every other call, so a caller does not modify it.
func Probes() []Probe {
	all, _ := probes()
	return all
}

// ProbeOf returns the probe with the identifier id, and false when the spec
// has no such probe.
func ProbeOf(id ProbeID) (Probe, bool) {
	all, at := probes()
	i, ok := at[id]
	if !ok {
		return Probe{}, false
	}
	return all[i], true
}
