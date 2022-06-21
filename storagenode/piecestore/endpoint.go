// Copyright (C) 2019 Storj Labs, Inc.
// See LICENSE for copying information.

package piecestore

import (
	"context"
	"fmt"
	"io"
	"os"
	"sync/atomic"
	"time"

	"github.com/spacemonkeygo/monkit/v3"
	"github.com/zeebo/errs"
	"go.uber.org/zap"
	"golang.org/x/sync/errgroup"

	"storj.io/common/bloomfilter"
	"storj.io/common/context2"
	"storj.io/common/errs2"
	"storj.io/common/identity"
	"storj.io/common/memory"
	"storj.io/common/pb"
	"storj.io/common/rpc/rpcstatus"
	"storj.io/common/rpc/rpctimeout"
	"storj.io/common/signing"
	"storj.io/common/storj"
	"storj.io/common/sync2"
	"storj.io/drpc"
	"storj.io/storj/storagenode/bandwidth"
	"storj.io/storj/storagenode/monitor"
	"storj.io/storj/storagenode/orders"
	"storj.io/storj/storagenode/orders/ordersfile"
	"storj.io/storj/storagenode/pieces"
	"storj.io/storj/storagenode/piecestore/usedserials"
	"storj.io/storj/storagenode/retain"
	"storj.io/storj/storagenode/trust"
)

var (
	mon = monkit.Package()
)

// OldConfig contains everything necessary for a server.
type OldConfig struct {
	Path                   string         `help:"path to store data in" default:"$CONFDIR/storage"`
	WhitelistedSatellites  storj.NodeURLs `help:"a comma-separated list of approved satellite node urls (unused)" devDefault:"" releaseDefault:""`
	AllocatedDiskSpace     memory.Size    `user:"true" help:"total allocated disk space in bytes" default:"1TB"`
	AllocatedBandwidth     memory.Size    `user:"true" help:"total allocated bandwidth in bytes (deprecated)" default:"0B"`
	KBucketRefreshInterval time.Duration  `help:"how frequently Kademlia bucket should be refreshed with node stats" default:"1h0m0s"`
}

// Config defines parameters for piecestore endpoint.
type Config struct {
	DatabaseDir             string        `help:"directory to store databases. if empty, uses data path" default:""`
	ExpirationGracePeriod   time.Duration `help:"how soon before expiration date should things be considered expired" default:"48h0m0s"`
	MaxConcurrentRequests   int           `help:"how many concurrent requests are allowed, before uploads are rejected. 0 represents unlimited." default:"0"`
	DeleteWorkers           int           `help:"how many piece delete workers" default:"1"`
	DeleteQueueSize         int           `help:"size of the piece delete queue" default:"10000"`
	OrderLimitGracePeriod   time.Duration `help:"how long after OrderLimit creation date are OrderLimits no longer accepted" default:"1h0m0s"`
	CacheSyncInterval       time.Duration `help:"how often the space used cache is synced to persistent storage" releaseDefault:"1h0m0s" devDefault:"0h1m0s"`
	StreamOperationTimeout  time.Duration `help:"how long to spend waiting for a stream operation before canceling" default:"30m"`
	RetainTimeBuffer        time.Duration `help:"allows for small differences in the satellite and storagenode clocks" default:"48h0m0s"`
	ReportCapacityThreshold memory.Size   `help:"threshold below which to immediately notify satellite of capacity" default:"500MB" hidden:"true"`
	MaxUsedSerialsSize      memory.Size   `help:"amount of memory allowed for used serials store - once surpassed, serials will be dropped at random" default:"1MB"`

	MinUploadSpeed                    memory.Size   `help:"a client upload speed should not be lower than MinUploadSpeed in bytes-per-second (E.g: 1Mb), otherwise, it will be flagged as slow-connection and potentially be closed" default:"0Mb"`
	MinUploadSpeedGraceDuration       time.Duration `help:"if MinUploadSpeed is configured, after a period of time after the client initiated the upload, the server will flag unusually slow upload client" default:"0h0m10s"`
	MinUploadSpeedCongestionThreshold float64       `help:"if the portion defined by the total number of alive connection per MaxConcurrentRequest reaches this threshold, a slow upload client will no longer be monitored and flagged" default:"0.8"`

	Trust trust.Config

	Monitor monitor.Config
	Orders  orders.Config
}

type pingStatsSource interface {
	WasPinged(when time.Time)
}

// Endpoint implements uploading, downloading and deleting for a storage node..
//
// architecture: Endpoint
type Endpoint struct {
	pb.DRPCContactUnimplementedServer

	log    *zap.Logger
	config Config

	signer    signing.Signer
	trust     *trust.Pool
	monitor   *monitor.Service
	retain    *retain.Service
	pingStats pingStatsSource

	store        *pieces.Store
	ordersStore  *orders.FileStore
	usage        bandwidth.DB
	usedSerials  *usedserials.Table
	pieceDeleter *pieces.Deleter

	liveRequests int32
}

// NewEndpoint creates a new piecestore endpoint.
func NewEndpoint(log *zap.Logger, signer signing.Signer, trust *trust.Pool, monitor *monitor.Service, retain *retain.Service, pingStats pingStatsSource, store *pieces.Store, pieceDeleter *pieces.Deleter, ordersStore *orders.FileStore, usage bandwidth.DB, usedSerials *usedserials.Table, config Config) (*Endpoint, error) {
	return &Endpoint{
		log:    log,
		config: config,

		signer:    signer,
		trust:     trust,
		monitor:   monitor,
		retain:    retain,
		pingStats: pingStats,

		store:        store,
		ordersStore:  ordersStore,
		usage:        usage,
		usedSerials:  usedSerials,
		pieceDeleter: pieceDeleter,

		liveRequests: 0,
	}, nil
}

var monLiveRequests = mon.TaskNamed("live-request")

// Delete handles deleting a piece on piece store requested by uplink.
//
// Deprecated: use DeletePieces instead.
func (endpoint *Endpoint) Delete(ctx context.Context, delete *pb.PieceDeleteRequest) (_ *pb.PieceDeleteResponse, err error) {
	defer monLiveRequests(&ctx)(&err)
	defer mon.Task()(&ctx)(&err)

	atomic.AddInt32(&endpoint.liveRequests, 1)
	defer atomic.AddInt32(&endpoint.liveRequests, -1)

	endpoint.pingStats.WasPinged(time.Now())

	if delete.Limit.Action != pb.PieceAction_DELETE {
		return nil, rpcstatus.Errorf(rpcstatus.InvalidArgument,
			"expected delete action got %v", delete.Limit.Action)
	}

	if err := endpoint.verifyOrderLimit(ctx, delete.Limit); err != nil {
		return nil, rpcstatus.Wrap(rpcstatus.Unauthenticated, err)
	}

	if err := endpoint.store.Delete(ctx, delete.Limit.SatelliteId, delete.Limit.PieceId); err != nil {
		// explicitly ignoring error because the errors

		// TODO: https://storjlabs.atlassian.net/browse/V3-3222
		// report rpc status of internal server error or not found error,
		// e.g. not found might happen when we get a deletion request after garbage
		// collection has deleted it
		endpoint.log.Error("delete failed", zap.Stringer("Satellite ID", delete.Limit.SatelliteId), zap.Stringer("Piece ID", delete.Limit.PieceId), zap.Error(err))
	} else {
		endpoint.log.Info("deleted", zap.Stringer("Satellite ID", delete.Limit.SatelliteId), zap.Stringer("Piece ID", delete.Limit.PieceId))
	}

	return &pb.PieceDeleteResponse{}, nil
}

// DeletePieces delete a list of pieces on satellite request.
func (endpoint *Endpoint) DeletePieces(
	ctx context.Context, req *pb.DeletePiecesRequest,
) (_ *pb.DeletePiecesResponse, err error) {
	defer mon.Task()(&ctx, req.PieceIds)(&err)

	peer, err := identity.PeerIdentityFromContext(ctx)
	if err != nil {
		return nil, rpcstatus.Wrap(rpcstatus.Unauthenticated, err)
	}

	err = endpoint.trust.VerifySatelliteID(ctx, peer.ID)
	if err != nil {
		return nil, rpcstatus.Error(rpcstatus.PermissionDenied, "delete pieces called with untrusted ID")
	}

	unhandled := endpoint.pieceDeleter.Enqueue(ctx, peer.ID, req.PieceIds)

	return &pb.DeletePiecesResponse{
		UnhandledCount: int64(unhandled),
	}, nil
}

// Upload handles uploading a piece on piece store.
func (endpoint *Endpoint) Upload(stream pb.DRPCPiecestore_UploadStream) (err error) {
	ctx := stream.Context()
	defer monLiveRequests(&ctx)(&err)
	defer mon.Task()(&ctx)(&err)

	liveRequests := atomic.AddInt32(&endpoint.liveRequests, 1)
	defer atomic.AddInt32(&endpoint.liveRequests, -1)

	endpoint.pingStats.WasPinged(time.Now())

	if endpoint.config.MaxConcurrentRequests > 0 && int(liveRequests) > endpoint.config.MaxConcurrentRequests {
		endpoint.log.Error("upload rejected, too many requests",
			zap.Int32("live requests", liveRequests),
			zap.Int("requestLimit", endpoint.config.MaxConcurrentRequests),
		)
		errMsg := fmt.Sprintf("storage node overloaded, request limit: %d", endpoint.config.MaxConcurrentRequests)
		return rpcstatus.Error(rpcstatus.Unavailable, errMsg)
	}

	startTime := time.Now().UTC()

	// TODO: set maximum message size

	// N.B.: we are only allowed to use message if the returned error is nil. it would be
	// a race condition otherwise as Run does not wait for the closure to exit.
	var message *pb.PieceUploadRequest
	err = rpctimeout.Run(ctx, endpoint.config.StreamOperationTimeout, func(_ context.Context) (err error) {
		message, err = stream.Recv()
		return err
	})
	switch {
	case err != nil:
		return rpcstatus.Wrap(rpcstatus.Internal, err)
	case message == nil:
		return rpcstatus.Error(rpcstatus.InvalidArgument, "expected a message")
	case message.Limit == nil:
		return rpcstatus.Error(rpcstatus.InvalidArgument, "expected order limit as the first message")
	}
	limit := message.Limit

	if limit.Action != pb.PieceAction_PUT && limit.Action != pb.PieceAction_PUT_REPAIR {
		return rpcstatus.Errorf(rpcstatus.InvalidArgument, "expected put or put repair action got %v", limit.Action)
	}

	if err := endpoint.verifyOrderLimit(ctx, limit); err != nil {
		return err
	}

	availableSpace, err := endpoint.monitor.AvailableSpace(ctx)
	if err != nil {
		return rpcstatus.Wrap(rpcstatus.Internal, err)
	}
	// if availableSpace has fallen below ReportCapacityThreshold, report capacity to satellites
	defer func() {
		if availableSpace < endpoint.config.ReportCapacityThreshold.Int64() {
			endpoint.monitor.NotifyLowDisk()
		}
	}()

	if availableSpace < limit.Limit {
		return rpcstatus.Errorf(rpcstatus.Aborted, "not enough available disk space, have: %v, need: %v", availableSpace, limit.Limit)
	}

	var pieceWriter *pieces.Writer
	// committed is set to true when the piece is committed.
	// It is used to distinguish successful pieces where the uplink cancels the connections,
	// and pieces that were actually canceled before being completed.
	var committed bool
	defer func() {
		endTime := time.Now().UTC()
		dt := endTime.Sub(startTime)
		uploadSize := int64(0)
		if pieceWriter != nil {
			uploadSize = pieceWriter.Size()
		}
		uploadRate := float64(0)
		if dt.Seconds() > 0 {
			uploadRate = float64(uploadSize) / dt.Seconds()
		}
		uploadDuration := dt.Nanoseconds()

		if err != nil && !errs2.IsCanceled(err) {
			mon.Counter("upload_failure_count").Inc(1)
			mon.Meter("upload_failure_byte_meter").Mark64(uploadSize)
			mon.IntVal("upload_failure_size_bytes").Observe(uploadSize)
			mon.IntVal("upload_failure_duration_ns").Observe(uploadDuration)
			mon.FloatVal("upload_failure_rate_bytes_per_sec").Observe(uploadRate)
			endpoint.log.Error("upload failed", zap.Stringer("Piece ID", limit.PieceId), zap.Stringer("Satellite ID", limit.SatelliteId), zap.Stringer("Action", limit.Action), zap.Error(err), zap.Int64("Size", uploadSize))
		} else if (errs2.IsCanceled(err) || drpc.ClosedError.Has(err)) && !committed {
			mon.Counter("upload_cancel_count").Inc(1)
			mon.Meter("upload_cancel_byte_meter").Mark64(uploadSize)
			mon.IntVal("upload_cancel_size_bytes").Observe(uploadSize)
			mon.IntVal("upload_cancel_duration_ns").Observe(uploadDuration)
			mon.FloatVal("upload_cancel_rate_bytes_per_sec").Observe(uploadRate)
			endpoint.log.Info("upload canceled", zap.Stringer("Piece ID", limit.PieceId), zap.Stringer("Satellite ID", limit.SatelliteId), zap.Stringer("Action", limit.Action), zap.Int64("Size", uploadSize))
		} else {
			mon.Counter("upload_success_count").Inc(1)
			mon.Meter("upload_success_byte_meter").Mark64(uploadSize)
			mon.IntVal("upload_success_size_bytes").Observe(uploadSize)
			mon.IntVal("upload_success_duration_ns").Observe(uploadDuration)
			mon.FloatVal("upload_success_rate_bytes_per_sec").Observe(uploadRate)
			endpoint.log.Info("uploaded", zap.Stringer("Piece ID", limit.PieceId), zap.Stringer("Satellite ID", limit.SatelliteId), zap.Stringer("Action", limit.Action), zap.Int64("Size", uploadSize))
		}
	}()

	endpoint.log.Info("upload started",
		zap.Stringer("Piece ID", limit.PieceId),
		zap.Stringer("Satellite ID", limit.SatelliteId),
		zap.Stringer("Action", limit.Action),
		zap.Int64("Available Space", availableSpace))
	mon.Counter("upload_started_count").Inc(1)

	pieceWriter, err = endpoint.store.Writer(ctx, limit.SatelliteId, limit.PieceId)
	if err != nil {
		return rpcstatus.Wrap(rpcstatus.Internal, err)
	}
	defer func() {
		// cancel error if it hasn't been committed
		if cancelErr := pieceWriter.Cancel(ctx); cancelErr != nil {
			if errs2.IsCanceled(cancelErr) {
				return
			}
			endpoint.log.Error("error during canceling a piece write", zap.Error(cancelErr))
		}
	}()

	// Ensure that the order is saved even in the face of an error. In the
	// success path, the order will be saved just before sending the response
	// and closing the stream (in which case, orderSaved will be true).
	commitOrderToStore, err := endpoint.beginSaveOrder(limit)
	if err != nil {
		return rpcstatus.Wrap(rpcstatus.InvalidArgument, err)
	}
	largestOrder := pb.Order{}
	defer commitOrderToStore(ctx, &largestOrder)

	// monitor speed of upload client to flag out slow uploads.
	speedEstimate := speedEstimation{
		grace: endpoint.config.MinUploadSpeedGraceDuration,
		limit: endpoint.config.MinUploadSpeed,
	}

	for {

		if err := speedEstimate.EnsureLimit(memory.Size(pieceWriter.Size()), endpoint.isCongested(), time.Now()); err != nil {
			return rpcstatus.Wrap(rpcstatus.Aborted, err)
		}

		// TODO: reuse messages to avoid allocations

		// N.B.: we are only allowed to use message if the returned error is nil. it would be
		// a race condition otherwise as Run does not wait for the closure to exit.
		err = rpctimeout.Run(ctx, endpoint.config.StreamOperationTimeout, func(_ context.Context) (err error) {
			message, err = stream.Recv()
			return err
		})
		if errs.Is(err, io.EOF) {
			return rpcstatus.Error(rpcstatus.InvalidArgument, "unexpected EOF")
		} else if err != nil {
			return rpcstatus.Wrap(rpcstatus.Internal, err)
		}

		if message == nil {
			return rpcstatus.Error(rpcstatus.InvalidArgument, "expected a message")
		}
		if message.Order == nil && message.Chunk == nil && message.Done == nil {
			return rpcstatus.Error(rpcstatus.InvalidArgument, "expected a message")
		}

		if message.Order != nil {
			if err := endpoint.VerifyOrder(ctx, limit, message.Order, largestOrder.Amount); err != nil {
				return err
			}
			largestOrder = *message.Order
		}

		if message.Chunk != nil {
			if message.Chunk.Offset != pieceWriter.Size() {
				return rpcstatus.Error(rpcstatus.InvalidArgument, "chunk out of order")
			}

			chunkSize := int64(len(message.Chunk.Data))
			if largestOrder.Amount < pieceWriter.Size()+chunkSize {
				// TODO: should we write currently and give a chance for uplink to remedy the situation?
				return rpcstatus.Errorf(rpcstatus.InvalidArgument,
					"not enough allocated, allocated=%v writing=%v",
					largestOrder.Amount, pieceWriter.Size()+int64(len(message.Chunk.Data)))
			}

			availableSpace -= chunkSize
			if availableSpace < 0 {
				return rpcstatus.Error(rpcstatus.Internal, "out of space")
			}
			if _, err := pieceWriter.Write(message.Chunk.Data); err != nil {
				return rpcstatus.Wrap(rpcstatus.Internal, err)
			}
		}

		if message.Done != nil {
			calculatedHash := pieceWriter.Hash()
			if err := endpoint.VerifyPieceHash(ctx, limit, message.Done, calculatedHash); err != nil {
				return rpcstatus.Wrap(rpcstatus.Internal, err)
			}
			if message.Done.PieceSize != pieceWriter.Size() {
				return rpcstatus.Errorf(rpcstatus.InvalidArgument,
					"Size of finished piece does not match size declared by uplink! %d != %d",
					message.Done.PieceSize, pieceWriter.Size())
			}

			{
				info := &pb.PieceHeader{
					Hash:         calculatedHash,
					CreationTime: message.Done.Timestamp,
					Signature:    message.Done.GetSignature(),
					OrderLimit:   *limit,
				}
				if err := pieceWriter.Commit(ctx, info); err != nil {
					return rpcstatus.Wrap(rpcstatus.Internal, err)
				}
				committed = true
				if !limit.PieceExpiration.IsZero() {
					err := endpoint.store.SetExpiration(ctx, limit.SatelliteId, limit.PieceId, limit.PieceExpiration)
					if err != nil {
						return rpcstatus.Wrap(rpcstatus.Internal, err)
					}
				}
			}

			storageNodeHash, err := signing.SignPieceHash(ctx, endpoint.signer, &pb.PieceHash{
				PieceId:   limit.PieceId,
				Hash:      calculatedHash,
				PieceSize: pieceWriter.Size(),
				Timestamp: time.Now(),
			})
			if err != nil {
				return rpcstatus.Wrap(rpcstatus.Internal, err)
			}

			closeErr := rpctimeout.Run(ctx, endpoint.config.StreamOperationTimeout, func(_ context.Context) (err error) {
				return stream.SendAndClose(&pb.PieceUploadResponse{Done: storageNodeHash})
			})
			if errs.Is(closeErr, io.EOF) {
				closeErr = nil
			}
			if closeErr != nil {
				return rpcstatus.Wrap(rpcstatus.Internal, closeErr)
			}
			return nil
		}
	}
}

// isCongested identifies state of congestion. If the total number of
// connections is above 80% of the MaxConcurrentRequests, then it is defined
// as congestion.
func (endpoint *Endpoint) isCongested() bool {

	requestCongestionThreshold := int32(float64(endpoint.config.MaxConcurrentRequests) * endpoint.config.MinUploadSpeedCongestionThreshold)

	connectionCount := atomic.LoadInt32(&endpoint.liveRequests)
	return connectionCount > requestCongestionThreshold
}

// Download handles Downloading a piece on piecestore.
func (endpoint *Endpoint) Download(stream pb.DRPCPiecestore_DownloadStream) (err error) {
	ctx := stream.Context()
	defer monLiveRequests(&ctx)(&err)
	defer mon.Task()(&ctx)(&err)

	atomic.AddInt32(&endpoint.liveRequests, 1)
	defer atomic.AddInt32(&endpoint.liveRequests, -1)

	startTime := time.Now().UTC()

	endpoint.pingStats.WasPinged(time.Now())

	// TODO: set maximum message size

	var message *pb.PieceDownloadRequest
	// N.B.: we are only allowed to use message if the returned error is nil. it would be
	// a race condition otherwise as Run does not wait for the closure to exit.
	err = rpctimeout.Run(ctx, endpoint.config.StreamOperationTimeout, func(_ context.Context) (err error) {
		message, err = stream.Recv()
		return err
	})
	if err != nil {
		return rpcstatus.Wrap(rpcstatus.Internal, err)
	}
	if message.Limit == nil || message.Chunk == nil {
		return rpcstatus.Error(rpcstatus.InvalidArgument, "expected order limit and chunk as the first message")
	}
	limit, chunk := message.Limit, message.Chunk

	if limit.Action != pb.PieceAction_GET && limit.Action != pb.PieceAction_GET_REPAIR && limit.Action != pb.PieceAction_GET_AUDIT {
		return rpcstatus.Errorf(rpcstatus.InvalidArgument,
			"expected get or get repair or audit action got %v", limit.Action)
	}

	if chunk.ChunkSize > limit.Limit {
		return rpcstatus.Errorf(rpcstatus.InvalidArgument,
			"requested more that order limit allows, limit=%v requested=%v", limit.Limit, chunk.ChunkSize)
	}

	actionSeriesTag := monkit.NewSeriesTag("action", limit.Action.String())

	endpoint.log.Info("download started", zap.Stringer("Piece ID", limit.PieceId), zap.Stringer("Satellite ID", limit.SatelliteId), zap.Stringer("Action", limit.Action))
	mon.Counter("download_started_count", actionSeriesTag).Inc(1)

	if err := endpoint.verifyOrderLimit(ctx, limit); err != nil {
		mon.Counter("download_failure_count", actionSeriesTag).Inc(1)
		mon.Meter("download_verify_orderlimit_failed", actionSeriesTag).Mark(1)
		endpoint.log.Error("download failed", zap.Stringer("Piece ID", limit.PieceId), zap.Stringer("Satellite ID", limit.SatelliteId), zap.Stringer("Action", limit.Action), zap.Error(err))
		return err
	}

	var pieceReader *pieces.Reader
	defer func() {
		endTime := time.Now().UTC()
		dt := endTime.Sub(startTime)
		downloadSize := int64(0)
		if pieceReader != nil {
			downloadSize = pieceReader.Size()
		}
		downloadRate := float64(0)
		if dt.Seconds() > 0 {
			downloadRate = float64(downloadSize) / dt.Seconds()
		}
		downloadDuration := dt.Nanoseconds()
		if errs2.IsCanceled(err) || drpc.ClosedError.Has(err) {
			mon.Counter("download_cancel_count", actionSeriesTag).Inc(1)
			mon.Meter("download_cancel_byte_meter", actionSeriesTag).Mark64(downloadSize)
			mon.IntVal("download_cancel_size_bytes", actionSeriesTag).Observe(downloadSize)
			mon.IntVal("download_cancel_duration_ns", actionSeriesTag).Observe(downloadDuration)
			mon.FloatVal("download_cancel_rate_bytes_per_sec", actionSeriesTag).Observe(downloadRate)
			endpoint.log.Info("download canceled", zap.Stringer("Piece ID", limit.PieceId), zap.Stringer("Satellite ID", limit.SatelliteId), zap.Stringer("Action", limit.Action))
		} else if err != nil {
			mon.Counter("download_failure_count", actionSeriesTag).Inc(1)
			mon.Meter("download_failure_byte_meter", actionSeriesTag).Mark64(downloadSize)
			mon.IntVal("download_failure_size_bytes", actionSeriesTag).Observe(downloadSize)
			mon.IntVal("download_failure_duration_ns", actionSeriesTag).Observe(downloadDuration)
			mon.FloatVal("download_failure_rate_bytes_per_sec", actionSeriesTag).Observe(downloadRate)
			endpoint.log.Error("download failed", zap.Stringer("Piece ID", limit.PieceId), zap.Stringer("Satellite ID", limit.SatelliteId), zap.Stringer("Action", limit.Action), zap.Error(err))
		} else {
			mon.Counter("download_success_count", actionSeriesTag).Inc(1)
			mon.Meter("download_success_byte_meter", actionSeriesTag).Mark64(downloadSize)
			mon.IntVal("download_success_size_bytes", actionSeriesTag).Observe(downloadSize)
			mon.IntVal("download_success_duration_ns", actionSeriesTag).Observe(downloadDuration)
			mon.FloatVal("download_success_rate_bytes_per_sec", actionSeriesTag).Observe(downloadRate)
			endpoint.log.Info("downloaded", zap.Stringer("Piece ID", limit.PieceId), zap.Stringer("Satellite ID", limit.SatelliteId), zap.Stringer("Action", limit.Action))
		}
	}()

	pieceReader, err = endpoint.store.Reader(ctx, limit.SatelliteId, limit.PieceId)
	if err != nil {
		if os.IsNotExist(err) {
			endpoint.monitor.VerifyDirReadableLoop.TriggerWait()
			return rpcstatus.Wrap(rpcstatus.NotFound, err)
		}
		return rpcstatus.Wrap(rpcstatus.Internal, err)
	}
	defer func() {
		err := pieceReader.Close() // similarly how transcation Rollback works
		if err != nil {
			if errs2.IsCanceled(err) {
				return
			}
			// no reason to report this error to the uplink
			endpoint.log.Error("failed to close piece reader", zap.Error(err))
		}
	}()

	// for repair traffic, send along the PieceHash and original OrderLimit for validation
	// before sending the piece itself
	if message.Limit.Action == pb.PieceAction_GET_REPAIR {
		pieceHash, orderLimit, err := endpoint.store.GetHashAndLimit(ctx, limit.SatelliteId, limit.PieceId, pieceReader)
		if err != nil {
			endpoint.log.Error("could not get hash and order limit", zap.Error(err))
			return rpcstatus.Wrap(rpcstatus.Internal, err)
		}

		err = rpctimeout.Run(ctx, endpoint.config.StreamOperationTimeout, func(_ context.Context) (err error) {
			return stream.Send(&pb.PieceDownloadResponse{Hash: &pieceHash, Limit: &orderLimit})
		})
		if err != nil {
			endpoint.log.Error("error sending hash and order limit", zap.Error(err))
			return rpcstatus.Wrap(rpcstatus.Internal, err)
		}
	}

	// TODO: verify chunk.Size behavior logic with regards to reading all
	if chunk.Offset+chunk.ChunkSize > pieceReader.Size() {
		return rpcstatus.Errorf(rpcstatus.InvalidArgument,
			"requested more data than available, requesting=%v available=%v",
			chunk.Offset+chunk.ChunkSize, pieceReader.Size())
	}

	throttle := sync2.NewThrottle()
	// TODO: see whether this can be implemented without a goroutine

	group, ctx := errgroup.WithContext(ctx)
	group.Go(func() (err error) {
		var maximumChunkSize = 1 * memory.MiB.Int64()

		currentOffset := chunk.Offset
		unsentAmount := chunk.ChunkSize
		for unsentAmount > 0 {
			tryToSend := min(unsentAmount, maximumChunkSize)

			// TODO: add timeout here
			chunkSize, err := throttle.ConsumeOrWait(tryToSend)
			if err != nil {
				// this can happen only because uplink decided to close the connection
				return nil //nolint: nilerr // We don't need to return an error when client cancels.
			}

			chunkData := make([]byte, chunkSize)
			_, err = pieceReader.Seek(currentOffset, io.SeekStart)
			if err != nil {
				endpoint.log.Error("error seeking on piecereader", zap.Error(err))
				return rpcstatus.Wrap(rpcstatus.Internal, err)
			}

			// ReadFull is required to ensure we are sending the right amount of data.
			_, err = io.ReadFull(pieceReader, chunkData)
			if err != nil {
				endpoint.log.Error("error reading from piecereader", zap.Error(err))
				return rpcstatus.Wrap(rpcstatus.Internal, err)
			}

			err = rpctimeout.Run(ctx, endpoint.config.StreamOperationTimeout, func(_ context.Context) (err error) {
				return stream.Send(&pb.PieceDownloadResponse{
					Chunk: &pb.PieceDownloadResponse_Chunk{
						Offset: currentOffset,
						Data:   chunkData,
					},
				})
			})
			if errs.Is(err, io.EOF) {
				// err is io.EOF when uplink asked for a piece, but decided not to retrieve it,
				// no need to propagate it
				return nil
			}
			if err != nil {
				return rpcstatus.Wrap(rpcstatus.Internal, err)
			}

			currentOffset += chunkSize
			unsentAmount -= chunkSize
		}
		return nil
	})

	recvErr := func() (err error) {
		commitOrderToStore, err := endpoint.beginSaveOrder(limit)
		if err != nil {
			return err
		}
		largestOrder := pb.Order{}
		defer commitOrderToStore(ctx, &largestOrder)

		// ensure that we always terminate sending goroutine
		defer throttle.Fail(io.EOF)

		for {
			// N.B.: we are only allowed to use message if the returned error is nil. it would be
			// a race condition otherwise as Run does not wait for the closure to exit.
			err = rpctimeout.Run(ctx, endpoint.config.StreamOperationTimeout, func(_ context.Context) (err error) {
				message, err = stream.Recv()
				return err
			})
			if errs.Is(err, io.EOF) {
				// err is io.EOF or canceled when uplink closed the connection, no need to return error
				return nil
			}
			if errs2.IsCanceled(err) {
				return nil
			}
			if err != nil {
				return rpcstatus.Wrap(rpcstatus.Internal, err)
			}

			if message == nil || message.Order == nil {
				return rpcstatus.Error(rpcstatus.InvalidArgument, "expected order as the message")
			}

			if err := endpoint.VerifyOrder(ctx, limit, message.Order, largestOrder.Amount); err != nil {
				return err
			}

			chunkSize := message.Order.Amount - largestOrder.Amount
			if err := throttle.Produce(chunkSize); err != nil {
				// shouldn't happen since only receiving side is calling Fail
				return rpcstatus.Wrap(rpcstatus.Internal, err)
			}
			largestOrder = *message.Order
		}
	}()

	// ensure we wait for sender to complete
	sendErr := group.Wait()
	return rpcstatus.Wrap(rpcstatus.Internal, errs.Combine(sendErr, recvErr))
}

// beginSaveOrder saves the order with all necessary information. It assumes it has been already verified.
func (endpoint *Endpoint) beginSaveOrder(limit *pb.OrderLimit) (_commit func(ctx context.Context, order *pb.Order), err error) {
	defer mon.Task()(nil)(&err)

	commit, err := endpoint.ordersStore.BeginEnqueue(limit.SatelliteId, limit.OrderCreation)
	if err != nil {
		return nil, err
	}

	done := false
	return func(ctx context.Context, order *pb.Order) {
		if done {
			return
		}
		done = true

		if order == nil || order.Amount <= 0 {
			// free unsent orders file for sending without writing anything
			err = commit(nil)
			if err != nil {
				endpoint.log.Error("failed to unlock orders file", zap.Error(err))
			}
			return
		}

		err = commit(&ordersfile.Info{Limit: limit, Order: order})
		if err != nil {
			endpoint.log.Error("failed to add order", zap.Error(err))
		} else {
			// We always want to save order to the database to be able to settle.
			err = endpoint.usage.Add(context2.WithoutCancellation(ctx), limit.SatelliteId, limit.Action, order.Amount, time.Now())
			if err != nil {
				endpoint.log.Error("failed to add bandwidth usage", zap.Error(err))
			}
		}
	}, nil
}

// RestoreTrash restores all trashed items for the satellite issuing the call.
func (endpoint *Endpoint) RestoreTrash(ctx context.Context, restoreTrashReq *pb.RestoreTrashRequest) (res *pb.RestoreTrashResponse, err error) {
	defer mon.Task()(&ctx)(&err)

	peer, err := identity.PeerIdentityFromContext(ctx)
	if err != nil {
		return nil, rpcstatus.Wrap(rpcstatus.Unauthenticated, err)
	}

	err = endpoint.trust.VerifySatelliteID(ctx, peer.ID)
	if err != nil {
		return nil, rpcstatus.Error(rpcstatus.PermissionDenied, "RestoreTrash called with untrusted ID")
	}

	endpoint.log.Info("restore trash started", zap.Stringer("Satellite ID", peer.ID))
	err = endpoint.store.RestoreTrash(ctx, peer.ID)
	if err != nil {
		endpoint.log.Error("restore trash failed", zap.Stringer("Satellite ID", peer.ID), zap.Error(err))
		return nil, rpcstatus.Wrap(rpcstatus.Internal, err)
	}
	endpoint.log.Info("restore trash finished", zap.Stringer("Satellite ID", peer.ID))

	return &pb.RestoreTrashResponse{}, nil
}

// Retain keeps only piece ids specified in the request.
func (endpoint *Endpoint) Retain(ctx context.Context, retainReq *pb.RetainRequest) (res *pb.RetainResponse, err error) {
	defer mon.Task()(&ctx)(&err)

	// if retain status is disabled, quit immediately
	if endpoint.retain.Status() == retain.Disabled {
		return &pb.RetainResponse{}, nil
	}

	peer, err := identity.PeerIdentityFromContext(ctx)
	if err != nil {
		return nil, rpcstatus.Wrap(rpcstatus.Unauthenticated, err)
	}

	err = endpoint.trust.VerifySatelliteID(ctx, peer.ID)
	if err != nil {
		return nil, rpcstatus.Errorf(rpcstatus.PermissionDenied, "retain called with untrusted ID")
	}

	filter, err := bloomfilter.NewFromBytes(retainReq.GetFilter())
	if err != nil {
		return nil, rpcstatus.Wrap(rpcstatus.InvalidArgument, err)
	}
	filterHashCount, _ := filter.Parameters()
	mon.IntVal("retain_filter_size").Observe(filter.Size())
	mon.IntVal("retain_filter_hash_count").Observe(int64(filterHashCount))
	mon.IntVal("retain_creation_date").Observe(retainReq.CreationDate.Unix())

	// the queue function will update the created before time based on the configurable retain buffer
	queued := endpoint.retain.Queue(retain.Request{
		SatelliteID:   peer.ID,
		CreatedBefore: retainReq.GetCreationDate(),
		Filter:        filter,
	})
	if !queued {
		endpoint.log.Debug("Retain job not queued for satellite", zap.Stringer("Satellite ID", peer.ID))
	}

	return &pb.RetainResponse{}, nil
}

// TestLiveRequestCount returns the current number of live requests.
func (endpoint *Endpoint) TestLiveRequestCount() int32 {
	return atomic.LoadInt32(&endpoint.liveRequests)
}

// min finds the min of two values.
func min(a, b int64) int64 {
	if a < b {
		return a
	}
	return b
}

// speedEstimation monitors state of incoming traffic. It would signal slow-speed
// client in non-congested traffic condition.
type speedEstimation struct {
	// grace indicates a certain period of time before the observator kicks in
	grace time.Duration
	// limit for flagging slow connections. Speed below this limit is considered to be slow.
	limit memory.Size
	// uncongestedTime indicates the duration of connection, measured in non-congested state
	uncongestedTime time.Duration
	lastChecked     time.Time
}

// EnsureLimit makes sure that in non-congested condition, a slow-upload client will be flagged out.
func (estimate *speedEstimation) EnsureLimit(transferred memory.Size, congested bool, now time.Time) error {
	if estimate.lastChecked.IsZero() {
		estimate.lastChecked = now
		return nil
	}

	delta := now.Sub(estimate.lastChecked)
	estimate.lastChecked = now

	// In congested condition, the speed check would produce false-positive results,
	// thus it shall be skipped.
	if congested {
		return nil
	}

	estimate.uncongestedTime += delta
	if estimate.uncongestedTime <= 0 || estimate.uncongestedTime <= estimate.grace {
		// not enough data
		return nil
	}
	bytesPerSec := float64(transferred) / estimate.uncongestedTime.Seconds()

	if bytesPerSec < float64(estimate.limit) {
		return errs.New("speed too low, current:%v < limit:%v", bytesPerSec, estimate.limit)
	}

	return nil
}
