package model

import (
	"time"

	"gorm.io/gorm/clause"
)

// BodyAudit stores request and response payloads separately from usage logs so
// normal log list queries stay small and fast. RequestId links the row to Log.
type BodyAudit struct {
	Id                    int64  `json:"id" gorm:"primaryKey;autoIncrement"`
	RequestId             string `json:"request_id" gorm:"size:64;uniqueIndex;not null"`
	CreatedAt             int64  `json:"created_at" gorm:"bigint;index"`
	UpdatedAt             int64  `json:"updated_at" gorm:"bigint"`
	UserId                int    `json:"user_id" gorm:"index"`
	ChannelId             int    `json:"channel_id" gorm:"index"`
	ModelName             string `json:"model_name" gorm:"size:256"`
	RequestBody           []byte `json:"-"`
	RequestBodySize       int64  `json:"request_body_size"`
	RequestBodyTruncated  bool   `json:"request_body_truncated"`
	ResponseBody          []byte `json:"-"`
	ResponseBodySize      int64  `json:"response_body_size"`
	ResponseBodyTruncated bool   `json:"response_body_truncated"`
	ResponseStatus        int    `json:"response_status"`
	ResponseContentType   string `json:"response_content_type" gorm:"size:256"`
	ResponseComplete      bool   `json:"response_complete"`
}

func UpsertBodyAudit(audit *BodyAudit) error {
	now := time.Now().Unix()
	if audit.CreatedAt == 0 {
		audit.CreatedAt = now
	}
	audit.UpdatedAt = now
	return DB.Clauses(clause.OnConflict{
		Columns: []clause.Column{{Name: "request_id"}},
		DoUpdates: clause.AssignmentColumns([]string{
			"updated_at",
			"user_id",
			"channel_id",
			"model_name",
			"request_body",
			"request_body_size",
			"request_body_truncated",
			"response_body",
			"response_body_size",
			"response_body_truncated",
			"response_status",
			"response_content_type",
			"response_complete",
		}),
	}).Create(audit).Error
}

func GetBodyAuditByRequestId(requestId string) (*BodyAudit, error) {
	var audit BodyAudit
	if err := DB.Where("request_id = ?", requestId).First(&audit).Error; err != nil {
		return nil, err
	}
	return &audit, nil
}

func DeleteBodyAuditsBefore(timestamp int64) error {
	return DB.Where("created_at < ?", timestamp).Delete(&BodyAudit{}).Error
}
