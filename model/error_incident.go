package model

import (
	"errors"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

var ErrErrorIncidentNotFound = errors.New("error incident not found")

// ErrorIncident is a privacy-minimal aggregation of repeated channel failures.
// It intentionally contains no prompt, response body, or full error message.
type ErrorIncident struct {
	Id            int64  `json:"id" gorm:"primaryKey;autoIncrement"`
	Fingerprint   string `json:"fingerprint" gorm:"type:varchar(64);uniqueIndex;not null"`
	FirstSeenAt   int64  `json:"first_seen_at" gorm:"bigint;index;not null"`
	LastSeenAt    int64  `json:"last_seen_at" gorm:"bigint;index;not null"`
	Count         int64  `json:"count" gorm:"bigint;not null;default:1"`
	LastRequestId string `json:"last_request_id" gorm:"type:varchar(64);index;default:''"`
	StatusCode    int    `json:"status_code" gorm:"index;not null;default:0"`
	ErrorType     string `json:"error_type" gorm:"type:varchar(128);default:''"`
	ErrorCode     string `json:"error_code" gorm:"type:varchar(128);index;default:''"`
	ChannelType   int    `json:"channel_type" gorm:"index;not null;default:0"`
	Model         string `json:"model" gorm:"type:varchar(191);index;default:''"`
	Path          string `json:"path" gorm:"type:varchar(255);default:''"`
	ResolvedAt    *int64 `json:"resolved_at,omitempty" gorm:"bigint"`
}

type ErrorIncidentOccurrence struct {
	Fingerprint string
	RequestId   string
	StatusCode  int
	ErrorType   string
	ErrorCode   string
	ChannelType int
	Model       string
	Path        string
	SeenAt      int64
}

// UpsertErrorIncident atomically increments the incident counter. A repeated
// failure reopens a previously resolved incident.
func UpsertErrorIncident(occurrence ErrorIncidentOccurrence) (*ErrorIncident, error) {
	seenAt := occurrence.SeenAt
	if seenAt == 0 {
		seenAt = time.Now().Unix()
	}
	incident := &ErrorIncident{
		Fingerprint: occurrence.Fingerprint, FirstSeenAt: seenAt, LastSeenAt: seenAt,
		Count: 1, LastRequestId: occurrence.RequestId, StatusCode: occurrence.StatusCode,
		ErrorType: occurrence.ErrorType, ErrorCode: occurrence.ErrorCode,
		ChannelType: occurrence.ChannelType, Model: occurrence.Model, Path: occurrence.Path,
	}
	err := DB.Clauses(clause.OnConflict{
		Columns: []clause.Column{{Name: "fingerprint"}},
		DoUpdates: clause.Assignments(map[string]interface{}{
			"last_seen_at": seenAt, "count": gorm.Expr("count + ?", 1),
			"last_request_id": occurrence.RequestId, "status_code": occurrence.StatusCode,
			"error_type": occurrence.ErrorType, "error_code": occurrence.ErrorCode,
			"channel_type": occurrence.ChannelType, "model": occurrence.Model,
			"path": occurrence.Path, "resolved_at": nil,
		}),
	}).Create(incident).Error
	if err != nil {
		return nil, err
	}
	err = DB.Where("fingerprint = ?", occurrence.Fingerprint).First(incident).Error
	return incident, err
}

func ListErrorIncidents(offset, limit int, unresolvedOnly bool) ([]ErrorIncident, int64, error) {
	if limit <= 0 || limit > 100 {
		limit = 20
	}
	tx := DB.Model(&ErrorIncident{})
	if unresolvedOnly {
		tx = tx.Where("resolved_at IS NULL")
	}
	var total int64
	if err := tx.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	var incidents []ErrorIncident
	err := tx.Order("last_seen_at DESC, id DESC").Offset(offset).Limit(limit).Find(&incidents).Error
	return incidents, total, err
}

func GetErrorIncident(fingerprint string) (*ErrorIncident, error) {
	var incident ErrorIncident
	err := DB.Where("fingerprint = ?", fingerprint).First(&incident).Error
	return &incident, err
}

func SetErrorIncidentResolved(fingerprint string, resolved bool) error {
	var value interface{}
	if resolved {
		now := time.Now().Unix()
		value = now
	}
	result := DB.Model(&ErrorIncident{}).Where("fingerprint = ?", fingerprint).Update("resolved_at", value)
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected == 0 {
		return ErrErrorIncidentNotFound
	}
	return nil
}

// ListErrorIncidentSamples returns log metadata only. Content and Other are
// cleared so this endpoint cannot accidentally expose prompts or error bodies.
func ListErrorIncidentSamples(fingerprint string, limit int) ([]Log, error) {
	if limit <= 0 || limit > 20 {
		limit = 10
	}
	var logs []Log
	err := LOG_DB.Where("type = ? AND other LIKE ?", LogTypeError, "%\"error_fingerprint\":\""+fingerprint+"\"%").
		Order("created_at DESC").Limit(limit).Find(&logs).Error
	for i := range logs {
		logs[i].Content = ""
		logs[i].Other = ""
		logs[i].Ip = ""
		logs[i].TokenName = ""
	}
	return logs, err
}
