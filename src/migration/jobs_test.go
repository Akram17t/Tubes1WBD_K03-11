package migration

import (
	"errors"
	"fmt"
	"io"
	"testing"

	"github.com/jackc/pgx/v5/pgconn"
)

func TestClassifyExecutionError(t *testing.T) {
	pg := func(code string) error { return fmt.Errorf("exec: %w", &pgconn.PgError{Code: code, Message: "pesan"}) }
	for _, test := range []struct {
		name      string
		err       error
		committed bool
		status    string
		code      string
		retryable bool
	}{
		{"deadlock", pg("40P01"), false, JobFailed, JobErrorTransient, true},
		{"serialization", pg("40001"), false, JobFailed, JobErrorTransient, true},
		{"lock timeout", pg("55P03"), false, JobFailed, JobErrorTransient, true},
		{"syntax", pg("42601"), false, JobFailed, JobErrorSQL, false},
		{"constraint", pg("23505"), false, JobFailed, JobErrorSQL, false},
		{"permission", pg("42501"), false, JobFailed, JobErrorSQL, false},
		{"server shutdown", pg("57P01"), false, JobFailed, JobErrorConnectionLost, true},
		{"koneksi sebelum commit", io.ErrUnexpectedEOF, false, JobFailed, JobErrorConnectionLost, true},
		{"koneksi saat commit", io.ErrUnexpectedEOF, true, JobOutcomeUnknown, JobErrorOutcomeUnknown, false},
		{"server error saat commit", pg("40001"), true, JobOutcomeUnknown, JobErrorOutcomeUnknown, false},
	} {
		result := ClassifyExecutionError(test.err, test.committed)
		if result.Status != test.status || result.ErrorCode != test.code || result.Retryable != test.retryable {
			t.Errorf("%s: %+v", test.name, result)
		}
	}
	// pesan driver tidak boleh diteruskan
	secret := errors.New("dial postgres://user:rahasia@host")
	if result := ClassifyExecutionError(secret, false); result.ErrorMessage == secret.Error() {
		t.Fatal("pesan koneksi bocor")
	}
}

func TestJobStateRules(t *testing.T) {
	none := JobProgress{}
	applied := JobProgress{CommittedBatches: 2}
	reverted := JobProgress{RevertedBatches: 1}
	for _, test := range []struct {
		operation string
		progress  JobProgress
		stopped   string
		blocksRun bool
	}{
		{"up", none, JobFailed, false},
		{"up", applied, JobPartial, true},
		{"forward", applied, JobPartial, true},
		{"rollback", none, JobFailed, false},
		{"rollback", reverted, JobPartiallyReverted, true},
		{"compensate", reverted, JobPartiallyReverted, true},
	} {
		if got := stoppedStatus(test.operation, test.progress); got != test.stopped {
			t.Errorf("%s %+v: %s", test.operation, test.progress, got)
		}
		if got := blocksDatabase(test.operation, JobQueued, test.progress); got != test.blocksRun {
			t.Errorf("blocker %s %+v: %v", test.operation, test.progress, got)
		}
	}

	failed := func(code string) *JobDetail {
		return &JobDetail{Job: Job{Status: JobFailed, Error: &JobError{Code: code}}}
	}
	if !resumable(failed(JobErrorTargetUnavailable)) || resumable(failed(JobErrorSQL)) || resumable(failed("forbidden")) {
		t.Error("resume job failed hanya untuk penyebab di luar SQL")
	}
	for _, status := range []string{JobQueued, JobRunning, JobSuccess, JobCompensated, JobOutcomeUnknown} {
		if resumable(&JobDetail{Job: Job{Status: status}}) {
			t.Errorf("%s tidak boleh di-resume", status)
		}
	}
	other := int64(9)
	if !resumable(&JobDetail{Job: Job{Status: JobPartial}}) || resumable(&JobDetail{Job: Job{Status: JobPartial}, CompensationJobID: &other}) {
		t.Error("partial dapat dilanjutkan kecuali sudah dikompensasi")
	}
}

func TestTraceCarrierKeepsOnlyTraceContext(t *testing.T) {
	encoded, err := traceCarrier(map[string]string{"traceparent": "00-a-b-01", "cookie": "session=x", "authorization": "y"})
	if err != nil || string(encoded) != `{"traceparent":"00-a-b-01"}` {
		t.Fatalf("%s %v", encoded, err)
	}
}
