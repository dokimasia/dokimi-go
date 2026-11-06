// Copyright Dokimasia B.V. 2026
// SPDX-License-Identifier: MIT

package host_test

import (
	"testing"

	"go.dokimi.dev/assert"

	"go.dokimi.dev/host"
)

func TestKill(t *testing.T) {
	t.Parallel()

	t.Run("String", func(t *testing.T) {
		t.Parallel()

		tests := []struct {
			name string
			give host.Kill
			want string
		}{
			{name: "returns the name of a process crash", give: host.ProcessCrash, want: "process-crash"},
			{name: "returns the name of a power loss", give: host.PowerLoss, want: "power-loss"},
			{name: "returns the name of a disk replacement", give: host.DiskReplacement, want: "disk-replacement"},
			{name: "returns the number of a kind outside the three", give: host.Kill(9), want: "Kill(9)"},
		}
		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				t.Parallel()

				assert.Equal(t, tt.give.String(), tt.want, "the name of the kind")
			})
		}
	})
}
