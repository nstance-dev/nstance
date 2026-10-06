// Nstance <https://nstance.dev>
// Copyright The Nstance Authors
// SPDX-License-Identifier: Apache-2.0

// Package nat coordinates tenant-managed network address translation.
package nat

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/nstance-dev/nstance/v2/internal/server/config"
	"github.com/nstance-dev/nstance/v2/internal/server/storage"
)

const assignmentStateKey = "nat/assignments.json"

// ErrPublicAddressesExhausted indicates that every configured address is assigned.
var ErrPublicAddressesExhausted = errors.New("public addresses exhausted")

// Assignment records one managed NAT instance and its routed instance subnet.
type Assignment struct {
	Tenant           string                `json:"tenant"`
	InstanceSubnetID string                `json:"instance_subnet_id"`
	InstanceID       string                `json:"instance_id"`
	InstanceType     string                `json:"instance_type"`
	ProviderID       string                `json:"provider_id,omitempty"`
	PublicAddress    *config.PublicAddress `json:"public_address,omitempty"`
	EmptySince       *time.Time            `json:"empty_since,omitempty"`
	Deleting         bool                  `json:"deleting,omitempty"`
	Retiring         bool                  `json:"retiring,omitempty"`
	Routed           bool                  `json:"routed,omitempty"`
	Replaces         string                `json:"replaces,omitempty"`
	CreatedAt        time.Time             `json:"created_at"`
	LastScaleAt      *time.Time            `json:"last_scale_at,omitempty"`
}

// assignmentState is the durable envelope for managed NAT assignments.
type assignmentState struct {
	Assignments map[string]Assignment `json:"assignments"`
}

// AssignmentStore persists managed NAT lifecycle state in shard storage.
type AssignmentStore struct {
	storage storage.Storage
	mu      sync.Mutex
}

// NewAssignmentStore creates a managed NAT assignment store.
func NewAssignmentStore(store storage.Storage) (*AssignmentStore, error) {
	if store == nil {
		return nil, fmt.Errorf("storage is required")
	}
	return &AssignmentStore{storage: store}, nil
}

// Claim records an initial NAT instance, assigning a fixed address when configured.
func (s *AssignmentStore) Claim(ctx context.Context, addresses []config.PublicAddress, tenant, instanceSubnetID, instanceID, instanceType string, createdAt time.Time) (Assignment, error) {
	if tenant == "" || instanceSubnetID == "" || instanceID == "" {
		return Assignment{}, fmt.Errorf("tenant, instance subnet, and instance are required")
	}
	var claimed Assignment
	err := s.update(ctx, func(assignments map[string]Assignment) error {
		if existing, ok := assignments[instanceID]; ok {
			claimed = existing
			return nil
		}
		address, err := availableAddress(addresses, assignments)
		if err != nil {
			return err
		}
		claimed = Assignment{
			Tenant: tenant, InstanceSubnetID: instanceSubnetID, InstanceID: instanceID,
			InstanceType: instanceType, PublicAddress: address, CreatedAt: createdAt,
		}
		assignments[instanceID] = claimed
		return nil
	})
	return claimed, err
}

// ClaimReplacement records a create-before-destroy replacement using the
// current subnet's fixed address, if any.
func (s *AssignmentStore) ClaimReplacement(ctx context.Context, current Assignment, instanceID, instanceType string, createdAt time.Time) (Assignment, error) {
	var replacement Assignment
	err := s.update(ctx, func(assignments map[string]Assignment) error {
		if existing, ok := assignments[instanceID]; ok {
			replacement = existing
			return nil
		}
		for _, assignment := range assignments {
			if assignment.Replaces == current.InstanceID {
				return fmt.Errorf("NAT instance %q already has a replacement", current.InstanceID)
			}
		}
		if _, ok := assignments[current.InstanceID]; !ok {
			return fmt.Errorf("NAT instance %q is not assigned", current.InstanceID)
		}
		replacement = Assignment{
			Tenant: current.Tenant, InstanceSubnetID: current.InstanceSubnetID,
			InstanceID: instanceID, InstanceType: instanceType,
			PublicAddress: current.PublicAddress, Replaces: current.InstanceID,
			CreatedAt: createdAt,
		}
		assignments[instanceID] = replacement
		return nil
	})
	return replacement, err
}

// Update changes lifecycle fields while preserving assignment ownership.
func (s *AssignmentStore) Update(ctx context.Context, instanceID string, apply func(*Assignment)) error {
	return s.update(ctx, func(assignments map[string]Assignment) error {
		assignment, ok := assignments[instanceID]
		if !ok {
			return fmt.Errorf("NAT instance %q is not assigned", instanceID)
		}
		apply(&assignment)
		assignments[instanceID] = assignment
		return nil
	})
}

// Promote makes a replacement routable and marks its predecessor for removal.
func (s *AssignmentStore) Promote(ctx context.Context, instanceID string, scaledAt time.Time) error {
	return s.update(ctx, func(assignments map[string]Assignment) error {
		replacement, ok := assignments[instanceID]
		if !ok || replacement.Replaces == "" {
			return fmt.Errorf("NAT instance %q is not a replacement", instanceID)
		}
		previous, ok := assignments[replacement.Replaces]
		if !ok {
			return fmt.Errorf("replaced NAT instance %q is not assigned", replacement.Replaces)
		}
		previous.Retiring = true
		previous.Routed = false
		replacement.Replaces = ""
		replacement.Routed = true
		replacement.LastScaleAt = &scaledAt
		assignments[previous.InstanceID] = previous
		assignments[replacement.InstanceID] = replacement
		return nil
	})
}

// Release removes one instance assignment.
func (s *AssignmentStore) Release(ctx context.Context, instanceID string) error {
	return s.update(ctx, func(assignments map[string]Assignment) error {
		delete(assignments, instanceID)
		return nil
	})
}

// Assignments returns the current durable lifecycle state.
func (s *AssignmentStore) Assignments(ctx context.Context) (map[string]Assignment, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	state, _, err := s.load(ctx)
	if err != nil {
		return nil, err
	}
	return state.Assignments, nil
}

// availableAddress returns the first configured address not held by an assignment.
func availableAddress(addresses []config.PublicAddress, assignments map[string]Assignment) (*config.PublicAddress, error) {
	if len(addresses) == 0 {
		return nil, nil
	}
	used := make(map[string]bool, len(assignments))
	for _, assignment := range assignments {
		if assignment.PublicAddress != nil {
			used[assignment.PublicAddress.IPv4] = true
		}
	}
	for _, address := range addresses {
		if !used[address.IPv4] {
			selected := address
			return &selected, nil
		}
	}
	return nil, ErrPublicAddressesExhausted
}

// update applies and conditionally persists an assignment-state mutation.
func (s *AssignmentStore) update(ctx context.Context, apply func(map[string]Assignment) error) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for range 8 {
		state, etag, err := s.load(ctx)
		if err != nil {
			return err
		}
		if err := apply(state.Assignments); err != nil {
			return err
		}
		data, err := json.Marshal(state)
		if err != nil {
			return err
		}
		if err := s.storage.PutIfMatch(ctx, assignmentStateKey, data, etag); errors.Is(err, storage.ErrPrecondition) {
			continue
		} else if err != nil {
			return fmt.Errorf("write NAT assignment state: %w", err)
		}
		return nil
	}
	return fmt.Errorf("update NAT assignment state: too many concurrent updates")
}

// load reads assignment state and its current storage entity tag.
func (s *AssignmentStore) load(ctx context.Context) (*assignmentState, string, error) {
	data, etag, err := s.storage.Get(ctx, assignmentStateKey)
	if errors.Is(err, storage.ErrNotFound) {
		return &assignmentState{Assignments: make(map[string]Assignment)}, "", nil
	}
	if err != nil {
		return nil, "", fmt.Errorf("read NAT assignment state: %w", err)
	}
	state := &assignmentState{}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(state); err != nil {
		return nil, "", fmt.Errorf("decode NAT assignment state: %w", err)
	}
	if state.Assignments == nil {
		state.Assignments = make(map[string]Assignment)
	}
	return state, etag, nil
}
