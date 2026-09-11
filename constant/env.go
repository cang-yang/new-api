package constant

var StreamingTimeout int
var DifyDebug bool
var MaxFileDownloadMB int
var StreamScannerMaxBufferMB int
var ForceStreamOption bool
var CountToken bool
var GetMediaToken bool
var GetMediaTokenNotStream bool
var UpdateTask bool
var MaxRequestBodyMB int
var AnonymousRequestBodyLimitKB int

// MaxPreConsumeQuotaPerRequest limits the estimated quota reserved by one
// request. A value of 0 disables the guard.
var MaxPreConsumeQuotaPerRequest int

// Body audit captures the final upstream request and raw upstream response for
// administrator-only inspection from the usage log details dialog.
var BodyAuditEnabled bool
var BodyAuditMaxBodyMB int
var BodyAuditRetentionDays int
var AzureDefaultAPIVersion string
var NotifyLimitCount int
var NotificationLimitDurationMinute int
var GenerateDefaultToken bool
var ErrorLogEnabled bool
var TaskQueryLimit int
var TaskTimeoutMinutes int
var TaskPollMaxFailures = 20
var TaskPluginProtocolTimeoutSeconds int
var TaskPluginProtocolTickMilliseconds int
var TaskPluginProtocolTickJitterMilliseconds int
var TaskPluginProtocolHeartbeatSeconds int

// temporary variable for sora patch, will be removed in future
var TaskPricePatches []string

// TrustedRedirectDomains is a list of trusted domains for redirect URL validation.
// Domains support subdomain matching (e.g., "example.com" matches "sub.example.com").
var TrustedRedirectDomains []string
