// Copyright (C) 2020 Storj Labs, Inc.
// See LICENSE for copying information.

package gracefulexit

import (
	"context"
	"time"

	"github.com/zeebo/errs"
	"go.uber.org/zap"

	"storj.io/common/errs2"
	"storj.io/common/pb"
	"storj.io/common/rpc"
	"storj.io/common/storj"
	"storj.io/storj/storagenode/pieces"
	"storj.io/storj/storagenode/satellites"
	"storj.io/storj/storagenode/trust"
)

// Service acts as the gateway to the `satellites` db for graceful exit
// code (querying and updating that db as necessary).
//
// architecture: Service
type Service interface {
	// ListPendingExits returns a slice with one record for every satellite
	// from which this node is gracefully exiting. Each record includes the
	// satellite's ID/address and information about the graceful exit status
	// and progress.
	ListPendingExits(ctx context.Context) ([]ExitingSatellite, error)

	// DeletePiece deletes one piece stored for a satellite, and updates
	// the deleted byte count for the corresponding graceful exit operation.
	DeletePiece(ctx context.Context, satelliteID storj.NodeID, pieceID storj.PieceID) error

	// DeleteSatellitePieces deletes all pieces stored for a satellite, and updates
	// the deleted byte count for the corresponding graceful exit operation.
	DeleteSatellitePieces(ctx context.Context, satelliteID storj.NodeID) error

	// ExitFailed updates the database when a graceful exit has failed.
	ExitFailed(ctx context.Context, satelliteID storj.NodeID, reason pb.ExitFailed_Reason, exitFailedBytes []byte) error

	// ExitCompleted updates the database when a graceful exit is completed. It also
	// deletes all pieces and blobs for that satellite.
	ExitCompleted(ctx context.Context, satelliteID storj.NodeID, completionReceipt []byte, wait func()) error

	// ExitNotPossible deletes the entry for the corresponding graceful exit operation.
	// This is intended to be called when a graceful exit operation was initiated but
	// the satellite rejected it.
	ExitNotPossible(ctx context.Context, satelliteID storj.NodeID) error
}

// ensures that service implements Service.
var _ Service = (*service)(nil)

// service exposes methods to manage GE progress.
//
// architecture: Service
type service struct {
	log         *zap.Logger
	store       *pieces.Store
	trust       *trust.Pool
	satelliteDB satellites.DB

	nowFunc func() time.Time
}

// NewService is a constructor for a GE service.
func NewService(log *zap.Logger, store *pieces.Store, trust *trust.Pool, satelliteDB satellites.DB, dialer rpc.Dialer, config Config) Service {
	return &service{
		log:         log,
		store:       store,
		trust:       trust,
		satelliteDB: satelliteDB,
		nowFunc:     func() time.Time { return time.Now().UTC() },
	}
}

// ExitingSatellite encapsulates a node address with its graceful exit progress.
type ExitingSatellite struct {
	satellites.ExitProgress
	NodeURL storj.NodeURL
}

func (c *service) ListPendingExits(ctx context.Context) (_ []ExitingSatellite, err error) {
	defer mon.Task()(&ctx)(&err)

	exitProgress, err := c.satelliteDB.ListGracefulExits(ctx)
	if err != nil {
		return nil, err
	}
	exitingSatellites := make([]ExitingSatellite, 0, len(exitProgress))
	for _, sat := range exitProgress {
		if sat.FinishedAt != nil {
			continue
		}
		nodeURL, err := c.trust.GetNodeURL(ctx, sat.SatelliteID)
		if err != nil {
			c.log.Error("failed to get satellite address", zap.Stringer("Satellite ID", sat.SatelliteID), zap.Error(err))
			continue
		}
		exitingSatellites = append(exitingSatellites, ExitingSatellite{ExitProgress: sat, NodeURL: nodeURL})
	}
	return exitingSatellites, nil
}

// DeletePiece deletes one piece stored for a satellite, and updates
// the deleted byte count for the corresponding graceful exit operation.
func (c *service) DeletePiece(ctx context.Context, satelliteID storj.NodeID, pieceID storj.PieceID) (err error) {
	defer mon.Task()(&ctx)(&err)

	piece, err := c.store.Reader(ctx, satelliteID, pieceID)
	if err != nil {
		return Error.Wrap(err)
	}
	err = c.store.Delete(ctx, satelliteID, pieceID)
	if err != nil {
		return Error.Wrap(err)
	}
	// update graceful exit progress
	size := piece.Size()
	return c.satelliteDB.UpdateGracefulExit(ctx, satelliteID, size)
}

// DeleteSatellitePieces deletes all pieces stored for a satellite, and updates
// the deleted byte count for the corresponding graceful exit operation.
func (c *service) DeleteSatellitePieces(ctx context.Context, satelliteID storj.NodeID) (err error) {
	defer mon.Task()(&ctx)(&err)

	var totalDeleted int64
	logger := c.log.With(zap.Stringer("Satellite ID", satelliteID), zap.String("action", "delete all pieces"))
	err = c.store.WalkSatellitePieces(ctx, satelliteID, func(piece pieces.StoredPieceAccess) error {
		err := c.store.Delete(ctx, satelliteID, piece.PieceID())
		if err != nil {
			logger.Error("failed to delete piece",
				zap.Stringer("Piece ID", piece.PieceID()), zap.Error(err))
			// but continue
		}
		_, size, err := piece.Size(ctx)
		if err != nil {
			logger.Warn("failed to get piece size",
				zap.Stringer("Piece ID", piece.PieceID()), zap.Error(err))
			return nil
		}
		totalDeleted += size
		return nil
	})
	if err != nil && !errs2.IsCanceled(err) {
		logger.Error("failed to delete all pieces", zap.Error(err))
	}
	// update graceful exit progress
	return c.satelliteDB.UpdateGracefulExit(ctx, satelliteID, totalDeleted)
}

// ExitFailed updates the database when a graceful exit has failed.
func (c *service) ExitFailed(ctx context.Context, satelliteID storj.NodeID, reason pb.ExitFailed_Reason, exitFailedBytes []byte) (err error) {
	defer mon.Task()(&ctx)(&err)
	return c.satelliteDB.CompleteGracefulExit(ctx, satelliteID, c.nowFunc(), satellites.ExitFailed, exitFailedBytes)
}

// ExitCompleted updates the database when a graceful exit is completed. It also
// deletes all pieces and blobs for that satellite.
func (c *service) ExitCompleted(ctx context.Context, satelliteID storj.NodeID, completionReceipt []byte, wait func()) (err error) {
	defer mon.Task()(&ctx)(&err)

	err = c.satelliteDB.CompleteGracefulExit(ctx, satelliteID, c.nowFunc(), satellites.ExitSucceeded, completionReceipt)
	if err != nil {
		return errs.Wrap(err)
	}

	// wait for deletes to complete
	wait()

	// delete all remaining pieces
	err = c.DeleteSatellitePieces(ctx, satelliteID)
	if err != nil {
		return errs.Wrap(err)
	}

	// delete everything left in blobs folder of specific satellites
	return c.store.DeleteSatelliteBlobs(ctx, satelliteID)
}

// ExitNotPossible deletes the entry from satellite table and inform graceful exit
// has failed to start.
func (c *service) ExitNotPossible(ctx context.Context, satelliteID storj.NodeID) (err error) {
	defer mon.Task()(&ctx)(&err)

	return c.satelliteDB.CancelGracefulExit(ctx, satelliteID)
}
