// SPDX-License-Identifier: AGPL-3.0-or-later
// Copyright (C) QuantumNous and contributors.
// WebSocket event reply filtering follows QuantumNous/new-api
// relay/requestplugin/socket.go at 48f9df96ea7c68eae04a0f3687869f232f48929f.
package replyfilter

import (
	"context"
	"errors"
)

// SocketFilter applies reply hooks to one WebSocket turn's event stream while
// letting the session keep the underlying connection alive if a turn stops.
type SocketFilter struct {
	w       *Writer
	send    func([]byte)
	stopErr *Failure
}

// NewSocket creates a per-turn WebSocket reply filter for JSON event messages.
// send receives every released non-terminal event (and delta events synthesized
// on flush). onStop runs at most once when a reply hook stops or fails the turn.
func NewSocket(parent context.Context, format string, instances []*Instance, send func([]byte), onStop func()) (*SocketFilter, context.Context, error) {
	if send == nil {
		return nil, parent, errors.New("socket send callback is required")
	}
	check, cancel, err := newCheck(parent, format, instances)
	if err != nil {
		return nil, parent, err
	}
	check.stream = true
	w := &Writer{
		format: format,
		check:  check,
		cancel: cancel,
		onStop: onStop,
		mode:   replyStream,
		socket: send,
	}
	return &SocketFilter{w: w, send: send}, check.ctx, nil
}

// Event feeds one non-terminal JSON event from upstream. Once stopped, further
// events are dropped until the caller terminates the turn.
func (s *SocketFilter) Event(message []byte) {
	if s == nil || s.w == nil {
		return
	}
	s.w.mu.Lock()
	defer s.w.mu.Unlock()
	switch {
	case s.w.stopped:
		return
	case s.w.mode == replyPassing:
		s.send(message)
	default:
		s.w.message(message)
	}
}

// Terminal checks the turn-ending JSON event (e.g. response.completed), emits
// any held events before it, and returns the final (possibly masked) terminal
// event for the session to send. Returns nil when a hook stopped the turn.
func (s *SocketFilter) Terminal(message []byte) []byte {
	if s == nil || s.w == nil {
		return message
	}
	s.w.mu.Lock()
	defer s.w.mu.Unlock()
	if s.w.stopped {
		return nil
	}
	if s.w.mode == replyPassing {
		return message
	}
	var last []byte
	s.w.socket = func(data []byte) {
		if last != nil {
			s.send(last)
		}
		last = data
	}
	s.w.message(message)
	if !s.w.stopped {
		s.w.endStream()
	}
	s.w.socket = s.send
	if s.w.stopped {
		if last != nil {
			s.send(last)
		}
		return nil
	}
	if last != nil {
		return last
	}
	return message
}

// Stopped returns the policy failure when the turn was stopped, or nil.
func (s *SocketFilter) Stopped() *Failure {
	if s == nil || s.w == nil {
		return nil
	}
	s.w.mu.Lock()
	defer s.w.mu.Unlock()
	if !s.w.stopped {
		return nil
	}
	if s.stopErr == nil {
		s.stopErr = s.w.stopError()
	}
	return s.stopErr
}

// Finish closes any unfinished turn stream, cancels the turn context, and
// returns aggregate audit records and whether the turn was stopped.
func (s *SocketFilter) Finish() ([]Record, bool) {
	if s == nil || s.w == nil {
		return nil, false
	}
	s.w.mu.Lock()
	defer s.w.mu.Unlock()
	defer s.w.cancel()
	if s.w.mode != replyPassing && !s.w.stopped {
		s.w.endStream()
	}
	s.w.mode = replyPassing
	return s.w.records(), s.w.stopped
}
