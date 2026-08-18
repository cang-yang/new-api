package model

import (
	"fmt"
	"sync"
	"testing"

	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func setupErrorIncidentTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	previousDB, previousLogDB := DB, LOG_DB
	t.Cleanup(func() { DB, LOG_DB = previousDB, previousLogDB })
	dsn := fmt.Sprintf("file:error-incidents-%s?mode=memory&cache=shared&_pragma=busy_timeout(10000)", t.Name())
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{})
	require.NoError(t, err)
	sqlDB, err := db.DB()
	require.NoError(t, err)
	sqlDB.SetMaxOpenConns(1)
	require.NoError(t, db.AutoMigrate(&ErrorIncident{}, &Log{}))
	DB, LOG_DB = db, db
	return db
}

func TestUpsertErrorIncidentIsAtomicUnderConcurrency(t *testing.T) {
	setupErrorIncidentTestDB(t)
	const workers = 32
	var wg sync.WaitGroup
	errs := make(chan error, workers)
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			_, err := UpsertErrorIncident(ErrorIncidentOccurrence{
				Fingerprint: "cef1_0123456789abcdef01234567",
				RequestId:   fmt.Sprintf("req-%d", i), StatusCode: 502,
				ErrorType: "openai_error", ErrorCode: "bad_response_body",
				ChannelType: 1, Model: "demo", Path: "/v1/chat/completions",
			})
			errs <- err
		}(i)
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		require.NoError(t, err)
	}
	incident, err := GetErrorIncident("cef1_0123456789abcdef01234567")
	require.NoError(t, err)
	assert.EqualValues(t, workers, incident.Count)
	var rows int64
	require.NoError(t, DB.Model(&ErrorIncident{}).Count(&rows).Error)
	assert.EqualValues(t, 1, rows)
}

func TestNewOccurrenceReopensResolvedIncident(t *testing.T) {
	setupErrorIncidentTestDB(t)
	occurrence := ErrorIncidentOccurrence{Fingerprint: "cef1_aaaaaaaaaaaaaaaaaaaaaaaa", StatusCode: 502}
	_, err := UpsertErrorIncident(occurrence)
	require.NoError(t, err)
	require.NoError(t, SetErrorIncidentResolved(occurrence.Fingerprint, true))
	incident, err := GetErrorIncident(occurrence.Fingerprint)
	require.NoError(t, err)
	require.NotNil(t, incident.ResolvedAt)

	incident, err = UpsertErrorIncident(occurrence)
	require.NoError(t, err)
	assert.Nil(t, incident.ResolvedAt)
	assert.EqualValues(t, 2, incident.Count)
}

func TestResolveMissingErrorIncidentReturnsNotFound(t *testing.T) {
	setupErrorIncidentTestDB(t)
	require.ErrorIs(t, SetErrorIncidentResolved("cef1_cccccccccccccccccccccccc", true), ErrErrorIncidentNotFound)
}

func TestErrorIncidentSamplesStripSensitiveFields(t *testing.T) {
	setupErrorIncidentTestDB(t)
	require.NoError(t, createLog(&Log{
		Type: LogTypeError, CreatedAt: 10, Content: "full upstream error",
		Other: `{"error_fingerprint":"cef1_bbbbbbbbbbbbbbbbbbbbbbbb","prompt":"secret"}`,
		Ip:    "192.0.2.1", TokenName: "secret-token", RequestId: "req-sample",
	}))
	logs, err := ListErrorIncidentSamples("cef1_bbbbbbbbbbbbbbbbbbbbbbbb", 10)
	require.NoError(t, err)
	require.Len(t, logs, 1)
	assert.Empty(t, logs[0].Content)
	assert.Empty(t, logs[0].Other)
	assert.Empty(t, logs[0].Ip)
	assert.Empty(t, logs[0].TokenName)
	assert.Equal(t, "req-sample", logs[0].RequestId)
}
