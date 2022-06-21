// Copyright (C) 2019 Storj Labs, Inc.
// See LICENSE for copying information.

package checker

import (
	"context"
	"sync"
	"sync/atomic"
	"time"

	"storj.io/common/storj"
	"storj.io/storj/satellite/metabase"
	"storj.io/storj/satellite/overlay"
)

// ReliabilityCache caches the reliable nodes for the specified staleness duration
// and updates automatically from overlay.
//
// architecture: Service
type ReliabilityCache struct {
	overlay   *overlay.Service
	staleness time.Duration
	mu        sync.Mutex
	state     atomic.Value // contains immutable *reliabilityState
}

// reliabilityState.
type reliabilityState struct {
	reliable map[storj.NodeID]struct{}
	created  time.Time
}

// NewReliabilityCache creates a new reliability checking cache.
func NewReliabilityCache(overlay *overlay.Service, staleness time.Duration) *ReliabilityCache {
	return &ReliabilityCache{
		overlay:   overlay,
		staleness: staleness,
	}
}

// LastUpdate returns when the cache was last updated, or the zero value (time.Time{}) if it
// has never yet been updated. LastUpdate() does not trigger an update itself.
func (cache *ReliabilityCache) LastUpdate() time.Time {
	if state, ok := cache.state.Load().(*reliabilityState); ok {
		return state.created
	}
	return time.Time{}
}

// NumNodes returns the number of online active nodes (as determined by the reliability cache).
// This number is not guaranteed to be consistent with either the nodes database or the
// reliability cache after returning; it is just a best-effort count and should be treated as an
// estimate.
func (cache *ReliabilityCache) NumNodes(ctx context.Context) (numNodes int, err error) {
	defer mon.Task()(&ctx)(&err)

	state, err := cache.loadFast(ctx, time.Time{})
	if err != nil {
		return 0, err
	}
	return len(state.reliable), nil
}

// MissingPieces returns piece indices that are unreliable with the given staleness period.
func (cache *ReliabilityCache) MissingPieces(ctx context.Context, created time.Time, pieces metabase.Pieces) (_ []metabase.Piece, err error) {
	defer mon.Task()(&ctx)(&err)

	state, err := cache.loadFast(ctx, created)
	if err != nil {
		return nil, err
	}
	var unreliable []metabase.Piece
	for _, p := range pieces {
		if _, ok := state.reliable[p.StorageNode]; !ok {
			unreliable = append(unreliable, p)
		}
	}
	return unreliable, nil
}

func (cache *ReliabilityCache) loadFast(ctx context.Context, validUpTo time.Time) (_ *reliabilityState, err error) {
	defer mon.Task()(&ctx)(&err)

	// This code is designed to be very fast in the case where a refresh is not needed: just an
	// atomic load from rarely written to bit of shared memory. The general strategy is to first
	// read if the state suffices to answer the query. If not (due to it not existing, being
	// too stale, etc.), then we acquire the mutex to block other requests that may be stale
	// and ensure we only issue one refresh at a time. After acquiring the mutex, we have to
	// double check that the state is still stale because some other call may have beat us to
	// the acquisition. Only then do we refresh and can then proceed answering the query.

	state, ok := cache.state.Load().(*reliabilityState)
	if !ok || validUpTo.After(state.created) || time.Since(state.created) > cache.staleness {
		cache.mu.Lock()
		state, ok = cache.state.Load().(*reliabilityState)
		if !ok || validUpTo.After(state.created) || time.Since(state.created) > cache.staleness {
			state, err = cache.refreshLocked(ctx)
		}
		cache.mu.Unlock()
		if err != nil {
			return nil, err
		}
	}
	return state, nil
}

// Refresh refreshes the cache.
func (cache *ReliabilityCache) Refresh(ctx context.Context) (err error) {
	defer mon.Task()(&ctx)(&err)

	cache.mu.Lock()
	defer cache.mu.Unlock()

	_, err = cache.refreshLocked(ctx)
	return err
}

// refreshLocked does the refreshes assuming the write mutex is held.
func (cache *ReliabilityCache) refreshLocked(ctx context.Context) (_ *reliabilityState, err error) {
	defer mon.Task()(&ctx)(&err)

	nodes, err := cache.overlay.Reliable(ctx)
	if err != nil {
		return nil, Error.Wrap(err)
	}

	state := &reliabilityState{
		created:  time.Now(),
		reliable: make(map[storj.NodeID]struct{}, len(nodes)),
	}
	for _, id := range nodes {
		state.reliable[id] = struct{}{}
	}

	cache.state.Store(state)
	return state, nil
}
