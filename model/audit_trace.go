package model

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// AuditTrace is the request-level record. It owns the original client request,
// the final client response, and an ordered collection of upstream attempts.
// Payloads live in AuditBlob so the hot metadata rows stay small.
type AuditTrace struct {
	Id                    int64  `json:"id" gorm:"primaryKey;autoIncrement"`
	RequestId             string `json:"request_id" gorm:"size:64;uniqueIndex;not null"`
	CreatedAt             int64  `json:"created_at" gorm:"bigint;index"`
	UpdatedAt             int64  `json:"updated_at" gorm:"bigint"`
	CompletedAt           int64  `json:"completed_at" gorm:"bigint;index"`
	Source                string `json:"source" gorm:"size:32;index"`
	Status                string `json:"status" gorm:"size:32;index"`
	UserId                int    `json:"user_id" gorm:"index"`
	TokenId               int    `json:"token_id" gorm:"index"`
	Method                string `json:"method" gorm:"size:16"`
	Route                 string `json:"route" gorm:"size:256"`
	RequestModel          string `json:"request_model" gorm:"size:256;index"`
	RelayFormat           string `json:"relay_format" gorm:"size:64"`
	OriginalRequestBlobId int64  `json:"original_request_blob_id" gorm:"index"`
	PolicySnapshotId      int64  `json:"policy_snapshot_id" gorm:"index"`
	FinalAttemptId        int64  `json:"final_attempt_id" gorm:"index"`
	ClientResponseBlobId  int64  `json:"client_response_blob_id" gorm:"index"`
	ClientStatus          int    `json:"client_status"`
	ClientTerminalKind    string `json:"client_terminal_kind" gorm:"size:64;index"`
	Outcome               string `json:"outcome" gorm:"size:32;index"`
	ReplayOfTraceId       int64  `json:"replay_of_trace_id" gorm:"index"`
}

// AuditAttempt is one application-level channel try. AttemptNo is independent
// from routing internals and cannot be reused within a trace.
type AuditAttempt struct {
	Id                    int64  `json:"id" gorm:"primaryKey;autoIncrement"`
	TraceId               int64  `json:"trace_id" gorm:"not null;uniqueIndex:idx_audit_attempt_trace_no;index"`
	AttemptNo             int    `json:"attempt_no" gorm:"not null;uniqueIndex:idx_audit_attempt_trace_no"`
	CreatedAt             int64  `json:"created_at" gorm:"bigint;index"`
	UpdatedAt             int64  `json:"updated_at" gorm:"bigint"`
	StartedAt             int64  `json:"started_at" gorm:"bigint"`
	CompletedAt           int64  `json:"completed_at" gorm:"bigint;index"`
	RoutingRetryIndex     int    `json:"routing_retry_index"`
	ChannelId             int    `json:"channel_id" gorm:"index"`
	ChannelType           int    `json:"channel_type"`
	ChannelName           string `json:"channel_name" gorm:"size:256"`
	RequestModel          string `json:"request_model" gorm:"size:256"`
	UpstreamModel         string `json:"upstream_model" gorm:"size:256;index"`
	RequestFormat         string `json:"request_format" gorm:"size:64"`
	UpstreamFormat        string `json:"upstream_format" gorm:"size:64"`
	ConfigSnapshotId      int64  `json:"config_snapshot_id" gorm:"index"`
	RequestBlobId         int64  `json:"request_blob_id" gorm:"index"`
	ResponseBlobId        int64  `json:"response_blob_id" gorm:"index"`
	Method                string `json:"method" gorm:"size:16"`
	Target                string `json:"target" gorm:"size:1024"`
	SanitizedHeadersJson  []byte `json:"-"`
	UpstreamRequestId     string `json:"upstream_request_id" gorm:"size:256;index"`
	State                 string `json:"state" gorm:"size:32;index"`
	Outcome               string `json:"outcome" gorm:"size:32;index"`
	HTTPStatus            int    `json:"http_status" gorm:"index"`
	ErrorCode             string `json:"error_code" gorm:"size:128;index"`
	ErrorMessage          string `json:"error_message" gorm:"type:text"`
	RetryAction           string `json:"retry_action" gorm:"size:32"`
	TerminalKind          string `json:"terminal_kind" gorm:"size:64"`
	Complete              bool   `json:"complete"`
	DurationMs            int64  `json:"duration_ms"`
	ReceivedEventCount    int    `json:"received_event_count"`
	MeaningfulOutputCount int    `json:"meaningful_output_count"`
	DownstreamStarted     bool   `json:"downstream_started"`
}

// AuditWireSend represents a physical net/http transmission. A transport may
// resend one AuditAttempt (for example after an authentication handshake).
type AuditWireSend struct {
	Id             int64  `json:"id" gorm:"primaryKey;autoIncrement"`
	AttemptId      int64  `json:"attempt_id" gorm:"not null;uniqueIndex:idx_audit_wire_send_no;index"`
	SendNo         int    `json:"send_no" gorm:"not null;uniqueIndex:idx_audit_wire_send_no"`
	CreatedAt      int64  `json:"created_at" gorm:"bigint;index"`
	CompletedAt    int64  `json:"completed_at" gorm:"bigint"`
	RequestBlobId  int64  `json:"request_blob_id" gorm:"index"`
	ResponseBlobId int64  `json:"response_blob_id" gorm:"index"`
	HTTPStatus     int    `json:"http_status"`
	ErrorCode      string `json:"error_code" gorm:"size:128"`
	DurationMs     int64  `json:"duration_ms"`
}

// AuditConfigSnapshot is immutable. Digest is computed from canonical JSON and
// scoped by Kind, allowing safe deduplication without storing channel secrets.
type AuditConfigSnapshot struct {
	Id            int64  `json:"id" gorm:"primaryKey;autoIncrement"`
	Kind          string `json:"kind" gorm:"size:32;not null;uniqueIndex:idx_audit_snapshot_kind_digest"`
	Digest        string `json:"digest" gorm:"size:64;not null;uniqueIndex:idx_audit_snapshot_kind_digest"`
	SchemaVersion int    `json:"schema_version"`
	CanonicalJson []byte `json:"-"`
	CreatedAt     int64  `json:"created_at" gorm:"bigint;index"`
}

// AuditBlob is an immutable captured payload. StorageBackend is "db" today;
// ObjectKey leaves an additive path to filesystem/object storage later.
type AuditBlob struct {
	Id             int64  `json:"id" gorm:"primaryKey;autoIncrement"`
	Digest         string `json:"digest" gorm:"size:64;index"`
	StorageBackend string `json:"storage_backend" gorm:"size:16"`
	ObjectKey      string `json:"object_key" gorm:"size:1024"`
	CaptureStage   string `json:"capture_stage" gorm:"size:64;index"`
	MediaType      string `json:"media_type" gorm:"size:256"`
	Body           []byte `json:"-"`
	OriginalSize   int64  `json:"original_size"`
	StoredSize     int64  `json:"stored_size"`
	Complete       bool   `json:"complete"`
	Truncated      bool   `json:"truncated"`
	CreatedAt      int64  `json:"created_at" gorm:"bigint;index"`
}

func nowMillis() int64 { return time.Now().UnixMilli() }

func CreateAuditTrace(trace *AuditTrace) error {
	if trace.CreatedAt == 0 {
		trace.CreatedAt = nowMillis()
	}
	trace.UpdatedAt = trace.CreatedAt
	return DB.Create(trace).Error
}

func GetOrCreateAuditTrace(trace *AuditTrace) (*AuditTrace, error) {
	if trace.RequestId == "" {
		return nil, errors.New("audit trace request id is required")
	}
	if trace.CreatedAt == 0 {
		trace.CreatedAt = nowMillis()
	}
	trace.UpdatedAt = trace.CreatedAt
	result := DB.Clauses(clause.OnConflict{
		Columns:   []clause.Column{{Name: "request_id"}},
		DoNothing: true,
	}).Create(trace)
	if result.Error != nil {
		return nil, result.Error
	}
	if result.RowsAffected == 0 {
		if err := DB.Where("request_id = ?", trace.RequestId).First(trace).Error; err != nil {
			return nil, err
		}
	}
	return trace, nil
}

func GetAuditTraceByRequestId(requestId string) (*AuditTrace, error) {
	var trace AuditTrace
	if err := DB.Where("request_id = ?", requestId).First(&trace).Error; err != nil {
		return nil, err
	}
	return &trace, nil
}

func CreateAuditAttempt(attempt *AuditAttempt) error {
	if attempt.CreatedAt == 0 {
		attempt.CreatedAt = nowMillis()
	}
	attempt.UpdatedAt = attempt.CreatedAt
	return DB.Create(attempt).Error
}

// CreateNextAuditAttempt allocates an application attempt number independently
// from the router's retry counter. Relay execution is sequential for one trace;
// the unique index remains the final guard against accidental reuse.
func CreateNextAuditAttempt(attempt *AuditAttempt) error {
	return DB.Transaction(func(tx *gorm.DB) error {
		var next int
		if err := tx.Model(&AuditAttempt{}).
			Select("COALESCE(MAX(attempt_no), -1) + 1").
			Where("trace_id = ?", attempt.TraceId).
			Scan(&next).Error; err != nil {
			return err
		}
		attempt.AttemptNo = next
		if attempt.CreatedAt == 0 {
			attempt.CreatedAt = nowMillis()
		}
		attempt.UpdatedAt = attempt.CreatedAt
		return tx.Create(attempt).Error
	})
}

func UpdateAuditAttemptCapture(attemptId, requestBlobId, responseBlobId int64, state string, httpStatus int, complete bool, terminalKind string) error {
	result := DB.Model(&AuditAttempt{}).Where("id = ?", attemptId).Updates(map[string]any{
		"updated_at":       nowMillis(),
		"completed_at":     nowMillis(),
		"request_blob_id":  requestBlobId,
		"response_blob_id": responseBlobId,
		"state":            state,
		"http_status":      httpStatus,
		"complete":         complete,
		"terminal_kind":    terminalKind,
	})
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected == 0 {
		return gorm.ErrRecordNotFound
	}
	return nil
}

func ListAuditAttempts(traceId int64) ([]AuditAttempt, error) {
	var attempts []AuditAttempt
	err := DB.Where("trace_id = ?", traceId).Order("attempt_no ASC").Find(&attempts).Error
	return attempts, err
}

func GetAuditTraceBundleByRequestId(requestId string) (*AuditTrace, []AuditAttempt, map[int64]AuditBlob, error) {
	trace, err := GetAuditTraceByRequestId(requestId)
	if err != nil {
		return nil, nil, nil, err
	}
	attempts, err := ListAuditAttempts(trace.Id)
	if err != nil {
		return nil, nil, nil, err
	}
	blobIds := make([]int64, 0, len(attempts)*2+2)
	appendBlobId := func(id int64) {
		if id != 0 {
			blobIds = append(blobIds, id)
		}
	}
	appendBlobId(trace.OriginalRequestBlobId)
	appendBlobId(trace.ClientResponseBlobId)
	for _, attempt := range attempts {
		appendBlobId(attempt.RequestBlobId)
		appendBlobId(attempt.ResponseBlobId)
	}
	blobs := make(map[int64]AuditBlob, len(blobIds))
	if len(blobIds) > 0 {
		var rows []AuditBlob
		if err := DB.Where("id IN ?", blobIds).Find(&rows).Error; err != nil {
			return nil, nil, nil, err
		}
		for _, blob := range rows {
			blobs[blob.Id] = blob
		}
	}
	return trace, attempts, blobs, nil
}

func CreateAuditWireSend(send *AuditWireSend) error {
	if send.CreatedAt == 0 {
		send.CreatedAt = nowMillis()
	}
	return DB.Create(send).Error
}

func CreateAuditBlob(blob *AuditBlob) (*AuditBlob, error) {
	if blob.StorageBackend == "" {
		blob.StorageBackend = "db"
	}
	if blob.OriginalSize == 0 && len(blob.Body) > 0 {
		blob.OriginalSize = int64(len(blob.Body))
	}
	blob.StoredSize = int64(len(blob.Body))
	if blob.Digest == "" {
		sum := sha256.Sum256(blob.Body)
		blob.Digest = hex.EncodeToString(sum[:])
	}
	if blob.CreatedAt == 0 {
		blob.CreatedAt = nowMillis()
	}
	return blob, DB.Create(blob).Error
}

func canonicalJSON(payload []byte) ([]byte, error) {
	var value any
	if err := json.Unmarshal(payload, &value); err != nil {
		return nil, err
	}
	return json.Marshal(value)
}

func GetOrCreateAuditConfigSnapshot(kind string, payload []byte) (*AuditConfigSnapshot, error) {
	canonical, err := canonicalJSON(payload)
	if err != nil {
		return nil, err
	}
	sum := sha256.Sum256(canonical)
	snapshot := &AuditConfigSnapshot{
		Kind:          kind,
		Digest:        hex.EncodeToString(sum[:]),
		SchemaVersion: 1,
		CanonicalJson: canonical,
		CreatedAt:     nowMillis(),
	}
	result := DB.Clauses(clause.OnConflict{
		Columns:   []clause.Column{{Name: "kind"}, {Name: "digest"}},
		DoNothing: true,
	}).Create(snapshot)
	if result.Error != nil {
		return nil, result.Error
	}
	if result.RowsAffected == 0 {
		if err := DB.Where("kind = ? AND digest = ?", kind, snapshot.Digest).First(snapshot).Error; err != nil {
			return nil, err
		}
	}
	return snapshot, nil
}

func FinalizeAuditTrace(traceId, finalAttemptId, clientResponseBlobId int64, status, terminalKind string) error {
	result := DB.Model(&AuditTrace{}).Where("id = ?", traceId).Updates(map[string]any{
		"updated_at":              nowMillis(),
		"completed_at":            nowMillis(),
		"final_attempt_id":        finalAttemptId,
		"client_response_blob_id": clientResponseBlobId,
		"status":                  status,
		"client_terminal_kind":    terminalKind,
	})
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected == 0 {
		return gorm.ErrRecordNotFound
	}
	return nil
}

func GetAuditBlob(id int64) (*AuditBlob, error) {
	var blob AuditBlob
	if err := DB.First(&blob, id).Error; err != nil {
		return nil, err
	}
	return &blob, nil
}

func DeleteAuditTrace(traceId int64) error {
	return DB.Transaction(func(tx *gorm.DB) error {
		var attempts []AuditAttempt
		if err := tx.Where("trace_id = ?", traceId).Find(&attempts).Error; err != nil {
			return err
		}
		for _, attempt := range attempts {
			if err := tx.Where("attempt_id = ?", attempt.Id).Delete(&AuditWireSend{}).Error; err != nil {
				return err
			}
		}
		if err := tx.Where("trace_id = ?", traceId).Delete(&AuditAttempt{}).Error; err != nil {
			return err
		}
		result := tx.Delete(&AuditTrace{}, traceId)
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected == 0 {
			return errors.New("audit trace not found")
		}
		return nil
	})
}

func DeleteAuditTracesBefore(timestamp int64) error {
	return DB.Transaction(func(tx *gorm.DB) error {
		var traces []AuditTrace
		if err := tx.Where("created_at < ?", timestamp).Find(&traces).Error; err != nil {
			return err
		}
		if len(traces) == 0 {
			return nil
		}
		traceIds := make([]int64, 0, len(traces))
		blobIds := make([]int64, 0, len(traces)*3)
		for _, trace := range traces {
			traceIds = append(traceIds, trace.Id)
			if trace.OriginalRequestBlobId != 0 {
				blobIds = append(blobIds, trace.OriginalRequestBlobId)
			}
			if trace.ClientResponseBlobId != 0 {
				blobIds = append(blobIds, trace.ClientResponseBlobId)
			}
		}
		var attempts []AuditAttempt
		if err := tx.Where("trace_id IN ?", traceIds).Find(&attempts).Error; err != nil {
			return err
		}
		attemptIds := make([]int64, 0, len(attempts))
		for _, attempt := range attempts {
			attemptIds = append(attemptIds, attempt.Id)
			if attempt.RequestBlobId != 0 {
				blobIds = append(blobIds, attempt.RequestBlobId)
			}
			if attempt.ResponseBlobId != 0 {
				blobIds = append(blobIds, attempt.ResponseBlobId)
			}
		}
		if len(attemptIds) > 0 {
			if err := tx.Where("attempt_id IN ?", attemptIds).Delete(&AuditWireSend{}).Error; err != nil {
				return err
			}
		}
		if err := tx.Where("trace_id IN ?", traceIds).Delete(&AuditAttempt{}).Error; err != nil {
			return err
		}
		if err := tx.Where("id IN ?", traceIds).Delete(&AuditTrace{}).Error; err != nil {
			return err
		}
		if len(blobIds) > 0 {
			if err := tx.Where("id IN ?", blobIds).Delete(&AuditBlob{}).Error; err != nil {
				return err
			}
		}
		return nil
	})
}
