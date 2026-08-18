package model

import (
	"testing"
	"time"

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
		&AuditWireSend{},
		&AuditBlob{},
		&AuditConfigSnapshot{},
		&AuditReplayGrant{},
	))
	DB = db
	return db
}

func TestAuditReplayGrantCanOnlyBeClaimedOnce(t *testing.T) {
	setupAuditTraceTestDB(t)

	grant := &AuditReplayGrant{
		TokenDigest: "digest-once",
		TraceId:     7,
		AttemptId:   9,
		Mode:        "exact_upstream",
		State:       "prepared",
		ExpiresAt:   2_000,
	}
	require.NoError(t, CreateAuditReplayGrant(grant))

	claimed, didClaim, err := ClaimAuditReplayGrant("digest-once", 7, 1_000)
	require.NoError(t, err)
	assert.True(t, didClaim)
	assert.Equal(t, "executing", claimed.State)

	repeated, didClaim, err := ClaimAuditReplayGrant("digest-once", 7, 1_001)
	require.NoError(t, err)
	assert.False(t, didClaim)
	assert.Equal(t, claimed.Id, repeated.Id)
}

func TestAuditReplayGrantRejectsExpiredConfirmation(t *testing.T) {
	setupAuditTraceTestDB(t)
	require.NoError(t, CreateAuditReplayGrant(&AuditReplayGrant{
		TokenDigest: "digest-expired", TraceId: 7, AttemptId: 9,
		Mode: "exact_upstream", State: "prepared", ExpiresAt: 999,
	}))

	_, claimed, err := ClaimAuditReplayGrant("digest-expired", 7, 1_000)
	assert.False(t, claimed)
	require.ErrorIs(t, err, ErrAuditReplayGrantExpired)
}

func TestAuditReplayGrantCannotBeConsumedThroughAnotherTrace(t *testing.T) {
	setupAuditTraceTestDB(t)
	require.NoError(t, CreateAuditReplayGrant(&AuditReplayGrant{
		TokenDigest: "digest-bound", TraceId: 7, AttemptId: 9,
		Mode: "exact_upstream", State: "prepared", ExpiresAt: 2_000,
	}))

	_, claimed, err := ClaimAuditReplayGrant("digest-bound", 8, 1_000)
	assert.False(t, claimed)
	require.ErrorIs(t, err, gorm.ErrRecordNotFound)
	stored, claimed, err := ClaimAuditReplayGrant("digest-bound", 7, 1_000)
	require.NoError(t, err)
	assert.True(t, claimed)
	assert.Equal(t, int64(7), stored.TraceId)
}

func TestAuditReplayGrantPersistsIdempotentResult(t *testing.T) {
	setupAuditTraceTestDB(t)
	grant := &AuditReplayGrant{
		TokenDigest: "digest-result", TraceId: 7, AttemptId: 9,
		Mode: "exact_upstream", State: "prepared", ExpiresAt: 2_000,
	}
	require.NoError(t, CreateAuditReplayGrant(grant))
	_, claimed, err := ClaimAuditReplayGrant(grant.TokenDigest, 7, 1_000)
	require.NoError(t, err)
	assert.True(t, claimed)
	require.NoError(t, CompleteAuditReplayGrant(grant.Id, 23, 200, 41, "completed", ""))

	stored, didClaim, err := ClaimAuditReplayGrant(grant.TokenDigest, 7, 1_001)
	require.NoError(t, err)
	assert.False(t, didClaim)
	assert.Equal(t, "completed", stored.State)
	assert.Equal(t, int64(23), stored.ReplayTraceId)
	assert.Equal(t, int64(41), stored.ResponseBlobId)
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

func TestSetAuditTraceOriginalRequestBlobIfEmptyPreservesFirstCapture(t *testing.T) {
	setupAuditTraceTestDB(t)
	trace := &AuditTrace{RequestId: "req-original-capture", Source: "relay", Status: "recording"}
	require.NoError(t, CreateAuditTrace(trace))

	assigned, err := SetAuditTraceOriginalRequestBlobIfEmpty(trace.Id, 11)
	require.NoError(t, err)
	assert.True(t, assigned)
	assigned, err = SetAuditTraceOriginalRequestBlobIfEmpty(trace.Id, 22)
	require.NoError(t, err)
	assert.False(t, assigned)

	stored, err := GetAuditTraceById(trace.Id)
	require.NoError(t, err)
	assert.Equal(t, int64(11), stored.OriginalRequestBlobId)
}

func TestMarkAuditTraceReplayOfLinksGeneratedTraceOnce(t *testing.T) {
	setupAuditTraceTestDB(t)
	trace := &AuditTrace{RequestId: "req-generated-replay", Source: "relay", Status: "succeeded"}
	require.NoError(t, CreateAuditTrace(trace))

	require.NoError(t, MarkAuditTraceReplayOf(trace.RequestId, 41, "replay_client_level"))
	stored, err := GetAuditTraceById(trace.Id)
	require.NoError(t, err)
	assert.Equal(t, int64(41), stored.ReplayOfTraceId)
	assert.Equal(t, "replay_client_level", stored.Source)

	err = MarkAuditTraceReplayOf(trace.RequestId, 42, "replay_client_level")
	require.ErrorIs(t, err, ErrAuditTraceAlreadyLinkedToReplay)
}

func TestDeleteAuditTracesBeforeRemovesOwnedAttemptsAndBlobs(t *testing.T) {
	setupAuditTraceTestDB(t)
	trace := &AuditTrace{RequestId: "req-expired", Source: "relay", Status: "succeeded", CreatedAt: 10}
	require.NoError(t, CreateAuditTrace(trace))
	requestBlob, err := CreateAuditBlob(&AuditBlob{CaptureStage: "upstream_request", Body: []byte(`{}`)})
	require.NoError(t, err)
	responseBlob, err := CreateAuditBlob(&AuditBlob{CaptureStage: "upstream_response", Body: []byte(`{}`)})
	require.NoError(t, err)
	clientBlob, err := CreateAuditBlob(&AuditBlob{CaptureStage: "client_response", Body: []byte(`{}`)})
	require.NoError(t, err)
	attempt := &AuditAttempt{TraceId: trace.Id, AttemptNo: 0, RequestBlobId: requestBlob.Id, ResponseBlobId: responseBlob.Id}
	require.NoError(t, CreateAuditAttempt(attempt))
	require.NoError(t, DB.Model(&AuditTrace{}).Where("id = ?", trace.Id).Update("client_response_blob_id", clientBlob.Id).Error)
	grant := &AuditReplayGrant{
		TokenDigest: "unused-expired-preview", TraceId: trace.Id, Mode: "client_level",
		State: "prepared", ExpiresAt: time.Now().Add(-time.Minute).UnixMilli(),
	}
	require.NoError(t, CreateAuditReplayGrant(grant))

	require.NoError(t, DeleteAuditTracesBefore(20))
	_, err = GetAuditTraceByRequestId(trace.RequestId)
	require.ErrorIs(t, err, gorm.ErrRecordNotFound)
	attempts, err := ListAuditAttempts(trace.Id)
	require.NoError(t, err)
	assert.Empty(t, attempts)
	for _, id := range []int64{requestBlob.Id, responseBlob.Id, clientBlob.Id} {
		_, err = GetAuditBlob(id)
		require.ErrorIs(t, err, gorm.ErrRecordNotFound)
	}
	require.ErrorIs(t, DB.First(&AuditReplayGrant{}, grant.Id).Error, gorm.ErrRecordNotFound)
}

func TestDeleteAuditTracesBeforeRemovesExpiredUnusedPreviewGrantForRetainedTrace(t *testing.T) {
	setupAuditTraceTestDB(t)
	trace := &AuditTrace{RequestId: "req-retained", Source: "relay", Status: "succeeded", CreatedAt: 100}
	require.NoError(t, CreateAuditTrace(trace))
	grant := &AuditReplayGrant{
		TokenDigest: "unused-expired-retained", TraceId: trace.Id, Mode: "client_level",
		State: "prepared", ExpiresAt: time.Now().Add(-time.Minute).UnixMilli(),
	}
	require.NoError(t, CreateAuditReplayGrant(grant))

	require.NoError(t, DeleteAuditTracesBefore(20))
	_, err := GetAuditTraceByRequestId(trace.RequestId)
	require.NoError(t, err)
	require.ErrorIs(t, DB.First(&AuditReplayGrant{}, grant.Id).Error, gorm.ErrRecordNotFound)
}

func TestDeleteAuditTracesBeforeKeepsInFlightGrantForRetainedTrace(t *testing.T) {
	setupAuditTraceTestDB(t)
	trace := &AuditTrace{RequestId: "req-in-flight", Source: "relay", Status: "recording", CreatedAt: 100}
	require.NoError(t, CreateAuditTrace(trace))
	grant := &AuditReplayGrant{
		TokenDigest: "expired-but-in-flight", TraceId: trace.Id, Mode: "client_level",
		State: "executing", ExpiresAt: time.Now().Add(-time.Minute).UnixMilli(),
	}
	require.NoError(t, CreateAuditReplayGrant(grant))

	require.NoError(t, DeleteAuditTracesBefore(20))
	require.NoError(t, DB.First(&AuditReplayGrant{}, grant.Id).Error)
}
