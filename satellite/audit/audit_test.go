// Copyright (C) 2021 Storj Labs, Inc.
// See LICENSE for copying information.

package audit_test

import (
	"context"
	"sort"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"storj.io/common/memory"
	"storj.io/common/pb"
	"storj.io/common/storj"
	"storj.io/common/testcontext"
	"storj.io/common/testrand"
	"storj.io/storj/private/testplanet"
	"storj.io/storj/satellite/accounting"
	"storj.io/storj/satellite/audit"
)

// TestAuditOrderLimit tests that while auditing, order limits without
// specified bucket are counted correctly for storage node audit bandwidth
// usage and the storage nodes will be paid for that.
func TestAuditOrderLimit(t *testing.T) {
	testplanet.Run(t, testplanet.Config{
		SatelliteCount: 1, StorageNodeCount: 4, UplinkCount: 1,
	}, func(t *testing.T, ctx *testcontext.Context, planet *testplanet.Planet) {
		satellite := planet.Satellites[0]
		audits := satellite.Audit

		audits.Worker.Loop.Pause()
		audits.Chore.Loop.Pause()

		now := time.Now()

		for _, storageNode := range planet.StorageNodes {
			storageNode.Storage2.Orders.Sender.Pause()
		}

		ul := planet.Uplinks[0]
		testData := testrand.Bytes(8 * memory.KiB)

		err := ul.Upload(ctx, satellite, "testbucket", "test/path", testData)
		require.NoError(t, err)

		audits.Chore.Loop.TriggerWait()
		queue := audits.Queues.Fetch()
		queueSegment, err := queue.Next()
		require.NoError(t, err)
		require.False(t, queueSegment.StreamID.IsZero())

		_, err = audits.Verifier.Reverify(ctx, queueSegment)
		require.NoError(t, err)

		report, err := audits.Verifier.Verify(ctx, queueSegment, nil)
		require.NoError(t, err)

		require.NoError(t, planet.WaitForStorageNodeEndpoints(ctx))

		for _, storageNode := range planet.StorageNodes {
			storageNode.Storage2.Orders.SendOrders(ctx, now.Add(24*time.Hour))
		}

		auditSettled := make(map[storj.NodeID]uint64)
		err = satellite.DB.StoragenodeAccounting().GetBandwidthSince(ctx, time.Time{}, func(c context.Context, sbr *accounting.StoragenodeBandwidthRollup) error {
			if sbr.Action == uint(pb.PieceAction_GET_AUDIT) {
				auditSettled[sbr.NodeID] += sbr.Settled
			}
			return nil
		})
		require.NoError(t, err)

		for _, nodeID := range report.Successes {
			require.NotZero(t, auditSettled[nodeID])
		}
	})
}

// Minimal test to verify that copies aren't audited.
func TestAuditSkipsRemoteCopies(t *testing.T) {
	testplanet.Run(t, testplanet.Config{
		SatelliteCount: 1, StorageNodeCount: 4, UplinkCount: 1,
	}, func(t *testing.T, ctx *testcontext.Context, planet *testplanet.Planet) {
		satellite := planet.Satellites[0]
		audits := satellite.Audit

		audits.Worker.Loop.Pause()
		audits.Chore.Loop.Pause()

		uplink := planet.Uplinks[0]
		testData := testrand.Bytes(8 * memory.KiB)

		err := uplink.Upload(ctx, satellite, "testbucket", "testobj1", testData)
		require.NoError(t, err)
		err = uplink.Upload(ctx, satellite, "testbucket", "testobj2", testData)
		require.NoError(t, err)

		originalSegments, err := satellite.Metabase.DB.TestingAllSegments(ctx)
		require.NoError(t, err)
		require.Len(t, originalSegments, 2)

		project, err := uplink.OpenProject(ctx, satellite)
		require.NoError(t, err)
		defer ctx.Check(project.Close)

		_, err = project.CopyObject(ctx, "testbucket", "testobj1", "testbucket", "copy", nil)
		require.NoError(t, err)

		audits.Chore.Loop.TriggerWait()
		queue := audits.Queues.Fetch()
		require.Equal(t, 2, queue.Size())

		auditSegments := make([]audit.Segment, 0, 2)
		for range originalSegments {
			auditSegment, err := queue.Next()
			require.NoError(t, err)
			auditSegments = append(auditSegments, auditSegment)
		}

		sort.Slice(auditSegments, func(i, j int) bool {
			// None of the audit methods expose the pieces so best we can compare is StreamID
			return auditSegments[i].StreamID.Less(auditSegments[j].StreamID)
		})

		// Check that StreamID of copy
		for i := range originalSegments {
			require.Equal(t, originalSegments[i].StreamID, auditSegments[i].StreamID)
		}

		// delete originals, keep 1 copy
		err = uplink.DeleteObject(ctx, satellite, "testbucket", "testobj1")
		require.NoError(t, err)
		err = uplink.DeleteObject(ctx, satellite, "testbucket", "testobj2")
		require.NoError(t, err)

		audits.Chore.Loop.TriggerWait()
		queue = audits.Queues.Fetch()

		// verify that the copy is being audited
		require.Equal(t, 1, queue.Size())
		remainingSegment, err := queue.Next()
		require.NoError(t, err)

		for _, originalSegment := range originalSegments {
			require.NotEqual(t, originalSegment.StreamID, remainingSegment.StreamID)
		}
	})
}

// Minimal test to verify that inline objects are not audited even if they are copies.
func TestAuditSkipsInlineCopies(t *testing.T) {
	testplanet.Run(t, testplanet.Config{
		SatelliteCount: 1, StorageNodeCount: 0, UplinkCount: 1,
	}, func(t *testing.T, ctx *testcontext.Context, planet *testplanet.Planet) {
		satellite := planet.Satellites[0]
		audits := satellite.Audit

		audits.Worker.Loop.Pause()
		audits.Chore.Loop.Pause()

		uplink := planet.Uplinks[0]
		testData := testrand.Bytes(1 * memory.KiB)

		err := uplink.Upload(ctx, satellite, "testbucket", "testobj1", testData)
		require.NoError(t, err)
		err = uplink.Upload(ctx, satellite, "testbucket", "testobj2", testData)
		require.NoError(t, err)

		project, err := uplink.OpenProject(ctx, satellite)
		require.NoError(t, err)
		defer ctx.Check(project.Close)

		_, err = project.CopyObject(ctx, "testbucket", "testobj1", "testbucket", "copy", nil)
		require.NoError(t, err)

		audits.Chore.Loop.TriggerWait()
		queue := audits.Queues.Fetch()
		require.Zero(t, queue.Size())

		// delete originals, keep 1 copy
		err = uplink.DeleteObject(ctx, satellite, "testbucket", "testobj1")
		require.NoError(t, err)
		err = uplink.DeleteObject(ctx, satellite, "testbucket", "testobj2")
		require.NoError(t, err)

		audits.Chore.Loop.TriggerWait()
		queue = audits.Queues.Fetch()

		require.Zero(t, queue.Size())
	})
}
