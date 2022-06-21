// Copyright (C) 2019 Storj Labs, Inc.
// See LICENSE for copying information.

package overlay_test

import (
	"context"
	"fmt"
	"reflect"
	"sort"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
	"go.uber.org/zap/zaptest"

	"storj.io/common/memory"
	"storj.io/common/pb"
	"storj.io/common/storj"
	"storj.io/common/testcontext"
	"storj.io/common/testrand"
	"storj.io/storj/private/testplanet"
	"storj.io/storj/satellite"
	"storj.io/storj/satellite/overlay"
	"storj.io/storj/satellite/reputation"
	"storj.io/storj/satellite/satellitedb/satellitedbtest"
)

func TestCache_Database(t *testing.T) {
	t.Parallel()

	satellitedbtest.Run(t, func(ctx *testcontext.Context, t *testing.T, db satellite.DB) {
		testCache(ctx, t, db.OverlayCache())
	})
}

// returns a NodeSelectionConfig with sensible test values.
func testNodeSelectionConfig(newNodeFraction float64, distinctIP bool) overlay.NodeSelectionConfig {
	return overlay.NodeSelectionConfig{
		NewNodeFraction: newNodeFraction,
		OnlineWindow:    time.Hour,
		DistinctIP:      distinctIP,
	}
}

// returns an AuditHistoryConfig with sensible test values.
func testAuditHistoryConfig() reputation.AuditHistoryConfig {
	return reputation.AuditHistoryConfig{
		WindowSize:       time.Hour,
		TrackingPeriod:   time.Hour,
		GracePeriod:      time.Hour,
		OfflineThreshold: 0,
	}
}

func testCache(ctx context.Context, t *testing.T, store overlay.DB) {
	valid1ID := testrand.NodeID()
	valid2ID := testrand.NodeID()
	valid3ID := testrand.NodeID()
	missingID := testrand.NodeID()
	address := &pb.NodeAddress{Address: "127.0.0.1:0"}
	lastNet := "127.0.0"

	nodeSelectionConfig := testNodeSelectionConfig(0, false)
	serviceConfig := overlay.Config{Node: nodeSelectionConfig, UpdateStatsBatchSize: 100}
	service, err := overlay.NewService(zaptest.NewLogger(t), store, serviceConfig)
	require.NoError(t, err)
	d := overlay.NodeCheckInInfo{
		Address:    address,
		LastIPPort: address.Address,
		LastNet:    lastNet,
		Version:    &pb.NodeVersion{Version: "v1.0.0"},
		IsUp:       true,
	}
	{ // Put
		d.NodeID = valid1ID
		err := store.UpdateCheckIn(ctx, d, time.Now().UTC(), nodeSelectionConfig)
		require.NoError(t, err)

		d.NodeID = valid2ID
		err = store.UpdateCheckIn(ctx, d, time.Now().UTC(), nodeSelectionConfig)
		require.NoError(t, err)

		d.NodeID = valid3ID
		err = store.UpdateCheckIn(ctx, d, time.Now().UTC(), nodeSelectionConfig)
		require.NoError(t, err)
		// disqualify one node
		err = service.DisqualifyNode(ctx, valid3ID, overlay.DisqualificationReasonUnknown)
		require.NoError(t, err)
	}

	{ // Invalid shouldn't cause a panic.
		validInfo := func() overlay.NodeCheckInInfo {
			return overlay.NodeCheckInInfo{
				Address:    address,
				LastIPPort: address.Address,
				LastNet:    lastNet,
				Version: &pb.NodeVersion{
					Version:    "v1.0.0",
					CommitHash: "alpha",
				},
				IsUp: true,
				Operator: &pb.NodeOperator{
					Email:          "\x00",
					Wallet:         "0x1234",
					WalletFeatures: []string{"zerog"},
				},
			}
		}

		// Currently Postgres returns an error and CockroachDB doesn't return
		// an error for a non-utf text field.

		d := validInfo()
		d.Operator.Email = "\x00"
		_ = store.UpdateCheckIn(ctx, d, time.Now().UTC(), nodeSelectionConfig)

		d = validInfo()
		d.Operator.Wallet = "\x00"
		_ = store.UpdateCheckIn(ctx, d, time.Now().UTC(), nodeSelectionConfig)

		d = validInfo()
		d.Operator.WalletFeatures[0] = "\x00"
		_ = store.UpdateCheckIn(ctx, d, time.Now().UTC(), nodeSelectionConfig)

		d = validInfo()
		d.Version.CommitHash = "\x00"
		_ = store.UpdateCheckIn(ctx, d, time.Now().UTC(), nodeSelectionConfig)
	}

	{ // Get
		_, err := service.Get(ctx, storj.NodeID{})
		require.Error(t, err)
		require.Equal(t, overlay.ErrEmptyNode, err)

		valid1, err := service.Get(ctx, valid1ID)
		require.NoError(t, err)
		require.Equal(t, valid1.Id, valid1ID)

		valid2, err := service.Get(ctx, valid2ID)
		require.NoError(t, err)
		require.Equal(t, valid2.Id, valid2ID)

		invalid2, err := service.Get(ctx, missingID)
		require.Error(t, err)
		require.True(t, overlay.ErrNodeNotFound.Has(err))
		require.Nil(t, invalid2)

		// TODO: add erroring database test
	}
}

func TestRandomizedSelection(t *testing.T) {
	t.Parallel()

	totalNodes := 1000
	selectIterations := 100
	numNodesToSelect := 100
	minSelectCount := 3 // TODO: compute this limit better

	satellitedbtest.Run(t, func(ctx *testcontext.Context, t *testing.T, db satellite.DB) {
		cache := db.OverlayCache()
		allIDs := make(storj.NodeIDList, totalNodes)
		nodeCounts := make(map[storj.NodeID]int)

		// put nodes in cache
		for i := 0; i < totalNodes; i++ {
			newID := testrand.NodeID()
			addr := fmt.Sprintf("127.0.%d.0:8080", i)
			lastNet := fmt.Sprintf("127.0.%d", i)
			d := overlay.NodeCheckInInfo{
				NodeID:     newID,
				Address:    &pb.NodeAddress{Address: addr, Transport: pb.NodeTransport_TCP_TLS_GRPC},
				LastIPPort: addr,
				LastNet:    lastNet,
				Version:    &pb.NodeVersion{Version: "v1.0.0"},
				Capacity:   &pb.NodeCapacity{},
				IsUp:       true,
			}
			err := cache.UpdateCheckIn(ctx, d, time.Now().UTC(), overlay.NodeSelectionConfig{})
			require.NoError(t, err)

			if i%2 == 0 { // make half of nodes "new" and half "vetted"
				_, err = cache.TestVetNode(ctx, newID)
				require.NoError(t, err)
			}

			allIDs[i] = newID
			nodeCounts[newID] = 0
		}

		// select numNodesToSelect nodes selectIterations times
		for i := 0; i < selectIterations; i++ {
			var nodes []*overlay.SelectedNode
			var err error

			if i%2 == 0 {
				nodes, err = cache.SelectStorageNodes(ctx, numNodesToSelect, 0, &overlay.NodeCriteria{
					OnlineWindow: time.Hour,
				})
				require.NoError(t, err)
			} else {
				nodes, err = cache.SelectStorageNodes(ctx, numNodesToSelect, numNodesToSelect, &overlay.NodeCriteria{
					OnlineWindow: time.Hour,
				})
				require.NoError(t, err)
			}
			require.Len(t, nodes, numNodesToSelect)

			for _, node := range nodes {
				nodeCounts[node.ID]++
			}
		}

		belowThreshold := 0

		table := []int{}

		// expect that each node has been selected at least minSelectCount times
		for _, id := range allIDs {
			count := nodeCounts[id]
			if count < minSelectCount {
				belowThreshold++
			}
			if count >= len(table) {
				table = append(table, make([]int, count-len(table)+1)...)
			}
			table[count]++
		}

		if belowThreshold > totalNodes*1/100 {
			t.Errorf("%d out of %d were below threshold %d", belowThreshold, totalNodes, minSelectCount)
			for count, amount := range table {
				t.Logf("%3d = %4d", count, amount)
			}
		}
	})
}
func TestRandomizedSelectionCache(t *testing.T) {
	t.Parallel()

	totalNodes := 1000
	selectIterations := 100
	numNodesToSelect := 100
	minSelectCount := 3

	testplanet.Run(t, testplanet.Config{
		SatelliteCount: 1, StorageNodeCount: 0, UplinkCount: 0,
		Reconfigure: testplanet.Reconfigure{
			Satellite: func(log *zap.Logger, index int, config *satellite.Config) {
				config.Overlay.NodeSelectionCache.Staleness = -time.Hour
				config.Overlay.Node.NewNodeFraction = 0.5 // select 50% new nodes
				config.Reputation.AuditCount = 1
				config.Reputation.AuditLambda = 1
				config.Reputation.AuditWeight = 1
				config.Reputation.AuditDQ = 0.5
				config.Reputation.AuditHistory = testAuditHistoryConfig()
			},
		},
	}, func(t *testing.T, ctx *testcontext.Context, planet *testplanet.Planet) {
		satellite := planet.Satellites[0]
		overlaydb := satellite.Overlay.DB
		uploadSelectionCache := satellite.Overlay.Service.UploadSelectionCache
		allIDs := make(storj.NodeIDList, totalNodes)
		nodeCounts := make(map[storj.NodeID]int)
		expectedNewCount := int(float64(totalNodes) * satellite.Config.Overlay.Node.NewNodeFraction)

		// put nodes in cache
		for i := 0; i < totalNodes; i++ {
			newID := testrand.NodeID()
			address := fmt.Sprintf("127.0.%d.0:8080", i)
			lastNet := fmt.Sprintf("127.0.%d", i)

			n := overlay.NodeCheckInInfo{
				NodeID: newID,
				Address: &pb.NodeAddress{
					Address:   address,
					Transport: pb.NodeTransport_TCP_TLS_GRPC,
				},
				LastNet:    lastNet,
				LastIPPort: address,
				IsUp:       true,
				Capacity: &pb.NodeCapacity{
					FreeDisk: 200 * memory.MiB.Int64(),
				},
				Version: &pb.NodeVersion{
					Version:    "v1.1.0",
					CommitHash: "",
					Timestamp:  time.Time{},
					Release:    true,
				},
			}
			defaults := overlay.NodeSelectionConfig{}
			err := overlaydb.UpdateCheckIn(ctx, n, time.Now().UTC(), defaults)
			require.NoError(t, err)

			if i%2 == 0 {
				// make half of nodes "new" and half "vetted"
				_, err = overlaydb.TestVetNode(ctx, newID)
				require.NoError(t, err)
			}

			allIDs[i] = newID
			nodeCounts[newID] = 0
		}

		err := uploadSelectionCache.Refresh(ctx)
		require.NoError(t, err)
		reputable, new := uploadSelectionCache.Size()
		require.Equal(t, totalNodes-expectedNewCount, reputable)
		require.Equal(t, expectedNewCount, new)

		// select numNodesToSelect nodes selectIterations times
		for i := 0; i < selectIterations; i++ {
			var nodes []*overlay.SelectedNode
			var err error
			req := overlay.FindStorageNodesRequest{
				RequestedCount: numNodesToSelect,
			}

			nodes, err = uploadSelectionCache.GetNodes(ctx, req)
			require.NoError(t, err)
			require.Len(t, nodes, numNodesToSelect)

			for _, node := range nodes {
				nodeCounts[node.ID]++
			}
		}

		belowThreshold := 0

		table := []int{}

		// expect that each node has been selected at least minSelectCount times
		for _, id := range allIDs {
			count := nodeCounts[id]
			if count < minSelectCount {
				belowThreshold++
			}
			if count >= len(table) {
				table = append(table, make([]int, count-len(table)+1)...)
			}
			table[count]++
		}

		if belowThreshold > totalNodes*1/100 {
			t.Errorf("%d out of %d were below threshold %d", belowThreshold, totalNodes, minSelectCount)
			for count, amount := range table {
				t.Logf("%3d = %4d", count, amount)
			}
		}
	})
}

func TestNodeInfo(t *testing.T) {
	testplanet.Run(t, testplanet.Config{
		SatelliteCount: 1, StorageNodeCount: 1, UplinkCount: 0,
	}, func(t *testing.T, ctx *testcontext.Context, planet *testplanet.Planet) {
		planet.StorageNodes[0].Storage2.Monitor.Loop.Pause()

		node, err := planet.Satellites[0].Overlay.Service.Get(ctx, planet.StorageNodes[0].ID())
		require.NoError(t, err)

		dossier := planet.StorageNodes[0].Contact.Service.Local()

		assert.Equal(t, pb.NodeType_STORAGE, node.Type)
		assert.NotEmpty(t, node.Operator.Email)
		assert.NotEmpty(t, node.Operator.Wallet)
		assert.Equal(t, dossier.Operator, node.Operator)
		assert.NotEmpty(t, node.Capacity.FreeDisk)
		assert.Equal(t, dossier.Capacity, node.Capacity)
		assert.NotEmpty(t, node.Version.Version)
		assert.Equal(t, dossier.Version.Version, node.Version.Version)
	})
}

func TestGetOnlineNodesForGetDelete(t *testing.T) {
	testplanet.Run(t, testplanet.Config{
		SatelliteCount: 1, StorageNodeCount: 2, UplinkCount: 0,
	}, func(t *testing.T, ctx *testcontext.Context, planet *testplanet.Planet) {
		// pause chores that might update node data
		planet.Satellites[0].Audit.Chore.Loop.Pause()
		planet.Satellites[0].Repair.Checker.Loop.Pause()
		planet.Satellites[0].Repair.Repairer.Loop.Pause()
		for _, node := range planet.StorageNodes {
			node.Contact.Chore.Pause(ctx)
		}

		// should not return anything if nodeIDs aren't in the nodes table
		actualNodes, err := planet.Satellites[0].Overlay.Service.GetOnlineNodesForGetDelete(ctx, []storj.NodeID{})
		require.NoError(t, err)
		require.Equal(t, 0, len(actualNodes))
		actualNodes, err = planet.Satellites[0].Overlay.Service.GetOnlineNodesForGetDelete(ctx, []storj.NodeID{testrand.NodeID()})
		require.NoError(t, err)
		require.Equal(t, 0, len(actualNodes))

		expectedNodes := make(map[storj.NodeID]*overlay.SelectedNode, len(planet.StorageNodes))
		nodeIDs := make([]storj.NodeID, len(planet.StorageNodes)+1)
		for i, node := range planet.StorageNodes {
			nodeIDs[i] = node.ID()
			dossier, err := planet.Satellites[0].Overlay.Service.Get(ctx, node.ID())
			require.NoError(t, err)
			expectedNodes[dossier.Id] = &overlay.SelectedNode{
				ID:         dossier.Id,
				Address:    dossier.Address,
				LastNet:    dossier.LastNet,
				LastIPPort: dossier.LastIPPort,
			}
		}
		// add a fake node ID to make sure GetOnlineNodesForGetDelete doesn't error and still returns the expected nodes.
		nodeIDs[len(planet.StorageNodes)] = testrand.NodeID()

		actualNodes, err = planet.Satellites[0].Overlay.Service.GetOnlineNodesForGetDelete(ctx, nodeIDs)
		require.NoError(t, err)

		require.True(t, reflect.DeepEqual(expectedNodes, actualNodes))
	})
}

func TestKnownReliable(t *testing.T) {
	testplanet.Run(t, testplanet.Config{
		SatelliteCount: 1, StorageNodeCount: 6, UplinkCount: 1,
		Reconfigure: testplanet.Reconfigure{
			Satellite: func(log *zap.Logger, index int, config *satellite.Config) {
				config.Reputation.AuditHistory = reputation.AuditHistoryConfig{
					WindowSize:               time.Hour,
					TrackingPeriod:           2 * time.Hour,
					GracePeriod:              time.Hour,
					OfflineThreshold:         0.6,
					OfflineDQEnabled:         false,
					OfflineSuspensionEnabled: true,
				}
			},
		},
	}, func(t *testing.T, ctx *testcontext.Context, planet *testplanet.Planet) {
		satellite := planet.Satellites[0]
		service := satellite.Overlay.Service
		oc := satellite.DB.OverlayCache()

		// Disqualify storage node #0
		err := oc.DisqualifyNode(ctx, planet.StorageNodes[0].ID(), time.Now().UTC(), overlay.DisqualificationReasonUnknown)
		require.NoError(t, err)

		// Stop storage node #1
		offlineNode := planet.StorageNodes[1]
		err = planet.StopPeer(offlineNode)
		require.NoError(t, err)
		// set last contact success to 1 hour ago to make node appear offline
		checkInInfo := getNodeInfo(offlineNode.ID())
		err = service.UpdateCheckIn(ctx, checkInInfo, time.Now().Add(-time.Hour))
		require.NoError(t, err)
		// Check that storage node #1 is offline
		node, err := service.Get(ctx, offlineNode.ID())
		require.NoError(t, err)
		require.False(t, service.IsOnline(node))

		// unknown audit suspend storage node #2
		err = oc.TestSuspendNodeUnknownAudit(ctx, planet.StorageNodes[2].ID(), time.Now())
		require.NoError(t, err)

		// offline suspend storage node #3
		err = oc.TestSuspendNodeOffline(ctx, planet.StorageNodes[3].ID(), time.Now())
		require.NoError(t, err)

		// Check that only storage nodes #4 and #5 are reliable
		result, err := service.KnownReliable(ctx, []storj.NodeID{
			planet.StorageNodes[0].ID(),
			planet.StorageNodes[1].ID(),
			planet.StorageNodes[2].ID(),
			planet.StorageNodes[3].ID(),
			planet.StorageNodes[4].ID(),
			planet.StorageNodes[5].ID(),
		})
		require.NoError(t, err)
		require.Len(t, result, 2)

		// Sort the storage nodes for predictable checks
		expectedReliable := []storj.NodeURL{
			planet.StorageNodes[4].NodeURL(),
			planet.StorageNodes[5].NodeURL(),
		}
		sort.Slice(expectedReliable, func(i, j int) bool { return expectedReliable[i].ID.Less(expectedReliable[j].ID) })
		sort.Slice(result, func(i, j int) bool { return result[i].Id.Less(result[j].Id) })

		// Assert the reliable nodes are the expected ones
		for i, node := range result {
			assert.Equal(t, expectedReliable[i].ID, node.Id)
			assert.Equal(t, expectedReliable[i].Address, node.Address.Address)
		}
	})
}

func TestUpdateCheckIn(t *testing.T) {
	satellitedbtest.Run(t, func(ctx *testcontext.Context, t *testing.T, db satellite.DB) { // setup
		nodeID := storj.NodeID{1, 2, 3}
		expectedEmail := "test@email.test"
		expectedAddress := "1.2.4.4:8080"
		info := overlay.NodeCheckInInfo{
			NodeID: nodeID,
			Address: &pb.NodeAddress{
				Address: expectedAddress,
			},
			IsUp: true,
			Capacity: &pb.NodeCapacity{
				FreeDisk: int64(5678),
			},
			Operator: &pb.NodeOperator{
				Email:          expectedEmail,
				Wallet:         "0x123",
				WalletFeatures: []string{"example"},
			},
			Version: &pb.NodeVersion{
				Version:    "v0.0.0",
				CommitHash: "",
				Timestamp:  time.Time{},
				Release:    false,
			},
			LastIPPort: expectedAddress,
			LastNet:    "1.2.4",
		}
		expectedNode := &overlay.NodeDossier{
			Node: pb.Node{
				Id: nodeID,
				Address: &pb.NodeAddress{
					Address:   info.Address.GetAddress(),
					Transport: pb.NodeTransport_TCP_TLS_GRPC,
				},
			},
			Type: pb.NodeType_STORAGE,
			Operator: pb.NodeOperator{
				Email:          info.Operator.GetEmail(),
				Wallet:         info.Operator.GetWallet(),
				WalletFeatures: info.Operator.GetWalletFeatures(),
			},
			Capacity: pb.NodeCapacity{
				FreeDisk: info.Capacity.GetFreeDisk(),
			},
			Version: pb.NodeVersion{
				Version:    "v0.0.0",
				CommitHash: "",
				Timestamp:  time.Time{},
				Release:    false,
			},
			Contained:    false,
			Disqualified: nil,
			PieceCount:   0,
			ExitStatus:   overlay.ExitStatus{NodeID: nodeID},
			LastIPPort:   expectedAddress,
			LastNet:      "1.2.4",
		}

		// confirm the node doesn't exist in nodes table yet
		_, err := db.OverlayCache().Get(ctx, nodeID)
		require.Error(t, err)
		require.Contains(t, err.Error(), "node not found")

		// check-in for that node id, which should add the node
		// to the nodes tables in the database
		startOfTest := time.Now()
		err = db.OverlayCache().UpdateCheckIn(ctx, info, startOfTest.Add(time.Second), overlay.NodeSelectionConfig{})
		require.NoError(t, err)

		// confirm that the node is now in the nodes table with the
		// correct fields set
		actualNode, err := db.OverlayCache().Get(ctx, nodeID)
		require.NoError(t, err)
		require.True(t, actualNode.Reputation.LastContactSuccess.After(startOfTest))
		require.True(t, actualNode.Reputation.LastContactFailure.UTC().Equal(time.Time{}.UTC()))
		actualNode.Address = expectedNode.Address

		// we need to overwrite the times so that the deep equal considers them the same
		expectedNode.Reputation.LastContactSuccess = actualNode.Reputation.LastContactSuccess
		expectedNode.Reputation.LastContactFailure = actualNode.Reputation.LastContactFailure
		expectedNode.Version.Timestamp = actualNode.Version.Timestamp
		expectedNode.CreatedAt = actualNode.CreatedAt
		require.Equal(t, expectedNode, actualNode)

		// confirm that we can update the address field
		startOfUpdateTest := time.Now()
		expectedAddress = "9.8.7.6"
		updatedInfo := overlay.NodeCheckInInfo{
			NodeID: nodeID,
			Address: &pb.NodeAddress{
				Address: expectedAddress,
			},
			IsUp: true,
			Version: &pb.NodeVersion{
				Version:    "v0.1.0",
				CommitHash: "abc123",
				Timestamp:  time.Now(),
				Release:    true,
			},
			LastIPPort: expectedAddress,
			LastNet:    "9.8.7",
		}
		// confirm that the updated node is in the nodes table with the
		// correct updated fields set
		err = db.OverlayCache().UpdateCheckIn(ctx, updatedInfo, startOfUpdateTest.Add(time.Second), overlay.NodeSelectionConfig{})
		require.NoError(t, err)
		updatedNode, err := db.OverlayCache().Get(ctx, nodeID)
		require.NoError(t, err)
		require.True(t, updatedNode.Reputation.LastContactSuccess.After(startOfUpdateTest))
		require.True(t, updatedNode.Reputation.LastContactFailure.Equal(time.Time{}))
		require.Equal(t, updatedNode.Address.GetAddress(), expectedAddress)
		require.Equal(t, updatedInfo.Version.GetVersion(), updatedNode.Version.GetVersion())
		require.Equal(t, updatedInfo.Version.GetCommitHash(), updatedNode.Version.GetCommitHash())
		require.Equal(t, updatedInfo.Version.GetRelease(), updatedNode.Version.GetRelease())
		require.True(t, updatedNode.Version.GetTimestamp().After(info.Version.GetTimestamp()))

		// confirm we can udpate IsUp field
		startOfUpdateTest2 := time.Now()
		updatedInfo2 := overlay.NodeCheckInInfo{
			NodeID: nodeID,
			Address: &pb.NodeAddress{
				Address: "9.8.7.6",
			},
			IsUp: false,
			Version: &pb.NodeVersion{
				Version:    "v0.0.0",
				CommitHash: "",
				Timestamp:  time.Time{},
				Release:    false,
			},
		}

		err = db.OverlayCache().UpdateCheckIn(ctx, updatedInfo2, startOfUpdateTest2.Add(time.Second), overlay.NodeSelectionConfig{})
		require.NoError(t, err)
		updated2Node, err := db.OverlayCache().Get(ctx, nodeID)
		require.NoError(t, err)
		require.True(t, updated2Node.Reputation.LastContactSuccess.Equal(updatedNode.Reputation.LastContactSuccess))
		require.True(t, updated2Node.Reputation.LastContactFailure.After(startOfUpdateTest2))
	})
}

// TestSuspendedSelection ensures that suspended nodes are not selected by SelectStorageNodes.
func TestSuspendedSelection(t *testing.T) {
	totalNodes := 10

	satellitedbtest.Run(t, func(ctx *testcontext.Context, t *testing.T, db satellite.DB) {
		cache := db.OverlayCache()
		suspendedIDs := make(map[storj.NodeID]bool)
		config := overlay.NodeSelectionConfig{}

		// put nodes in cache
		for i := 0; i < totalNodes; i++ {
			newID := testrand.NodeID()
			addr := fmt.Sprintf("127.0.%d.0:8080", i)
			lastNet := fmt.Sprintf("127.0.%d", i)
			d := overlay.NodeCheckInInfo{
				NodeID:     newID,
				Address:    &pb.NodeAddress{Address: addr, Transport: pb.NodeTransport_TCP_TLS_GRPC},
				LastIPPort: addr,
				LastNet:    lastNet,
				Version:    &pb.NodeVersion{Version: "v1.0.0"},
				Capacity:   &pb.NodeCapacity{},
				IsUp:       true,
			}
			err := cache.UpdateCheckIn(ctx, d, time.Now().UTC(), config)
			require.NoError(t, err)

			if i%2 == 0 { // make half of nodes "new" and half "vetted"
				_, err = cache.TestVetNode(ctx, newID)
				require.NoError(t, err)
			}

			// suspend the first four nodes (2 new, 2 vetted)
			// 2 offline suspended and 2 unknown audit suspended
			if i < 4 {
				if i < 2 {
					err = cache.TestSuspendNodeOffline(ctx, newID, time.Now())
					require.NoError(t, err)
					continue
				}
				err = cache.TestSuspendNodeUnknownAudit(ctx, newID, time.Now())
				require.NoError(t, err)
				suspendedIDs[newID] = true
			}
		}

		var nodes []*overlay.SelectedNode
		var err error

		numNodesToSelect := 10

		// select 10 vetted nodes - 5 vetted, 2 suspended, so expect 3
		nodes, err = cache.SelectStorageNodes(ctx, numNodesToSelect, 0, &overlay.NodeCriteria{
			OnlineWindow: time.Hour,
		})
		require.NoError(t, err)
		require.Len(t, nodes, 3)
		for _, node := range nodes {
			require.False(t, suspendedIDs[node.ID])
		}

		// select 10 new nodes - 5 new, 2 suspended, so expect 3
		nodes, err = cache.SelectStorageNodes(ctx, numNodesToSelect, numNodesToSelect, &overlay.NodeCriteria{
			OnlineWindow: time.Hour,
		})
		require.NoError(t, err)
		require.Len(t, nodes, 3)
		for _, node := range nodes {
			require.False(t, suspendedIDs[node.ID])
		}
	})
}

func TestUpdateReputation(t *testing.T) {
	testplanet.Run(t, testplanet.Config{
		SatelliteCount: 1, StorageNodeCount: 1, UplinkCount: 0,
	}, func(t *testing.T, ctx *testcontext.Context, planet *testplanet.Planet) {
		service := planet.Satellites[0].Overlay.Service
		overlaydb := planet.Satellites[0].Overlay.DB
		node := planet.StorageNodes[0]

		info, err := service.Get(ctx, node.ID())
		require.NoError(t, err)
		require.Nil(t, info.Disqualified)
		require.Nil(t, info.UnknownAuditSuspended)
		require.Nil(t, info.OfflineSuspended)
		require.Nil(t, info.Reputation.Status.VettedAt)

		t0 := time.Now().Truncate(time.Hour)
		t1 := t0.Add(time.Hour)
		t2 := t0.Add(2 * time.Hour)
		t3 := t0.Add(3 * time.Hour)

		reputationChange := overlay.ReputationUpdate{
			Disqualified:          nil,
			UnknownAuditSuspended: &t1,
			OfflineSuspended:      &t2,
			VettedAt:              &t3,
		}
		err = service.UpdateReputation(ctx, node.ID(), reputationChange)
		require.NoError(t, err)

		info, err = service.Get(ctx, node.ID())
		require.NoError(t, err)
		require.Equal(t, reputationChange.Disqualified, info.Disqualified)
		require.Equal(t, reputationChange.UnknownAuditSuspended, info.UnknownAuditSuspended)
		require.Equal(t, reputationChange.OfflineSuspended, info.OfflineSuspended)
		require.Equal(t, reputationChange.VettedAt, info.Reputation.Status.VettedAt)

		reputationChange.Disqualified = &t0

		err = service.UpdateReputation(ctx, node.ID(), reputationChange)
		require.NoError(t, err)

		info, err = service.Get(ctx, node.ID())
		require.NoError(t, err)
		require.Equal(t, reputationChange.Disqualified, info.Disqualified)

		nodeInfo, err := overlaydb.UpdateExitStatus(ctx, &overlay.ExitStatusRequest{
			NodeID:              node.ID(),
			ExitInitiatedAt:     t0,
			ExitLoopCompletedAt: t1,
			ExitFinishedAt:      t1,
			ExitSuccess:         true,
		})
		require.NoError(t, err)
		require.NotNil(t, nodeInfo.ExitStatus.ExitFinishedAt)

		// make sure Disqualified field is not updated if a node has finished
		// graceful exit
		reputationChange.Disqualified = &t0
		err = service.UpdateReputation(ctx, node.ID(), reputationChange)
		require.NoError(t, err)

		exitedNodeInfo, err := service.Get(ctx, node.ID())
		require.NoError(t, err)
		require.Equal(t, info.Disqualified, exitedNodeInfo.Disqualified)
	})
}

func getNodeInfo(nodeID storj.NodeID) overlay.NodeCheckInInfo {
	return overlay.NodeCheckInInfo{
		NodeID: nodeID,
		IsUp:   true,
		Address: &pb.NodeAddress{
			Address: "1.2.3.4",
		},
		Operator: &pb.NodeOperator{
			Email:  "test@email.test",
			Wallet: "0x123",
		},
		Version: &pb.NodeVersion{
			Version:    "v0.0.0",
			CommitHash: "",
			Timestamp:  time.Time{},
			Release:    false,
		},
	}
}

func TestVetAndUnvetNode(t *testing.T) {
	testplanet.Run(t, testplanet.Config{
		SatelliteCount: 1, StorageNodeCount: 2, UplinkCount: 0,
	}, func(t *testing.T, ctx *testcontext.Context, planet *testplanet.Planet) {
		service := planet.Satellites[0].Overlay.Service
		node := planet.StorageNodes[0]

		// clear existing data
		err := service.TestUnvetNode(ctx, node.ID())
		require.NoError(t, err)
		dossier, err := service.Get(ctx, node.ID())
		require.NoError(t, err)
		require.Nil(t, dossier.Reputation.Status.VettedAt)

		// vet again
		vettedTime, err := service.TestVetNode(ctx, node.ID())
		require.NoError(t, err)
		require.NotNil(t, vettedTime)
		dossier, err = service.Get(ctx, node.ID())
		require.NoError(t, err)
		require.NotNil(t, dossier.Reputation.Status.VettedAt)

		// unvet again
		err = service.TestUnvetNode(ctx, node.ID())
		require.NoError(t, err)
		dossier, err = service.Get(ctx, node.ID())
		require.NoError(t, err)
		require.Nil(t, dossier.Reputation.Status.VettedAt)
	})
}

func TestReliable(t *testing.T) {
	testplanet.Run(t, testplanet.Config{
		SatelliteCount: 1, StorageNodeCount: 2, UplinkCount: 0,
	}, func(t *testing.T, ctx *testcontext.Context, planet *testplanet.Planet) {
		service := planet.Satellites[0].Overlay.Service
		node := planet.StorageNodes[0]

		nodes, err := service.Reliable(ctx)
		require.NoError(t, err)
		require.Len(t, nodes, 2)

		err = planet.Satellites[0].Overlay.Service.TestNodeCountryCode(ctx, node.ID(), "FR")
		require.NoError(t, err)

		// first node should be excluded from Reliable result because of country code
		nodes, err = service.Reliable(ctx)
		require.NoError(t, err)
		require.Len(t, nodes, 1)
		require.NotEqual(t, node.ID(), nodes[0])
	})
}

func TestKnownReliableInExcludedCountries(t *testing.T) {
	testplanet.Run(t, testplanet.Config{
		SatelliteCount: 1, StorageNodeCount: 2, UplinkCount: 0,
	}, func(t *testing.T, ctx *testcontext.Context, planet *testplanet.Planet) {
		service := planet.Satellites[0].Overlay.Service
		node := planet.StorageNodes[0]

		nodes, err := service.Reliable(ctx)
		require.NoError(t, err)
		require.Len(t, nodes, 2)

		err = planet.Satellites[0].Overlay.Service.TestNodeCountryCode(ctx, node.ID(), "FR")
		require.NoError(t, err)

		// first node should be excluded from Reliable result because of country code
		nodes, err = service.KnownReliableInExcludedCountries(ctx, nodes)
		require.NoError(t, err)
		require.Len(t, nodes, 1)
		require.Equal(t, node.ID(), nodes[0])
	})
}
