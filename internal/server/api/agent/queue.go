// Nstance <https://nstance.dev>
// Copyright The Nstance Authors
// SPDX-License-Identifier: Apache-2.0

package agent

import (
	"time"
)

// QueueFile implements FileDelivery interface
func (s *Service) QueueFile(instanceID, filename string, content []byte) {
	s.pendingFilesMu.Lock()

	file := &PendingFile{
		Filename:     filename,
		Content:      content,
		LastModified: time.Now().UTC(),
	}

	s.pendingFiles[instanceID] = append(s.pendingFiles[instanceID], file)
	notify := s.pendingFilesNotify[instanceID]
	s.pendingFilesMu.Unlock()

	notifyStream(notify)

	s.logger.Debug("Queued pending file",
		"instance_id", instanceID,
		"filename", filename,
		"size", len(content))
}

func (s *Service) getPendingFiles(instanceID string) []*PendingFile {
	s.pendingFilesMu.RLock()
	defer s.pendingFilesMu.RUnlock()

	files := s.pendingFiles[instanceID]
	if len(files) == 0 {
		return nil
	}

	// Return a copy to avoid race conditions
	result := make([]*PendingFile, len(files))
	copy(result, files)
	return result
}

func (s *Service) clearPendingFiles(instanceID string) {
	s.pendingFilesMu.Lock()
	defer s.pendingFilesMu.Unlock()

	delete(s.pendingFiles, instanceID)
	s.logger.Debug("Cleared pending files", "instance_id", instanceID)
}

// registerPendingFilesStream records the wakeup channel for an instance's active file stream.
func (s *Service) registerPendingFilesStream(instanceID string) chan struct{} {
	s.pendingFilesMu.Lock()
	defer s.pendingFilesMu.Unlock()

	ch := make(chan struct{}, 1)
	s.pendingFilesNotify[instanceID] = ch
	return ch
}

// unregisterPendingFilesStream removes the wakeup channel if it still belongs to this stream.
func (s *Service) unregisterPendingFilesStream(instanceID string, ch chan struct{}) {
	s.pendingFilesMu.Lock()
	defer s.pendingFilesMu.Unlock()

	if s.pendingFilesNotify[instanceID] == ch {
		delete(s.pendingFilesNotify, instanceID)
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
