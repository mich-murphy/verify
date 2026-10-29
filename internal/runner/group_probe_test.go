package runner

import (
	"context"
	"errors"
	"syscall"
	"testing"
)

func TestGroupProbeOnlyAbsenceSkipsInspection(t *testing.T) {
	inspectionErr := errors.New("inspection unavailable")
	for _, tc := range []struct {
		name       string
		probeErr   error
		table      string
		tableErr   error
		wantAlive  bool
		wantErr    bool
		wantTables int
	}{
		{name: "absent", probeErr: syscall.ESRCH, tableErr: inspectionErr},
		{name: "live", table: "42 S\n", wantAlive: true, wantTables: 1},
		{name: "zombie", table: "42 Z\n", wantTables: 1},
		{name: "zombie and live", table: "42 Z\n42 S\n", wantAlive: true, wantTables: 1},
		{name: "permission denied live", probeErr: syscall.EPERM, table: "42 S\n", wantAlive: true, wantTables: 1},
		{name: "permission denied zombie", probeErr: syscall.EPERM, table: "42 Z\n", wantTables: 1},
		{name: "unexpected probe error", probeErr: syscall.EIO, table: "42 S\n", wantAlive: true, wantTables: 1},
		{name: "inspection failure", tableErr: inspectionErr, wantErr: true, wantTables: 1},
		{name: "probe and inspection failure", probeErr: syscall.EPERM, tableErr: inspectionErr, wantErr: true, wantTables: 1},
		{name: "malformed inspection", table: "bad S\n", wantErr: true, wantTables: 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := testRunner(t, Config{})
			probes, tables := 0, 0
			r.process.probe = func(group int) error {
				probes++
				if group != 42 {
					t.Fatalf("probed wrong group: %d", group)
				}
				return tc.probeErr
			}
			r.process.table = func(context.Context) ([]byte, error) {
				tables++
				return []byte(tc.table), tc.tableErr
			}
			alive, err := r.groupAlive(t.Context(), 42)
			if alive != tc.wantAlive || (err != nil) != tc.wantErr || probes != 1 || tables != tc.wantTables {
				t.Fatalf("alive=%v err=%v probes=%d tables=%d", alive, err, probes, tables)
			}
			if tc.wantErr && tc.tableErr != nil && !errors.Is(err, tc.tableErr) {
				t.Fatalf("lost inspection cause: %v", err)
			}
		})
	}
}
