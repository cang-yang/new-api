package model

import (
	"testing"

	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func setupAuditTraceTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	previousDB := DB
	t.Cleanup(func() { DB = previousDB })

	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, db.AutoMigrate(
		&AuditTrace{},
		&AuditAttempt{},
		&AuditBlob{},
		&AuditConfigSnapshot{},
	))
	DB = db
	return db
}

func TestAuditAttemptsPreserveEveryApplicationRetry(t *testing.T) {
	setupAuditTraceTestDB(t)

	trace := &AuditTrace{RequestId: "req-trace-retries", Source: "relay", Status: "recording"}
	require.NoError(t, CreateAuditTrace(trace))
	require.NoError(t, CreateAuditAttempt(&AuditAttempt{
		TraceId:     trace.Id,
		AttemptNo:   0,
		ChannelId:   3,
		State:       "failed",
		HTTPStatus:  500,
		RetryAction: "retry",
	}))
	require.NoError(t, CreateAuditAttempt(&AuditAttempt{
		TraceId:     trace.Id,
		AttemptNo:   1,
		ChannelId:   8,
		State:       "succeeded",
		HTTPStatus:  200,
		RetryAction: "stop",
	}))

	attempts, err := ListAuditAttempts(trace.Id)
	require.NoError(t, err)
	require.Len(t, attempts, 2)
	assert.Equal(t, 0, attempts[0].AttemptNo)
	assert.Equal(t, 3, attempts[0].ChannelId)
	assert.Equal(t, 1, attempts[1].AttemptNo)
	assert.Equal(t, 8, attempts[1].ChannelId)
}

func TestAuditAttemptNumberIsUniqueWithinTrace(t *testing.T) {
	setupAuditTraceTestDB(t)

	trace := &AuditTrace{RequestId: "req-trace-unique", Source: "relay", Status: "recording"}
	require.NoError(t, CreateAuditTrace(trace))
	require.NoError(t, CreateAuditAttempt(&AuditAttempt{TraceId: trace.Id, AttemptNo: 0}))

	err := CreateAuditAttempt(&AuditAttempt{TraceId: trace.Id, AttemptNo: 0})
	require.Error(t, err)
}

func TestAuditConfigSnapshotDeduplicatesCanonicalPayload(t *testing.T) {
	setupAuditTraceTestDB(t)

	first, err := GetOrCreateAuditConfigSnapshot("channel", []byte(`{"channel_id":3,"param_override":{}}`))
	require.NoError(t, err)
	second, err := GetOrCreateAuditConfigSnapshot("channel", []byte(`{"channel_id":3,"param_override":{}}`))
	require.NoError(t, err)

	assert.Equal(t, first.Id, second.Id)
	assert.Equal(t, first.Digest, second.Digest)
}

func TestFinalizeAuditTraceKeepsFinalAttemptAndClientResult(t *testing.T) {
	setupAuditTraceTestDB(t)

	trace := &AuditTrace{RequestId: "req-trace-final", Source: "relay", Status: "recording"}
	require.NoError(t, CreateAuditTrace(trace))
	attempt := &AuditAttempt{TraceId: trace.Id, AttemptNo: 0, State: "succeeded"}
	require.NoError(t, CreateAuditAttempt(attempt))
	blob, err := CreateAuditBlob(&AuditBlob{
		CaptureStage: "client_response",
		MediaType:    "application/json",
		Body:         []byte(`{"ok":true}`),
		OriginalSize: 11,
		Complete:     true,
	})
	require.NoError(t, err)

	require.NoError(t, FinalizeAuditTrace(trace.Id, attempt.Id, blob.Id, "succeeded", "protocol_terminal"))
	stored, err := GetAuditTraceByRequestId(trace.RequestId)
	require.NoError(t, err)
	assert.Equal(t, attempt.Id, stored.FinalAttemptId)
	assert.Equal(t, blob.Id, stored.ClientResponseBlobId)
	assert.Equal(t, "succeeded", stored.Status)
	assert.Equal(t, "protocol_terminal", stored.ClientTerminalKind)
}
