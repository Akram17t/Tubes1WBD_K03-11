package migration

import (
	"errors"
	"net/http"

	"tubes-wbd/src/api"
)

// Posisi migration diturunkan dari event sukses, bukan disimpan sebagai status.
const (
	PositionNeverApplied = "never_applied"
	PositionActive       = "active"
	PositionRolledBack   = "rolled_back"
	PositionSuperseded   = "superseded"
)

var errInconsistentHistory = errors.New("history sukses database tidak membentuk urutan linear yang sah")

type successEvent struct {
	ExecutionID       int64
	MigrationID       int64
	Operation         string
	TargetExecutionID *int64
}

type stackEntry struct {
	ExecutionID int64
	MigrationID int64
}

// databasePosition adalah active stack dan forward stack satu database; elemen terakhir adalah puncak.
// Entry forward menyimpan execution sukses yang dibatalkan, yaitu target request forward.
type databasePosition struct {
	active  []stackEntry
	forward []stackEntry
	applied map[int64]bool
}

// buildPosition memutar ulang event sukses menurut urutannya.
func buildPosition(events []successEvent) (databasePosition, error) {
	position := databasePosition{applied: map[int64]bool{}}
	for _, event := range events {
		entry := stackEntry{event.ExecutionID, event.MigrationID}
		switch event.Operation {
		case "up":
			position.active = append(position.active, entry)
			position.forward = nil
			position.applied[event.MigrationID] = true
		case "rollback":
			top, ok := position.activeTop()
			if !ok || event.TargetExecutionID == nil || top.ExecutionID != *event.TargetExecutionID {
				return position, errInconsistentHistory
			}
			position.active = position.active[:len(position.active)-1]
			position.forward = append(position.forward, top)
		case "forward":
			next, ok := position.forwardTop()
			if !ok || next.MigrationID != event.MigrationID {
				return position, errInconsistentHistory
			}
			position.forward = position.forward[:len(position.forward)-1]
			position.active = append(position.active, entry)
		case "compensate":
			// Kompensasi membatalkan operasi yang belum pernah selesai, jadi kedua stack tidak berubah.
		default:
			return position, errInconsistentHistory
		}
	}
	return position, nil
}

func (p databasePosition) activeTop() (stackEntry, bool) {
	if len(p.active) == 0 {
		return stackEntry{}, false
	}
	return p.active[len(p.active)-1], true
}

func (p databasePosition) forwardTop() (stackEntry, bool) {
	if len(p.forward) == 0 {
		return stackEntry{}, false
	}
	return p.forward[len(p.forward)-1], true
}

func (p databasePosition) of(migrationID int64) string {
	for _, entry := range p.active {
		if entry.MigrationID == migrationID {
			return PositionActive
		}
	}
	for _, entry := range p.forward {
		if entry.MigrationID == migrationID {
			return PositionRolledBack
		}
	}
	if p.applied[migrationID] {
		return PositionSuperseded
	}
	return PositionNeverApplied
}

// jobBlocker adalah job yang meninggalkan efek belum tuntas sehingga menahan operasi lain.
type jobBlocker struct {
	JobID  int64
	Status string
}

func databaseBlocked(blocker jobBlocker) error {
	return &DefinitionError{http.StatusConflict, api.CodeDatabaseBlocked,
		"Database ditahan oleh job yang belum tuntas. Lanjutkan atau kompensasi job tersebut terlebih dahulu.",
		map[string]any{"blocking_job_id": api.FormatID(blocker.JobID), "block_reason": blocker.Status}, ErrConflict}
}
