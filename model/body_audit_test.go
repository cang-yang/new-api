package model

import (
	"testing"

	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestBodyAuditUpsertKeepsLatestAttempt(t *testing.T) {
	previousDB := DB
	t.Cleanup(func() { DB = previousDB })

	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, db.AutoMigrate(&BodyAudit{}))
	DB = db

	require.NoError(t, UpsertBodyAudit(&BodyAudit{
		RequestId:        "req-audit-1",
		ChannelId:        3,
		RequestBody:      []byte(`{"model":"first"}`),
		RequestBodySize:  17,
		ResponseBody:     []byte(`{"error":"retry"}`),
		ResponseBodySize: 17,
		ResponseStatus:   500,
	}))
	require.NoError(t, UpsertBodyAudit(&BodyAudit{
		RequestId:        "req-audit-1",
		ChannelId:        8,
		RequestBody:      []byte(`{"model":"final"}`),
		RequestBodySize:  17,
		ResponseBody:     []byte(`{"content":"ok"}`),
		ResponseBodySize: 16,
		ResponseStatus:   200,
		ResponseComplete: true,
	}))

	audit, err := GetBodyAuditByRequestId("req-audit-1")
	require.NoError(t, err)
	assert.Equal(t, 8, audit.ChannelId)
	assert.Equal(t, []byte(`{"model":"final"}`), audit.RequestBody)
	assert.Equal(t, []byte(`{"content":"ok"}`), audit.ResponseBody)
	assert.Equal(t, 200, audit.ResponseStatus)
	assert.True(t, audit.ResponseComplete)
}
