package model

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"gorm.io/gorm"
)

// Only adaptation settings belong to a recovery point. Credentials, routing,
// billing and provider connection settings must never be restored with it.
var adaptationSettingKeys = []string{"adaptation_scope", "sillytavern_preset", "worldbook", "response_text_filter"}

var ErrAdaptationConflict = errors.New("Adaptation settings changed. Reload before saving; your draft has not been applied.")
var ErrAdaptationActivationRequired = errors.New("Save adaptation changes as a draft, check them, then activate them.")

type AdaptationBundle map[string]json.RawMessage

// Strings deliberately use the dialect's unbounded default (longtext on MySQL,
// text on PostgreSQL/SQLite), not MySQL's 64 KiB TEXT.
type ChannelAdaptationVersion struct {
	ChannelID      int    `gorm:"primaryKey;autoIncrement:false"`
	Revision       int64  `gorm:"not null"`
	ActiveRevision int64  `gorm:"not null"`
	Draft          string `json:"-"`
	Previous       string `json:"-"`
	KnownGood      string `json:"-"`
	CheckedDigest  string `gorm:"size:64"`
}

type ChannelAdaptationState struct {
	Revision       int64            `json:"revision"`
	ActiveRevision int64            `json:"active_revision"`
	Active         AdaptationBundle `json:"active"`
	Draft          AdaptationBundle `json:"draft"`
	Previous       AdaptationBundle `json:"previous"`
	KnownGood      AdaptationBundle `json:"known_good"`
	Checked        bool             `json:"checked"`
	BaseDigest     string           `json:"base_digest"`
}

type ChannelAdaptationChange struct {
	Revision   int64            `json:"revision"`
	BaseDigest string           `json:"base_digest"`
	Action     string           `json:"action"`
	Bundle     AdaptationBundle `json:"bundle,omitempty"`
}

type adaptationCacheEntry struct {
	revision int64
	bundle   AdaptationBundle
	bytes    int
}

// This cache contains immutable bundles only, never credentials or loaded JS
// realms. Each process checks the small persisted active revision on admission.
var adaptationCache = struct {
	sync.Mutex
	entries map[int]adaptationCacheEntry
	order   []int
	bytes   int
}{entries: make(map[int]adaptationCacheEntry)}

// A bounded, body-free sample, not an all-traffic success-rate query. Audit
// tables live in the primary database even when usage logs use ClickHouse.
func RecentChannelAdaptationAudits(ctx context.Context, id int) ([]AuditAttempt, map[int64]AuditConfigSnapshot, error) {
	var attempts []AuditAttempt
	err := DB.WithContext(ctx).Select("id", "config_snapshot_id", "http_status", "error_code", "state", "completed_at").
		Where("channel_id = ? AND created_at >= ? AND completed_at > 0", id, time.Now().Add(-24*time.Hour).UnixMilli()).
		Order("id DESC").Limit(50).Find(&attempts).Error
	if err != nil {
		return nil, nil, err
	}
	ids := make([]int64, 0, len(attempts))
	for _, attempt := range attempts {
		if attempt.ConfigSnapshotId != 0 {
			ids = append(ids, attempt.ConfigSnapshotId)
		}
	}
	var snapshots []AuditConfigSnapshot
	if len(ids) > 0 {
		err = DB.WithContext(ctx).Select("id", "canonical_json").
			Where("id IN ? AND length(canonical_json) <= ?", ids, 128<<10).Find(&snapshots).Error
	}
	result := make(map[int64]AuditConfigSnapshot, len(snapshots))
	for _, snapshot := range snapshots {
		result[snapshot.Id] = snapshot
	}
	return attempts, result, err
}

// FreezeChannelAdaptation avoids transferring a multi-MiB preset for every
// request while preserving cross-instance activation. The revision read is the
// snapshot boundary; a later activation cannot mutate the returned channel.
func FreezeChannelAdaptation(ctx context.Context, channel *Channel) (*Channel, error) {
	var version ChannelAdaptationVersion
	read := DB.WithContext(ctx).Select("channel_id", "active_revision").
		Where("channel_id = ?", channel.Id).Limit(1).Find(&version)
	if read.Error != nil {
		return nil, read.Error
	}
	if read.RowsAffected == 0 {
		return channel, nil // Unmanaged settings use the existing channel cache.
	}
	var err error
	adaptationCache.Lock()
	entry, hit := adaptationCache.entries[channel.Id]
	adaptationCache.Unlock()
	if !hit || entry.revision != version.ActiveRevision {
		// Same lock order as activation. Reading settings and revision together
		// prevents caching the new bundle under the previous revision.
		err = DB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
			var current Channel
			if err := lockForUpdate(tx).Select("id", "settings").First(&current, channel.Id).Error; err != nil {
				return err
			}
			if err := tx.Select("channel_id", "active_revision").Where("channel_id = ?", channel.Id).Take(&version).Error; err != nil {
				return err
			}
			bundle, err := DecodeAdaptationBundle(current.OtherSettings)
			if err != nil {
				return err
			}
			data, err := common.Marshal(bundle)
			if err != nil {
				return err
			}
			entry = adaptationCacheEntry{revision: version.ActiveRevision, bundle: bundle, bytes: len(data)}
			return nil
		})
		if err != nil {
			return nil, err
		}
		adaptationCache.Lock()
		if previous, ok := adaptationCache.entries[channel.Id]; ok {
			adaptationCache.bytes -= previous.bytes
			adaptationCache.order = slices.DeleteFunc(adaptationCache.order, func(id int) bool { return id == channel.Id })
		}
		adaptationCache.entries[channel.Id] = entry
		adaptationCache.order = append(adaptationCache.order, channel.Id)
		adaptationCache.bytes += entry.bytes
		for len(adaptationCache.order) > 32 || adaptationCache.bytes > 64<<20 {
			oldest := adaptationCache.order[0]
			adaptationCache.order = adaptationCache.order[1:]
			adaptationCache.bytes -= adaptationCache.entries[oldest].bytes
			delete(adaptationCache.entries, oldest)
		}
		adaptationCache.Unlock()
	}
	selected := *channel
	selected.OtherSettings, err = entry.bundle.Apply(channel.OtherSettings)
	return &selected, err
}

func DecodeAdaptationBundle(settings string) (AdaptationBundle, error) {
	var values map[string]json.RawMessage
	if settings != "" {
		if err := common.UnmarshalJsonStr(settings, &values); err != nil {
			return nil, fmt.Errorf("invalid channel settings: %w", err)
		}
	}
	bundle := make(AdaptationBundle)
	for _, key := range adaptationSettingKeys {
		if value, ok := values[key]; ok {
			bundle[key] = value
		}
	}
	return bundle, nil
}

func (bundle AdaptationBundle) Apply(settings string) (string, error) {
	values := make(map[string]json.RawMessage)
	if settings != "" {
		if err := common.UnmarshalJsonStr(settings, &values); err != nil {
			return "", err
		}
		if values == nil {
			values = make(map[string]json.RawMessage)
		}
	}
	for key := range bundle {
		if !slices.Contains(adaptationSettingKeys, key) {
			return "", fmt.Errorf("unsupported adaptation setting: %s", key)
		}
	}
	for _, key := range adaptationSettingKeys {
		delete(values, key)
		if value, ok := bundle[key]; ok {
			values[key] = value
		}
	}
	data, err := common.Marshal(values)
	return string(data), err
}

// Canonical JSON avoids spurious conflicts from whitespace and object ordering.
func adaptationDigest(value any) (string, error) {
	data, err := common.Marshal(value)
	if err != nil {
		return "", err
	}
	var canonical any
	if err := common.UnmarshalPreservingNumbers(data, &canonical); err != nil {
		return "", err
	}
	data, err = common.Marshal(canonical)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:]), nil
}

func adaptationCheckDigest(channel *Channel, bundle AdaptationBundle) (string, error) {
	// A check expires when its provider/model/override context changes. Secrets
	// are neither persisted in recovery points nor included in their export.
	var settings dto.ChannelOtherSettings
	if channel.OtherSettings != "" {
		if err := common.UnmarshalJsonStr(channel.OtherSettings, &settings); err != nil {
			return "", err
		}
	}
	return adaptationDigest([]any{bundle, channel.Type, channel.BaseURL, channel.Models, channel.ModelMapping,
		channel.ParamOverride, channel.Setting, settings.ToolLossPolicy, settings.AdvancedCustom})
}

func adaptationState(channel *Channel, version *ChannelAdaptationVersion) (*ChannelAdaptationState, error) {
	active, err := DecodeAdaptationBundle(channel.OtherSettings)
	if err != nil {
		return nil, err
	}
	state := &ChannelAdaptationState{Revision: version.Revision, ActiveRevision: version.ActiveRevision, Active: active}
	state.BaseDigest, err = adaptationDigest(active)
	if err != nil {
		return nil, err
	}
	for _, field := range []struct {
		source string
		target *AdaptationBundle
	}{{version.Draft, &state.Draft}, {version.Previous, &state.Previous}, {version.KnownGood, &state.KnownGood}} {
		if field.source != "" {
			if err := common.UnmarshalJsonStr(field.source, field.target); err != nil {
				return nil, err
			}
		}
	}
	if version.Draft != "" && version.CheckedDigest != "" {
		digest, err := adaptationCheckDigest(channel, state.Draft)
		if err != nil {
			return nil, err
		}
		state.Checked = digest == version.CheckedDigest
	}
	return state, nil
}

func GetChannelAdaptationState(ctx context.Context, id int) (*ChannelAdaptationState, error) {
	var result *ChannelAdaptationState
	err := DB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var channel Channel
		if err := lockForUpdate(tx).First(&channel, id).Error; err != nil {
			return err
		}
		version := ChannelAdaptationVersion{ChannelID: id}
		if channel.AdaptationManaged {
			if err := tx.First(&version, "channel_id = ?", id).Error; err != nil {
				return err
			}
		}
		var err error
		result, err = adaptationState(&channel, &version)
		return err
	})
	return result, err
}

// Validate is bounded and local: it must not download modules or call a model.
// Every writer locks the same channel row; revision checks also protect clients
// with stale tabs. Active settings and recovery snapshots commit together.
func ChangeChannelAdaptation(ctx context.Context, id int, change ChannelAdaptationChange, validate func(*Channel) error) (*ChannelAdaptationState, error) {
	var result *ChannelAdaptationState
	err := DB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var channel Channel
		if err := lockForUpdate(tx).First(&channel, id).Error; err != nil {
			return err
		}
		version := ChannelAdaptationVersion{ChannelID: id}
		if channel.AdaptationManaged {
			if err := tx.First(&version, "channel_id = ?", id).Error; err != nil {
				return err
			}
		}
		current, err := adaptationState(&channel, &version)
		if err != nil {
			return err
		}
		if current.Revision != change.Revision || current.BaseDigest != change.BaseDigest {
			return ErrAdaptationConflict
		}
		switch change.Action {
		case "save_draft":
			if change.Bundle == nil {
				return errors.New("An adaptation draft is required.")
			}
			if _, err := change.Bundle.Apply("{}"); err != nil {
				return err
			}
			data, err := common.Marshal(change.Bundle)
			if err != nil {
				return err
			}
			version.Draft, version.CheckedDigest = string(data), ""
		case "check", "activate":
			if current.Draft == nil {
				return errors.New("Save an adaptation draft before checking or activating it.")
			}
			digest, err := adaptationCheckDigest(&channel, current.Draft)
			if err != nil {
				return err
			}
			if change.Action == "activate" && (!current.Checked || digest != version.CheckedDigest) {
				return errors.New("Check this draft again before activating it.")
			}
			candidate := channel
			candidate.OtherSettings, err = current.Draft.Apply(channel.OtherSettings)
			if err != nil {
				return err
			}
			if err := candidate.ValidateSettings(); err != nil {
				return err
			}
			if validate != nil {
				if err := validate(&candidate); err != nil {
					return err
				}
			}
			version.CheckedDigest = digest
			if change.Action == "activate" {
				data, err := common.Marshal(current.Active)
				if err != nil {
					return err
				}
				version.Previous = string(data)
				version.Draft, version.CheckedDigest = "", ""
				version.ActiveRevision = version.Revision + 1
				channel.OtherSettings = candidate.OtherSettings
			}
		case "restore_previous", "restore_known_good":
			snapshot := version.Previous
			if change.Action == "restore_known_good" {
				snapshot = version.KnownGood
			}
			if snapshot == "" {
				return errors.New("No recovery point is available.")
			}
			// Restoring is a candidate, never an unchecked live replacement.
			version.Draft, version.CheckedDigest = snapshot, ""
		case "mark_known_good":
			data, err := common.Marshal(current.Active)
			if err != nil {
				return err
			}
			version.KnownGood = string(data)
		default:
			return errors.New("Invalid adaptation action.")
		}
		version.Revision++
		if channel.AdaptationManaged {
			updated := tx.Model(&ChannelAdaptationVersion{}).
				Where("channel_id = ? AND revision = ?", id, change.Revision).
				Select("*").Updates(&version)
			if updated.Error != nil {
				return updated.Error
			}
			if updated.RowsAffected != 1 {
				return ErrAdaptationConflict
			}
		} else if err := tx.Create(&version).Error; err != nil {
			return err
		}
		if err := tx.Model(&Channel{}).Where("id = ?", id).Updates(map[string]any{
			"settings": channel.OtherSettings, "adaptation_managed": true,
		}).Error; err != nil {
			return err
		}
		result, err = adaptationState(&channel, &version)
		return err
	})
	return result, err
}

// Ordinary channel saves cannot overwrite a managed adaptation, including when
// a stale editor races an activation. Non-adaptation settings remain editable.
func guardManagedAdaptationUpdate(current, update *Channel) error {
	if update.OtherSettings == "" {
		return nil
	}
	oldBundle, err := DecodeAdaptationBundle(current.OtherSettings)
	if err != nil {
		return err
	}
	newBundle, err := DecodeAdaptationBundle(update.OtherSettings)
	if err != nil {
		return err
	}
	before, err := adaptationDigest(oldBundle)
	if err != nil {
		return err
	}
	after, err := adaptationDigest(newBundle)
	if err != nil {
		return err
	}
	if before != after {
		return ErrAdaptationActivationRequired
	}
	return nil
}

// Background model discovery owns only its prefixed settings. Merging under
// the same row lock avoids reverting an adaptation activated during a slow
// upstream model-list request.
func UpdateChannelModelDiscovery(channel *Channel, settings dto.ChannelOtherSettings, updateModels bool) error {
	return DB.Transaction(func(tx *gorm.DB) error {
		var current Channel
		if err := lockForUpdate(tx).First(&current, channel.Id).Error; err != nil {
			return err
		}
		values := make(map[string]json.RawMessage)
		if current.OtherSettings != "" {
			if err := common.UnmarshalJsonStr(current.OtherSettings, &values); err != nil {
				return err
			}
		}
		if values == nil {
			values = make(map[string]json.RawMessage)
		}
		data, err := common.Marshal(settings)
		if err != nil {
			return err
		}
		var discovery map[string]json.RawMessage
		if err := common.Unmarshal(data, &discovery); err != nil {
			return err
		}
		for key := range values {
			if strings.HasPrefix(key, "upstream_model_update_") {
				delete(values, key)
			}
		}
		for key, value := range discovery {
			if strings.HasPrefix(key, "upstream_model_update_") {
				values[key] = value
			}
		}
		data, err = common.Marshal(values)
		if err != nil {
			return err
		}
		updates := map[string]any{"settings": string(data)}
		if updateModels {
			updates["models"] = channel.Models
		}
		if err := tx.Model(&current).Updates(updates).Error; err != nil {
			return err
		}
		channel.OtherSettings = string(data)
		return nil
	})
}
