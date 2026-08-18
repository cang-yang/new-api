package service

import (
	"testing"

	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestAppendStreamStatusIncludesUnifiedOutcome(t *testing.T) {
	info := &relaycommon.RelayInfo{IsStream: true, ReceivedResponseCount: 3, StreamStatus: relaycommon.NewStreamStatus()}
	info.StreamStatus.SetEndReason(relaycommon.StreamEndReasonEOF, nil)
	other := map[string]interface{}{}

	appendStreamStatus(info, other)

	stream, ok := other["stream_status"].(map[string]interface{})
	require.True(t, ok)
	assert.Equal(t, "error", stream["status"])
	assert.Equal(t, "incomplete", stream["outcome"])
	assert.Equal(t, 3, stream["received_event_count"])
}
