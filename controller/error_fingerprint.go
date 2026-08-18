package controller

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
)

type channelErrorSignature struct {
	RequestPath string `json:"request_path,omitempty"`
	ErrorType   string `json:"error_type,omitempty"`
	ErrorCode   string `json:"error_code,omitempty"`
	StatusCode  int    `json:"status_code"`
	ChannelType int    `json:"channel_type"`
	Model       string `json:"model,omitempty"`
	IsStream    bool   `json:"is_stream"`
}

func fingerprintChannelError(signature channelErrorSignature) string {
	payload, _ := json.Marshal(signature)
	sum := sha256.Sum256(payload)
	return "cef1_" + hex.EncodeToString(sum[:12])
}
