// Copyright (C) 2019 Storj Labs, Inc.
// See LICENSE for copying information.

package audit_test

import (
	"strconv"
	"testing"

	"github.com/stretchr/testify/require"

	"storj.io/common/memory"
	"storj.io/common/testcontext"
	"storj.io/common/testrand"
	"storj.io/storj/private/testplanet"
	"storj.io/storj/satellite/audit"
)

func TestChoreAndWorkerIntegration(t *testing.T) {
	testplanet.Run(t, testplanet.Config{
		SatelliteCount: 1, StorageNodeCount: 5, UplinkCount: 1,
	}, func(t *testing.T, ctx *testcontext.Context, planet *testplanet.Planet) {
		satellite := planet.Satellites[0]
		audits := satellite.Audit
		audits.Worker.Loop.Pause()
		audits.Chore.Loop.Pause()

		ul := planet.Uplinks[0]

		// Upload 2 remote files with 1 segment.
		for i := 0; i < 2; i++ {
			testData := testrand.Bytes(8 * memory.KiB)
			path := "/some/remote/path/" + strconv.Itoa(i)
			err := ul.Upload(ctx, satellite, "testbucket", path, testData)
			require.NoError(t, err)
		}

		audits.Chore.Loop.TriggerWait()
		queue := audits.Queues.Fetch()
		require.EqualValues(t, 2, queue.Size(), "audit queue")

		uniqueSegments := make(map[audit.Segment]struct{})
		var err error
		var segment audit.Segment
		var segmentCount int
		for {
			segment, err = queue.Next()
			if err != nil {
				break
			}
			segmentCount++
			_, ok := uniqueSegments[segment]
			require.False(t, ok, "expected unique segment in chore queue")

			uniqueSegments[segment] = struct{}{}
		}
		require.True(t, audit.ErrEmptyQueue.Has(err))
		require.Equal(t, 2, segmentCount)
		require.Equal(t, 0, queue.Size())

		// Repopulate the queue for the worker.
		audits.Chore.Loop.TriggerWait()

		// Make sure the worker processes the audit queue.
		audits.Worker.Loop.TriggerWait()
		queue = audits.Queues.Fetch()
		require.EqualValues(t, 0, queue.Size(), "audit queue")
	})
}
