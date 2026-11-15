package controller

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/QuantumNous/new-api/relaykit/types"
	"github.com/QuantumNous/new-api/service"

	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/mysql"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/schema"
)

func TestProcessChannelErrorUsesSnapshotWithoutLeakingChannelMetadata(t *testing.T) {
	gin.SetMode(gin.TestMode)
	previousDB, previousLogDB := model.DB, model.LOG_DB
	previousRedisEnabled := common.RedisEnabled
	previousMainDatabaseType := common.MainDatabaseType()
	previousLogDatabaseType := common.LogDatabaseType()
	previousErrorLogEnabled := constant.ErrorLogEnabled

	database, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	sqlDB, err := database.DB()
	require.NoError(t, err)
	sqlDB.SetMaxOpenConns(1)
	require.NoError(t, database.AutoMigrate(&model.User{}, &model.Log{}))
	model.DB, model.LOG_DB = database, database
	common.RedisEnabled = false
	common.SetDatabaseTypes(common.DatabaseTypeSQLite, common.DatabaseTypeSQLite)
	constant.ErrorLogEnabled = true
	t.Cleanup(func() {
		model.DB, model.LOG_DB = previousDB, previousLogDB
		common.RedisEnabled = previousRedisEnabled
		common.SetDatabaseTypes(previousMainDatabaseType, previousLogDatabaseType)
		constant.ErrorLogEnabled = previousErrorLogEnabled
		require.NoError(t, sqlDB.Close())
	})

	require.NoError(t, database.Create(&model.User{Id: 7, Username: "log-owner", Group: "default"}).Error)
	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	ctx.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
	ctx.Set("id", 7)
	ctx.Set("username", "log-owner")
	ctx.Set("token_name", "test-token")
	ctx.Set("token_id", 11)
	ctx.Set("original_model", "gpt-test")
	ctx.Set("group", "default")
	ctx.Set("channel_id", 202)
	ctx.Set("channel_name", "mutable-context-channel")
	ctx.Set("channel_type", 9)
	ctx.Set("use_channel", []string{"101"})
	common.SetContextKey(ctx, constant.ContextKeyRequestStartTime, time.Now().Add(-time.Second))

	channelSnapshot := types.ChannelError{
		ChannelId:   101,
		ChannelType: 1,
		ChannelName: "snapshot-channel",
		AutoBan:     false,
	}
	apiErr := types.NewOpenAIError(errors.New("upstream failed"), types.ErrorCodeBadResponseStatusCode, http.StatusBadGateway)

	processChannelError(ctx, channelSnapshot, apiErr, nil)

	var stored model.Log
	require.NoError(t, database.First(&stored).Error)
	assert.Equal(t, channelSnapshot.ChannelId, stored.ChannelId)
	storedOther, err := common.StrToMap(stored.Other)
	require.NoError(t, err)
	assert.Equal(t, float64(http.StatusBadGateway), storedOther["status_code"])
	for _, key := range []string{"channel_id", "channel_name", "channel_type"} {
		assert.NotContains(t, storedOther, key)
	}
	adminInfo, ok := storedOther["admin_info"].(map[string]any)
	require.True(t, ok)
	assert.Equal(t, []any{"101"}, adminInfo["use_channel"])

	logs, total, err := model.GetUserLogs(7, model.LogFilter{Type: model.LogTypeError}, 0, 10)
	require.NoError(t, err)
	require.Equal(t, int64(1), total)
	require.Len(t, logs, 1)
	assert.Equal(t, channelSnapshot.ChannelId, logs[0].ChannelId)
	assert.Empty(t, logs[0].ChannelName)
	userOther, err := common.StrToMap(logs[0].Other)
	require.NoError(t, err)
	assert.NotContains(t, userOther, "admin_info")
	for _, key := range []string{"channel_id", "channel_name", "channel_type"} {
		assert.NotContains(t, userOther, key)
	}
}

func TestRelayFailureBodyAudit(t *testing.T) {
	for _, dialect := range []string{"sqlite", "mysql", "postgres"} {
		t.Run(dialect, func(t *testing.T) {
			var driver gorm.Dialector
			switch dialect {
			case "sqlite":
				driver = sqlite.Open(filepath.Join(t.TempDir(), "failures.db"))
			case "mysql":
				if os.Getenv("TEST_MYSQL_DSN") == "" {
					t.Skip("TEST_MYSQL_DSN is not configured")
				}
				driver = mysql.Open(os.Getenv("TEST_MYSQL_DSN"))
			case "postgres":
				if os.Getenv("TEST_POSTGRES_DSN") == "" {
					t.Skip("TEST_POSTGRES_DSN is not configured")
				}
				driver = postgres.Open(os.Getenv("TEST_POSTGRES_DSN"))
			}
			db, err := gorm.Open(driver, &gorm.Config{NamingStrategy: schema.NamingStrategy{TablePrefix: "relay_failure_test_"}})
			require.NoError(t, err)
			sqlDB, err := db.DB()
			require.NoError(t, err)
			sqlDB.SetMaxOpenConns(1)
			versionQuery := "select version()"
			if dialect == "sqlite" {
				versionQuery = "select sqlite_version()"
			}
			var engineVersion string
			require.NoError(t, db.Raw(versionQuery).Scan(&engineVersion).Error)
			t.Logf("database=%s version=%s", dialect, engineVersion)
			previousDB, previousLogDB := model.DB, model.LOG_DB
			previousMain, previousLog := common.MainDatabaseType(), common.LogDatabaseType()
			previousAudit, previousErrorLog, previousLimit := constant.BodyAuditEnabled, constant.ErrorLogEnabled, constant.BodyAuditMaxBodyMB
			previousRedis := common.RedisEnabled
			model.DB, model.LOG_DB = db, db
			common.SetDatabaseTypes(common.DatabaseType(dialect), common.DatabaseType(dialect))
			common.RedisEnabled = false
			constant.BodyAuditEnabled, constant.ErrorLogEnabled, constant.BodyAuditMaxBodyMB = true, true, 1
			models := []any{&model.User{}, &model.Log{}, &model.BodyAudit{}, &model.AuditTrace{}, &model.AuditAttempt{}, &model.AuditWireSend{}, &model.AuditConfigSnapshot{}, &model.AuditBlob{}, &model.AuditReplayGrant{}}
			t.Cleanup(func() {
				require.NoError(t, db.Migrator().DropTable(models...))
				model.DB, model.LOG_DB = previousDB, previousLogDB
				common.SetDatabaseTypes(previousMain, previousLog)
				common.RedisEnabled = previousRedis
				constant.BodyAuditEnabled, constant.ErrorLogEnabled, constant.BodyAuditMaxBodyMB = previousAudit, previousErrorLog, previousLimit
				require.NoError(t, sqlDB.Close())
			})
			require.NoError(t, db.AutoMigrate(models...))
			require.NoError(t, db.Create(&model.User{Id: 7, Username: "failure-owner", Group: "default"}).Error)
			for _, tc := range []struct {
				name, body, script, want string
				audit                    bool
			}{
				{"invalid-json", `{"model":`, "", "error", true},
				{"script-exception", `{"model":"test-model","messages":[{"role":"user","content":"private client request"}]}`, `() => { throw new TypeError("PRIVATE EXCEPTION"); }`, "TypeError", true},
				{"missing-api", `{"model":"test-model","messages":[{"role":"user","content":"private client request"}]}`, `() => unavailablePresetAPI()`, "ReferenceError", true},
				{"audit-off", `{"model":`, "", "error", false},
			} {
				t.Run(tc.name, func(t *testing.T) {
					constant.BodyAuditEnabled = tc.audit
					recorder := httptest.NewRecorder()
					ctx, _ := gin.CreateTestContext(recorder)
					ctx.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(tc.body))
					ctx.Request.Header.Set("Content-Type", "application/json")
					ctx.Set(common.RequestIdKey, "failure-"+tc.name)
					ctx.Set("id", 7)
					ctx.Set("username", "failure-owner")
					ctx.Set("token_id", 11)
					ctx.Set("token_name", "test-token")
					ctx.Set("group", "default")
					ctx.Set("original_model", "test-model")
					ctx.Set("channel_id", 101)
					ctx.Set("channel_type", 1)
					if tc.script != "" {
						preset, err := common.Marshal(map[string]any{
							"prompts":      []any{map[string]any{"identifier": "main", "role": "system", "content": "Follow the client request."}},
							"prompt_order": []any{map[string]any{"order": []any{map[string]any{"identifier": "main", "enabled": true}}}},
							"extensions":   map[string]any{"SPreset": map[string]any{"FinalRequestProcessing": map[string]any{"enabled": true, "script": tc.script}}},
						})
						require.NoError(t, err)
						common.SetContextKey(ctx, constant.ContextKeyChannelOtherSetting, dto.ChannelOtherSettings{SillyTavernPreset: &dto.SillyTavernPresetConfig{Preset: preset, ScriptRuntime: "spreset-v1"}})
					}
					Relay(ctx, types.RelayFormatOpenAI)
					require.Equal(t, http.StatusBadRequest, recorder.Code, recorder.Body.String())
					require.Contains(t, recorder.Body.String(), tc.want)
					assert.NotContains(t, recorder.Body.String(), "PRIVATE EXCEPTION")
					var rows []model.Log
					require.NoError(t, db.Where("request_id = ?", "failure-"+tc.name).Find(&rows).Error)
					require.Len(t, rows, 1, "local failure must be visible without an upstream response")
					assert.Equal(t, model.LogTypeError, rows[0].Type)
					assert.Zero(t, rows[0].Quota)
					assert.NotContains(t, rows[0].Other, "PRIVATE EXCEPTION")
					if tc.script != "" {
						assert.Contains(t, rows[0].Other, `"hook":"FinalRequestProcessing"`)
					}
					if !tc.audit {
						return
					}
					audit, err := model.GetBodyAuditByRequestId("failure-" + tc.name)
					require.NoError(t, err)
					assert.Equal(t, recorder.Body.String(), string(audit.ClientResponseBody))
					assert.Zero(t, audit.ResponseStatus)
					assert.Empty(t, audit.RequestBody)
					assert.Empty(t, audit.ResponseBody)
					trace, attempts, blobs, err := model.GetAuditTraceBundleByRequestId("failure-" + tc.name)
					require.NoError(t, err)
					assert.Empty(t, attempts, "no phantom upstream attempt")
					assert.Equal(t, "not_sent", trace.ClientTerminalKind)
					assert.Equal(t, "failed", trace.Status)
					assert.Equal(t, http.StatusBadRequest, trace.ClientStatus)
					assert.Equal(t, tc.body, string(blobs[trace.OriginalRequestBlobId].Body))
					assert.Equal(t, recorder.Body.String(), string(blobs[trace.ClientResponseBlobId].Body))
					var before, after int64
					require.NoError(t, db.Model(&model.AuditBlob{}).Count(&before).Error)
					service.FinalizeBodyAuditClientResponse(ctx)
					require.NoError(t, db.Model(&model.AuditBlob{}).Count(&after).Error)
					assert.Equal(t, before, after, "finalization must not create duplicate captures")
				})
			}
			// The ordinary provider path has already written this error. The
			// terminal fallback must not duplicate that per-attempt log.
			ctx, _ := gin.CreateTestContext(httptest.NewRecorder())
			ctx.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
			ctx.Set("id", 7)
			ctx.Set(common.RequestIdKey, "failure-deduplicated")
			apiErr := types.NewErrorWithStatusCode(errors.New("upstream bad request"), types.ErrorCodeBadResponseStatusCode, http.StatusBadRequest)
			processChannelError(ctx, types.ChannelError{ChannelId: 101}, apiErr, nil)
			service.RecordRelayFailure(ctx, apiErr, nil)
			var count int64
			require.NoError(t, db.Model(&model.Log{}).Where("request_id = ?", "failure-deduplicated").Count(&count).Error)
			assert.Equal(t, int64(1), count)
		})
	}
}
