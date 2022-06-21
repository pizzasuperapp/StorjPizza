// Copyright (C) 2019 Storj Labs, Inc.
// See LICENSE for copying information.

package audit_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"storj.io/common/pkcrypto"
	"storj.io/common/testcontext"
	"storj.io/common/testrand"
	"storj.io/storj/private/testplanet"
	"storj.io/storj/satellite/audit"
	"storj.io/storj/satellite/overlay"
	"storj.io/storj/satellite/reputation"
)

func TestContainIncrementAndGet(t *testing.T) {
	testplanet.Run(t, testplanet.Config{
		SatelliteCount: 1, StorageNodeCount: 2,
	}, func(t *testing.T, ctx *testcontext.Context, planet *testplanet.Planet) {
		containment := planet.Satellites[0].DB.Containment()

		input := &audit.PendingAudit{
			NodeID:            planet.StorageNodes[0].ID(),
			ExpectedShareHash: pkcrypto.SHA256Hash(testrand.Bytes(10)),
		}

		err := containment.IncrementPending(ctx, input)
		require.NoError(t, err)

		output, err := containment.Get(ctx, input.NodeID)
		require.NoError(t, err)

		assert.Equal(t, input, output)

		nodeID1 := planet.StorageNodes[1].ID()
		_, err = containment.Get(ctx, nodeID1)
		require.Error(t, err, audit.ErrContainedNotFound.New("%v", nodeID1))
		assert.True(t, audit.ErrContainedNotFound.Has(err))
	})
}

func TestContainIncrementPendingEntryExists(t *testing.T) {
	testplanet.Run(t, testplanet.Config{
		SatelliteCount: 1, StorageNodeCount: 1,
	}, func(t *testing.T, ctx *testcontext.Context, planet *testplanet.Planet) {
		containment := planet.Satellites[0].DB.Containment()

		info1 := &audit.PendingAudit{
			NodeID:            planet.StorageNodes[0].ID(),
			ExpectedShareHash: pkcrypto.SHA256Hash(testrand.Bytes(10)),
		}

		err := containment.IncrementPending(ctx, info1)
		require.NoError(t, err)

		// expect reverify count for an entry to be 0 after first IncrementPending call
		pending, err := containment.Get(ctx, info1.NodeID)
		require.NoError(t, err)
		assert.EqualValues(t, 0, pending.ReverifyCount)

		// expect reverify count to be 1 after second IncrementPending call
		err = containment.IncrementPending(ctx, info1)
		require.NoError(t, err)
		pending, err = containment.Get(ctx, info1.NodeID)
		require.NoError(t, err)
		assert.EqualValues(t, 1, pending.ReverifyCount)
	})
}

func TestContainDelete(t *testing.T) {
	testplanet.Run(t, testplanet.Config{
		SatelliteCount: 1, StorageNodeCount: 1,
	}, func(t *testing.T, ctx *testcontext.Context, planet *testplanet.Planet) {
		containment := planet.Satellites[0].DB.Containment()
		cache := planet.Satellites[0].DB.OverlayCache()

		info1 := &audit.PendingAudit{
			NodeID:            planet.StorageNodes[0].ID(),
			ExpectedShareHash: pkcrypto.SHA256Hash(testrand.Bytes(10)),
		}

		err := containment.IncrementPending(ctx, info1)
		require.NoError(t, err)

		// delete the node from containment db
		isDeleted, err := containment.Delete(ctx, info1.NodeID)
		require.NoError(t, err)
		assert.True(t, isDeleted)

		// check contained flag set to false
		node, err := cache.Get(ctx, info1.NodeID)
		require.NoError(t, err)
		assert.False(t, node.Contained)

		// get pending audit that doesn't exist
		_, err = containment.Get(ctx, info1.NodeID)
		assert.Error(t, err, audit.ErrContainedNotFound.New("%v", info1.NodeID))
		assert.True(t, audit.ErrContainedNotFound.Has(err))

		// delete pending audit that doesn't exist
		isDeleted, err = containment.Delete(ctx, info1.NodeID)
		require.NoError(t, err)
		assert.False(t, isDeleted)
	})
}

// UpdateStats used to remove nodes from containment. It doesn't anymore.
// This is a sanity check.
func TestContainUpdateStats(t *testing.T) {
	testplanet.Run(t, testplanet.Config{
		SatelliteCount: 1, StorageNodeCount: 1,
	}, func(t *testing.T, ctx *testcontext.Context, planet *testplanet.Planet) {
		containment := planet.Satellites[0].DB.Containment()
		cache := planet.Satellites[0].DB.OverlayCache()

		info1 := &audit.PendingAudit{
			NodeID:            planet.StorageNodes[0].ID(),
			ExpectedShareHash: pkcrypto.SHA256Hash(testrand.Bytes(10)),
		}

		err := containment.IncrementPending(ctx, info1)
		require.NoError(t, err)

		// update node stats
		err = planet.Satellites[0].Reputation.Service.ApplyAudit(ctx, info1.NodeID, overlay.ReputationStatus{}, reputation.AuditSuccess)
		require.NoError(t, err)

		// check contained flag set to false
		node, err := cache.Get(ctx, info1.NodeID)
		require.NoError(t, err)
		assert.False(t, node.Contained)

		// get pending audit
		_, err = containment.Get(ctx, info1.NodeID)
		require.NoError(t, err)
	})
}
