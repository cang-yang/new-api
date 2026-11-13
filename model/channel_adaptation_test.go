package model

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/mysql"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/schema"
)

func TestChannelAdaptationLifecycle(t *testing.T) {
	for _, dialect := range []string{"sqlite", "mysql", "postgres"} {
		t.Run(dialect, func(t *testing.T) {
			var driver gorm.Dialector
			switch dialect {
			case "sqlite":
				driver = sqlite.Open(filepath.Join(t.TempDir(), "adaptation.db"))
			case "mysql":
				dsn := os.Getenv("TEST_MYSQL_DSN")
				if dsn == "" {
					t.Skip("TEST_MYSQL_DSN is not configured")
				}
				driver = mysql.Open(dsn)
			case "postgres":
				dsn := os.Getenv("TEST_POSTGRES_DSN")
				if dsn == "" {
					t.Skip("TEST_POSTGRES_DSN is not configured")
				}
				driver = postgres.Open(dsn)
			}
			db, err := gorm.Open(driver, &gorm.Config{NamingStrategy: schema.NamingStrategy{TablePrefix: "adaptation_lifecycle_test_"}})
			require.NoError(t, err)
			oldDB, oldType := DB, common.MainDatabaseType()
			DB = db
			adaptationCache.Lock()
			clear(adaptationCache.entries)
			adaptationCache.order, adaptationCache.bytes = nil, 0
			adaptationCache.Unlock()
			common.SetMainDatabaseType(common.DatabaseType(dialect))
			initCol()
			sqlDB, err := db.DB()
			require.NoError(t, err)
			if dialect == "sqlite" {
				sqlDB.SetMaxOpenConns(1)
			}
			t.Cleanup(func() {
				DB = oldDB
				common.SetMainDatabaseType(oldType)
				initCol()
				require.NoError(t, db.Migrator().DropTable(&ChannelAdaptationVersion{}, &Channel{}, &Ability{}, &AuditAttempt{}, &AuditConfigSnapshot{}))
				require.NoError(t, sqlDB.Close())
			})
			var engineVersion string
			query := "select version()"
			if dialect == "sqlite" {
				query = "select sqlite_version()"
			}
			require.NoError(t, db.Raw(query).Scan(&engineVersion).Error)
			t.Logf("database=%s version=%s", dialect, engineVersion)
			require.NoError(t, db.AutoMigrate(&Channel{}, &Ability{}))
			// The released schema has all Channel columns except this new flag.
			// Populate it before upgrading; also exercise fresh version-table creation.
			require.NoError(t, db.Migrator().DropColumn(&Channel{}, "AdaptationManaged"))
			require.NoError(t, db.Model(&Channel{}).Omit("adaptation_managed").Create(&Channel{
				Id: 91001, Key: "test-secret-not-in-recovery", Name: "Existing channel",
				Models: "test-model", OtherSettings: `{"adaptation_scope":"all_supported","provider_custom":{"keep":true}}`,
			}).Error)
			for range 2 {
				require.NoError(t, db.AutoMigrate(&Channel{}, &ChannelAdaptationVersion{}))
			}
			var channel Channel
			require.NoError(t, db.First(&channel, 91001).Error)
			require.Equal(t, "test-secret-not-in-recovery", channel.Key)
			require.False(t, channel.AdaptationManaged)
			state, err := GetChannelAdaptationState(t.Context(), channel.Id)
			require.NoError(t, err)
			require.Zero(t, state.Revision)
			require.False(t, state.Checked)
			cachedChannel := channel
			unmanaged, err := FreezeChannelAdaptation(t.Context(), &cachedChannel)
			require.NoError(t, err)
			require.Equal(t, channel.OtherSettings, unmanaged.OtherSettings)

			change := func(action string, bundle AdaptationBundle, validate func(*Channel) error) {
				t.Helper()
				next, err := ChangeChannelAdaptation(t.Context(), channel.Id, ChannelAdaptationChange{
					Action: action, Revision: state.Revision, BaseDigest: state.BaseDigest, Bundle: bundle,
				}, validate)
				require.NoError(t, err)
				state = next
			}
			candidate := AdaptationBundle{"adaptation_scope": []byte(`"plain_chat_only"`)}
			change("save_draft", candidate, nil)
			require.False(t, state.Checked)
			require.JSONEq(t, `"all_supported"`, string(state.Active["adaptation_scope"]))
			_, err = ChangeChannelAdaptation(t.Context(), channel.Id, ChannelAdaptationChange{
				Action: "activate", Revision: state.Revision, BaseDigest: state.BaseDigest,
			}, nil)
			require.ErrorContains(t, err, "Check this draft again")
			change("check", nil, nil)
			require.True(t, state.Checked)
			stale := *state
			// Failure during final activation must not publish any part of the bundle.
			_, err = ChangeChannelAdaptation(t.Context(), channel.Id, ChannelAdaptationChange{
				Action: "activate", Revision: state.Revision, BaseDigest: state.BaseDigest,
			}, func(*Channel) error { return errors.New("candidate rejected") })
			require.ErrorContains(t, err, "candidate rejected")
			unchanged, err := GetChannelAdaptationState(t.Context(), channel.Id)
			require.NoError(t, err)
			assert.Equal(t, state, unchanged)
			change("activate", nil, nil)
			require.Nil(t, state.Draft)
			frozen, err := FreezeChannelAdaptation(t.Context(), &cachedChannel)
			require.NoError(t, err)
			require.Contains(t, frozen.OtherSettings, `"adaptation_scope":"plain_chat_only"`)
			require.Contains(t, cachedChannel.OtherSettings, `"adaptation_scope":"all_supported"`, "cached channels must never be mutated")
			require.Equal(t, cachedChannel.Key, frozen.Key)
			require.JSONEq(t, `"plain_chat_only"`, string(state.Active["adaptation_scope"]))
			require.JSONEq(t, `"all_supported"`, string(state.Previous["adaptation_scope"]))
			_, err = ChangeChannelAdaptation(t.Context(), channel.Id, ChannelAdaptationChange{
				Action: "save_draft", Revision: stale.Revision, BaseDigest: stale.BaseDigest, Bundle: candidate,
			}, nil)
			require.ErrorIs(t, err, ErrAdaptationConflict)
			// A stale ordinary channel save cannot bypass lifecycle checks.
			require.ErrorIs(t, channel.Update(), ErrAdaptationActivationRequired)
			require.NoError(t, db.First(&channel, channel.Id).Error)
			require.Contains(t, channel.OtherSettings, `"provider_custom":{"keep":true}`)
			require.Equal(t, "test-secret-not-in-recovery", channel.Key)
			change("mark_known_good", nil, nil)
			require.Equal(t, state.Active, state.KnownGood)
			change("restore_previous", nil, nil)
			require.JSONEq(t, `"all_supported"`, string(state.Draft["adaptation_scope"]))
			require.JSONEq(t, `"plain_chat_only"`, string(state.Active["adaptation_scope"]))
			change("check", nil, nil)
			// A successful check belongs to the saved model/provider context too.
			require.NoError(t, db.Model(&Channel{}).Where("id = ?", channel.Id).Update("models", "other-model").Error)
			reloaded, err := GetChannelAdaptationState(t.Context(), channel.Id)
			require.NoError(t, err)
			require.False(t, reloaded.Checked)
			state = reloaded
			change("check", nil, nil)
			change("activate", nil, nil)
			updatedSnapshot, err := FreezeChannelAdaptation(t.Context(), &cachedChannel)
			require.NoError(t, err)
			require.Contains(t, updatedSnapshot.OtherSettings, `"adaptation_scope":"all_supported"`)
			require.Contains(t, frozen.OtherSettings, `"adaptation_scope":"plain_chat_only"`, "in-flight requests retain the old complete bundle")
			repeatedSnapshot, err := FreezeChannelAdaptation(t.Context(), &cachedChannel)
			require.NoError(t, err)
			require.Equal(t, updatedSnapshot.OtherSettings, repeatedSnapshot.OtherSettings)
			change("restore_known_good", nil, nil)
			change("check", nil, nil)
			change("activate", nil, nil)
			require.JSONEq(t, `"plain_chat_only"`, string(state.Active["adaptation_scope"]))

			// Drafts can exceed MySQL TEXT's 64 KiB and still round-trip losslessly.
			book := &dto.WorldbookConfig{Enabled: false}
			data, err := common.Marshal(map[string]any{"enabled": book.Enabled, "future_field": strings.Repeat("background ", 10000), "precise_id": "9007199254740993"})
			require.NoError(t, err)
			change("save_draft", AdaptationBundle{"worldbook": data}, nil)
			require.JSONEq(t, string(data), string(state.Draft["worldbook"]))
			snapshot, err := common.Marshal(state)
			require.NoError(t, err)
			assert.NotContains(t, string(snapshot), "test-secret-not-in-recovery")
			assert.NotContains(t, string(snapshot), "provider_custom")

			// Two editors on the same revision: one succeeds, one conflicts.
			start := make(chan struct{})
			results := make(chan error, 2)
			var writers sync.WaitGroup
			for range 2 {
				writers.Go(func() {
					<-start
					_, err := ChangeChannelAdaptation(t.Context(), channel.Id, ChannelAdaptationChange{
						Action: "save_draft", Revision: state.Revision, BaseDigest: state.BaseDigest, Bundle: candidate,
					}, nil)
					results <- err
				})
			}
			close(start)
			writers.Wait()
			close(results)
			var succeeded, conflicted int
			for err := range results {
				if err == nil {
					succeeded++
				} else {
					require.ErrorIs(t, err, ErrAdaptationConflict)
					conflicted++
				}
			}
			assert.Equal(t, 1, succeeded)
			assert.Equal(t, 1, conflicted)

			// A slow model refresh started against the previous settings must
			// merge only its own fields, not roll back a newly activated preset.
			require.NoError(t, UpdateChannelModelDiscovery(&channel, dto.ChannelOtherSettings{}, false))
			var refreshed Channel
			require.NoError(t, db.First(&refreshed, channel.Id).Error)
			active, err := DecodeAdaptationBundle(refreshed.OtherSettings)
			require.NoError(t, err)
			require.Equal(t, state.Active, active)
			require.Contains(t, refreshed.OtherSettings, "provider_custom")
			// A bounded recent-record query must work on every supported SQL
			// dialect and never load response bodies or oversized snapshots.
			require.NoError(t, db.AutoMigrate(&AuditAttempt{}, &AuditConfigSnapshot{}))
			now := time.Now().UnixMilli()
			small := AuditConfigSnapshot{Kind: "attempt", Digest: "small", CanonicalJson: []byte(`{"sillytavern_preset":{}}`)}
			large := AuditConfigSnapshot{Kind: "attempt", Digest: "large", CanonicalJson: []byte(strings.Repeat("x", 129<<10))}
			require.NoError(t, db.Create(&small).Error)
			require.NoError(t, db.Create(&large).Error)
			for index := range 55 {
				row := AuditAttempt{TraceId: int64(index + 1), AttemptNo: 1, ChannelId: channel.Id, CreatedAt: now, CompletedAt: now, ConfigSnapshotId: small.Id, HTTPStatus: 200}
				if index == 54 {
					row.ConfigSnapshotId = large.Id
				}
				require.NoError(t, db.Create(&row).Error)
			}
			for index, row := range []AuditAttempt{
				{ChannelId: channel.Id, CreatedAt: now - int64(25*time.Hour/time.Millisecond), CompletedAt: now},
				{ChannelId: channel.Id + 1, CreatedAt: now, CompletedAt: now},
				{ChannelId: channel.Id, CreatedAt: now},
			} {
				row.TraceId, row.AttemptNo = int64(100+index), 1
				require.NoError(t, db.Create(&row).Error)
			}
			attempts, snapshots, err := RecentChannelAdaptationAudits(t.Context(), channel.Id)
			require.NoError(t, err)
			require.Len(t, attempts, 50)
			require.Equal(t, int64(55), attempts[0].Id)
			require.Len(t, snapshots, 1)
			require.Equal(t, small.CanonicalJson, snapshots[small.Id].CanonicalJson)
			_, err = BatchDeleteChannels([]int{channel.Id})
			require.NoError(t, err)
			var versions int64
			require.NoError(t, db.Model(&ChannelAdaptationVersion{}).Where("channel_id = ?", channel.Id).Count(&versions).Error)
			require.Zero(t, versions, "deleting a channel must remove its draft and recovery data")
		})
	}
}
