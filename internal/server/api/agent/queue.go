// Nstance <https://nstance.dev>
// Copyright The Nstance Authors
// SPDX-License-Identifier: Apache-2.0

package agent

import (
	"context"
	"time"
)

// requestFiles generates the requested file patch for an instance.
// A nil filenames slice requests every configured file; an empty slice permits a hash-only patch.
func (s *Service) requestFiles(instanceID, configHash string, filenames []string) {
	s.pendingFilesMu.Lock()
	// Health reports will retry; do not generate file contents without a receiver.
	if s.pendingFilesNotify[instanceID] == nil {
		s.pendingFilesMu.Unlock()
		return
	}
	if request := s.fileRequests[instanceID]; request != nil {
		if request.ConfigHash == configHash {
			s.pendingFilesMu.Unlock()
			return
		}
		request.Cancel()
		delete(s.fileRequests, instanceID)
	}
	if pending := s.pendingFiles[instanceID]; pending != nil && pending.ConfigHash == configHash {
		s.pendingFilesMu.Unlock()
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	request := &fileRequest{ConfigHash: configHash, Cancel: cancel}
	s.fileRequests[instanceID] = request
	s.pendingFilesMu.Unlock()

	go func() {
		defer cancel()
		var generated map[string][]byte
		runtimeHash := configHash
		var err error
		if filenames == nil || len(filenames) != 0 {
			generated, runtimeHash, err = s.fileGenerator.GenerateFiles(ctx, instanceID, filenames)
		}

		s.pendingFilesMu.Lock()
		if s.fileRequests[instanceID] != request {
			s.pendingFilesMu.Unlock()
			return
		}
		delete(s.fileRequests, instanceID)
		if err != nil {
			s.pendingFilesMu.Unlock()
			s.logger.Error("Failed to generate pending files", "instance_id", instanceID, "error", err)
			return
		}
		if runtimeHash != configHash {
			s.pendingFilesMu.Unlock()
			s.logger.Debug("Discarded stale file patch", "instance_id", instanceID)
			return
		}
		if runtimeHash == "" {
			s.pendingFilesMu.Unlock()
			return
		}
		notify := s.pendingFilesNotify[instanceID]
		if notify == nil {
			s.pendingFilesMu.Unlock()
			return
		}

		now := time.Now().UTC()
		files := make([]*PendingFile, 0, len(generated))
		for filename, content := range generated {
			files = append(files, &PendingFile{Filename: filename, Content: content, LastModified: now})
		}
		s.pendingFiles[instanceID] = &pendingFilePatch{
			Files:      files,
			ConfigHash: runtimeHash,
		}
		s.pendingFilesMu.Unlock()
		notifyStream(notify)
		s.logger.Debug("Generated pending file patch", "instance_id", instanceID, "updated", len(files))
	}()
}

// ForgetInstance discards pending file work after an instance is permanently removed.
func (s *Service) ForgetInstance(instanceID string) {
	s.pendingFilesMu.Lock()
	defer s.pendingFilesMu.Unlock()
	delete(s.pendingFiles, instanceID)
	if request := s.fileRequests[instanceID]; request != nil {
		request.Cancel()
	}
	delete(s.fileRequests, instanceID)
}

// registerPendingFilesStream records the wakeup channel for an instance's active file stream.
func (s *Service) registerPendingFilesStream(instanceID string) chan struct{} {
	s.pendingFilesMu.Lock()
	previous := s.pendingFilesNotify[instanceID]
	ch := make(chan struct{}, 1)
	s.pendingFilesNotify[instanceID] = ch
	s.pendingFilesMu.Unlock()
	notifyStream(previous)
	return ch
}

// unregisterPendingFilesStream removes the wakeup channel if it still belongs to this stream.
func (s *Service) unregisterPendingFilesStream(instanceID string, ch chan struct{}) {
	s.pendingFilesMu.Lock()
	defer s.pendingFilesMu.Unlock()
	if s.pendingFilesNotify[instanceID] == ch {
		delete(s.pendingFilesNotify, instanceID)
		delete(s.pendingFiles, instanceID)
		if request := s.fileRequests[instanceID]; request != nil {
			request.Cancel()
		}
		delete(s.fileRequests, instanceID)
	}
}

// queueKeyRequest adds a key generation request to the pending queue for an instance
func (s *Service) queueKeyRequest(instanceID string, keyNames []string) {
	s.pendingKeyRequestsMu.Lock()

	request := &PendingKeyRequest{
		KeyNames: keyNames,
		Created:  time.Now().UTC(),
	}

	s.pendingKeyRequests[instanceID] = append(s.pendingKeyRequests[instanceID], request)
	notify := s.pendingKeyRequestsNotify[instanceID]
	s.pendingKeyRequestsMu.Unlock()

	notifyStream(notify)

	s.logger.Debug("Queued key generation request", "instance_id", instanceID, "key_count", len(keyNames))
}

// getPendingKeyRequests returns and clears pending key requests for an instance
func (s *Service) getPendingKeyRequests(instanceID string) []*PendingKeyRequest {
	s.pendingKeyRequestsMu.Lock()
	defer s.pendingKeyRequestsMu.Unlock()

	requests := s.pendingKeyRequests[instanceID]
	if len(requests) == 0 {
		return nil
	}

	// Clear the requests after returning them
	delete(s.pendingKeyRequests, instanceID)
	s.logger.Debug("Retrieved pending key requests", "instance_id", instanceID, "request_count", len(requests))

	// Return a copy to avoid race conditions
	result := make([]*PendingKeyRequest, len(requests))
	copy(result, requests)
	return result
}

// registerPendingKeyRequestsStream records the wakeup channel for an instance's active key request stream.
func (s *Service) registerPendingKeyRequestsStream(instanceID string) chan struct{} {
	s.pendingKeyRequestsMu.Lock()
	defer s.pendingKeyRequestsMu.Unlock()

	ch := make(chan struct{}, 1)
	s.pendingKeyRequestsNotify[instanceID] = ch
	return ch
}

// unregisterPendingKeyRequestsStream removes the wakeup channel if it still belongs to this stream.
func (s *Service) unregisterPendingKeyRequestsStream(instanceID string, ch chan struct{}) {
	s.pendingKeyRequestsMu.Lock()
	defer s.pendingKeyRequestsMu.Unlock()

	if s.pendingKeyRequestsNotify[instanceID] == ch {
		delete(s.pendingKeyRequestsNotify, instanceID)
	}
}

// notifyStream wakes an active stream without blocking the producer if it is already awake.
func notifyStream(ch chan struct{}) {
	if ch == nil {
		return
	}
	select {
	case ch <- struct{}{}:
	default:
	}
}
