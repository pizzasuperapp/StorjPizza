// Copyright (C) 2019 Storj Labs, Inc.
// See LICENSE for copying information.

package gracefulexit

import (
	"context"
	"database/sql"
	"time"

	"github.com/zeebo/errs"
	"go.uber.org/zap"

	"storj.io/common/storj"
	"storj.io/common/sync2"
	"storj.io/storj/satellite/metabase/segmentloop"
	"storj.io/storj/satellite/overlay"
)

// Chore populates the graceful exit transfer queue.
//
// architecture: Chore
type Chore struct {
	log         *zap.Logger
	Loop        *sync2.Cycle
	db          DB
	config      Config
	overlay     overlay.DB
	segmentLoop *segmentloop.Service
}

// NewChore instantiates Chore.
func NewChore(log *zap.Logger, db DB, overlay overlay.DB, segmentLoop *segmentloop.Service, config Config) *Chore {
	return &Chore{
		log:         log,
		Loop:        sync2.NewCycle(config.ChoreInterval),
		db:          db,
		config:      config,
		overlay:     overlay,
		segmentLoop: segmentLoop,
	}
}

// Run starts the chore.
func (chore *Chore) Run(ctx context.Context) (err error) {
	defer mon.Task()(&ctx)(&err)
	return chore.Loop.Run(ctx, func(ctx context.Context) (err error) {
		defer mon.Task()(&ctx)(&err)

		exitingNodes, err := chore.overlay.GetExitingNodes(ctx)
		if err != nil {
			chore.log.Error("error retrieving nodes that have not finished exiting", zap.Error(err))
			return nil
		}

		nodeCount := len(exitingNodes)
		if nodeCount == 0 {
			return nil
		}
		chore.log.Debug("found exiting nodes", zap.Int("exitingNodes", nodeCount))

		exitingNodesLoopIncomplete := make(storj.NodeIDList, 0, nodeCount)
		for _, node := range exitingNodes {
			if node.ExitLoopCompletedAt == nil {
				exitingNodesLoopIncomplete = append(exitingNodesLoopIncomplete, node.NodeID)
				continue
			}

			progress, err := chore.db.GetProgress(ctx, node.NodeID)
			if err != nil && !errs.Is(err, sql.ErrNoRows) {
				chore.log.Error("error retrieving progress for node", zap.Stringer("Node ID", node.NodeID), zap.Error(err))
				continue
			}

			lastActivityTime := *node.ExitLoopCompletedAt
			if progress != nil {
				lastActivityTime = progress.UpdatedAt
			}

			// check inactive timeframe
			if lastActivityTime.Add(chore.config.MaxInactiveTimeFrame).Before(time.Now().UTC()) {
				exitStatusRequest := &overlay.ExitStatusRequest{
					NodeID:         node.NodeID,
					ExitSuccess:    false,
					ExitFinishedAt: time.Now().UTC(),
				}
				mon.Meter("graceful_exit_fail_inactive").Mark(1)
				_, err = chore.overlay.UpdateExitStatus(ctx, exitStatusRequest)
				if err != nil {
					chore.log.Error("error updating exit status", zap.Error(err))
					continue
				}

				// remove all items from the transfer queue
				err := chore.db.DeleteTransferQueueItems(ctx, node.NodeID)
				if err != nil {
					chore.log.Error("error deleting node from transfer queue", zap.Error(err))
				}
			}
		}

		// Populate transfer queue for nodes that have not completed the exit loop yet
		pathCollector := NewPathCollector(chore.db, exitingNodesLoopIncomplete, chore.log, chore.config.ChoreBatchSize)
		err = chore.segmentLoop.Join(ctx, pathCollector)
		if err != nil {
			chore.log.Error("error joining segment loop.", zap.Error(err))
			return nil
		}

		err = pathCollector.Flush(ctx)
		if err != nil {
			chore.log.Error("error flushing collector buffer.", zap.Error(err))
			return nil
		}

		now := time.Now().UTC()
		for _, nodeID := range exitingNodesLoopIncomplete {
			exitStatus := overlay.ExitStatusRequest{
				NodeID:              nodeID,
				ExitLoopCompletedAt: now,
			}
			_, err = chore.overlay.UpdateExitStatus(ctx, &exitStatus)
			if err != nil {
				chore.log.Error("error updating exit status.", zap.Error(err))
			}

			bytesToTransfer := pathCollector.nodeIDStorage[nodeID]
			mon.IntVal("graceful_exit_init_bytes_stored").Observe(bytesToTransfer)
		}
		return nil
	})
}

// Close closes chore.
func (chore *Chore) Close() error {
	chore.Loop.Close()
	return nil
}
