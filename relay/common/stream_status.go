package common

import (
	"fmt"
	"strings"
	"sync"
	"time"
)

type StreamEndReason string

type ResponseOutcome string

const (
	ResponseOutcomeComplete       ResponseOutcome = "complete"
	ResponseOutcomeEmpty          ResponseOutcome = "empty"
	ResponseOutcomeIncomplete     ResponseOutcome = "incomplete"
	ResponseOutcomeUpstreamFailed ResponseOutcome = "upstream_failed"
	ResponseOutcomeClientGone     ResponseOutcome = "client_gone"
	ResponseOutcomeTimeout        ResponseOutcome = "timeout"
	ResponseOutcomeParseError     ResponseOutcome = "parse_error"
)

const (
	StreamEndReasonNone           StreamEndReason = ""
	StreamEndReasonDone           StreamEndReason = "done"
	StreamEndReasonTimeout        StreamEndReason = "timeout"
	StreamEndReasonClientGone     StreamEndReason = "client_gone"
	StreamEndReasonScannerErr     StreamEndReason = "scanner_error"
	StreamEndReasonHandlerStop    StreamEndReason = "handler_stop"
	StreamEndReasonEOF            StreamEndReason = "eof"
	StreamEndReasonPanic          StreamEndReason = "panic"
	StreamEndReasonPingFail       StreamEndReason = "ping_fail"
	StreamEndReasonUpstreamFailed StreamEndReason = "upstream_failed"
)

const maxStreamErrorEntries = 20

type StreamErrorEntry struct {
	Message   string
	Timestamp time.Time
}

type StreamStatus struct {
	EndReason StreamEndReason
	EndError  error
	endOnce   sync.Once

	mu               sync.Mutex
	Errors           []StreamErrorEntry
	ErrorCount       int
	protocolFailure  bool
	protocolComplete bool
	protocolEmpty    bool
}

func NewStreamStatus() *StreamStatus {
	return &StreamStatus{}
}

func (s *StreamStatus) SetEndReason(reason StreamEndReason, err error) {
	if s == nil {
		return
	}
	s.endOnce.Do(func() {
		s.EndReason = reason
		s.EndError = err
	})
}

func (s *StreamStatus) RecordError(msg string) {
	if s == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.ErrorCount++
	if len(s.Errors) < maxStreamErrorEntries {
		s.Errors = append(s.Errors, StreamErrorEntry{
			Message:   msg,
			Timestamp: time.Now(),
		})
	}
}

func (s *StreamStatus) HasErrors() bool {
	if s == nil {
		return false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.ErrorCount > 0
}

func (s *StreamStatus) MarkProtocolFailure(msg string) {
	if s == nil {
		return
	}
	s.mu.Lock()
	s.protocolFailure = true
	s.mu.Unlock()
	if msg != "" {
		s.RecordError(msg)
	}
}

// MarkProtocolComplete records that the upstream protocol emitted its own
// terminal success event. It does not stop scanning because some protocols
// send usage-only chunks after the terminal candidate.
func (s *StreamStatus) MarkProtocolComplete() {
	if s == nil {
		return
	}
	s.mu.Lock()
	s.protocolComplete = true
	s.mu.Unlock()
}

// MarkProtocolEmpty refines a successful terminal event when no meaningful
// model output was observed.
func (s *StreamStatus) MarkProtocolEmpty() {
	if s == nil {
		return
	}
	s.mu.Lock()
	s.protocolEmpty = true
	s.mu.Unlock()
}

func (s *StreamStatus) TotalErrorCount() int {
	if s == nil {
		return 0
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.ErrorCount
}

func (s *StreamStatus) IsNormalEnd() bool {
	if s == nil {
		return true
	}
	s.mu.Lock()
	protocolComplete := s.protocolComplete
	protocolFailure := s.protocolFailure
	protocolEmpty := s.protocolEmpty
	s.mu.Unlock()
	return (s.EndReason == StreamEndReasonDone || protocolComplete) &&
		!protocolFailure && !protocolEmpty && !s.HasErrors()
}

func (s *StreamStatus) Outcome(receivedEventCount int) ResponseOutcome {
	if s == nil {
		return ResponseOutcomeComplete
	}
	s.mu.Lock()
	protocolFailure := s.protocolFailure
	protocolComplete := s.protocolComplete
	protocolEmpty := s.protocolEmpty
	s.mu.Unlock()
	if protocolFailure {
		return ResponseOutcomeUpstreamFailed
	}
	if protocolEmpty {
		return ResponseOutcomeEmpty
	}
	if protocolComplete {
		if s.HasErrors() {
			return ResponseOutcomeParseError
		}
		return ResponseOutcomeComplete
	}
	switch s.EndReason {
	case StreamEndReasonDone:
		if s.HasErrors() {
			return ResponseOutcomeParseError
		}
		return ResponseOutcomeComplete
	case StreamEndReasonEOF:
		if receivedEventCount == 0 {
			return ResponseOutcomeEmpty
		}
		return ResponseOutcomeIncomplete
	case StreamEndReasonClientGone:
		return ResponseOutcomeClientGone
	case StreamEndReasonTimeout:
		return ResponseOutcomeTimeout
	case StreamEndReasonScannerErr, StreamEndReasonPanic, StreamEndReasonPingFail, StreamEndReasonUpstreamFailed:
		return ResponseOutcomeUpstreamFailed
	case StreamEndReasonHandlerStop:
		return ResponseOutcomeParseError
	default:
		return ResponseOutcomeIncomplete
	}
}

func (s *StreamStatus) Summary() string {
	if s == nil {
		return "StreamStatus<nil>"
	}
	b := &strings.Builder{}
	fmt.Fprintf(b, "reason=%s", s.EndReason)
	if s.EndError != nil {
		fmt.Fprintf(b, " end_error=%q", s.EndError.Error())
	}
	s.mu.Lock()
	if s.ErrorCount > 0 {
		fmt.Fprintf(b, " soft_errors=%d", s.ErrorCount)
	}
	s.mu.Unlock()
	return b.String()
}
