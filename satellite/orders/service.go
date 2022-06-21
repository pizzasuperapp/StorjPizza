// Copyright (C) 2019 Storj Labs, Inc.
// See LICENSE for copying information.

package orders

import (
	"context"
	"math"
	mathrand "math/rand"
	"sync"
	"time"

	"github.com/zeebo/errs"
	"go.uber.org/zap"

	"storj.io/common/pb"
	"storj.io/common/signing"
	"storj.io/common/storj"
	"storj.io/common/uuid"
	"storj.io/storj/satellite/internalpb"
	"storj.io/storj/satellite/metabase"
	"storj.io/storj/satellite/overlay"
	"storj.io/uplink/private/eestream"
)

var (
	// ErrDownloadFailedNotEnoughPieces is returned when download failed due to missing pieces.
	ErrDownloadFailedNotEnoughPieces = errs.Class("not enough pieces for download")
	// ErrDecryptOrderMetadata is returned when a step of decrypting metadata fails.
	ErrDecryptOrderMetadata = errs.Class("decrytping order metadata")
)

// Config is a configuration struct for orders Service.
type Config struct {
	EncryptionKeys      EncryptionKeys `help:"encryption keys to encrypt info in orders" default:""`
	Expiration          time.Duration  `help:"how long until an order expires" default:"48h" testDefault:"168h"` // default is 2 days
	FlushBatchSize      int            `help:"how many items in the rollups write cache before they are flushed to the database" devDefault:"20" releaseDefault:"1000" testDefault:"10"`
	FlushInterval       time.Duration  `help:"how often to flush the rollups write cache to the database" devDefault:"30s" releaseDefault:"1m" testDefault:"$TESTINTERVAL"`
	NodeStatusLogging   bool           `hidden:"true" help:"deprecated, log the offline/disqualification status of nodes" default:"false" testDefault:"true"`
	OrdersSemaphoreSize int            `help:"how many concurrent orders to process at once. zero is unlimited" default:"2"`
}

// BucketsDB returns information about buckets.
type BucketsDB interface {
	// GetBucketID returns an existing bucket id.
	GetBucketID(ctx context.Context, bucket metabase.BucketLocation) (id uuid.UUID, err error)
}

// Service for creating order limits.
//
// architecture: Service
type Service struct {
	log       *zap.Logger
	satellite signing.Signer
	overlay   *overlay.Service
	orders    DB
	buckets   BucketsDB

	encryptionKeys EncryptionKeys

	orderExpiration time.Duration

	rngMu sync.Mutex
	rng   *mathrand.Rand
}

// NewService creates new service for creating order limits.
func NewService(
	log *zap.Logger, satellite signing.Signer, overlay *overlay.Service,
	orders DB, buckets BucketsDB,
	config Config,
) (*Service, error) {
	if config.EncryptionKeys.Default.IsZero() {
		return nil, Error.New("encryption keys must be specified to include encrypted metadata")
	}

	return &Service{
		log:       log,
		satellite: satellite,
		overlay:   overlay,
		orders:    orders,
		buckets:   buckets,

		encryptionKeys: config.EncryptionKeys,

		orderExpiration: config.Expiration,

		rng: mathrand.New(mathrand.NewSource(time.Now().UnixNano())),
	}, nil
}

// VerifyOrderLimitSignature verifies that the signature inside order limit belongs to the satellite.
func (service *Service) VerifyOrderLimitSignature(ctx context.Context, signed *pb.OrderLimit) (err error) {
	defer mon.Task()(&ctx)(&err)
	return signing.VerifyOrderLimitSignature(ctx, service.satellite, signed)
}

func (service *Service) updateBandwidth(ctx context.Context, bucket metabase.BucketLocation, addressedOrderLimits ...*pb.AddressedOrderLimit) (err error) {
	defer mon.Task()(&ctx)(&err)
	if len(addressedOrderLimits) == 0 {
		return nil
	}

	var action pb.PieceAction

	var bucketAllocation int64

	for _, addressedOrderLimit := range addressedOrderLimits {
		if addressedOrderLimit != nil && addressedOrderLimit.Limit != nil {
			orderLimit := addressedOrderLimit.Limit
			action = orderLimit.Action
			bucketAllocation += orderLimit.Limit
		}
	}

	now := time.Now().UTC()
	intervalStart := time.Date(now.Year(), now.Month(), now.Day(), now.Hour(), 0, 0, 0, now.Location())

	// TODO: all of this below should be a single db transaction. in fact, this whole function should probably be part of an existing transaction
	if err := service.orders.UpdateBucketBandwidthAllocation(ctx, bucket.ProjectID, []byte(bucket.BucketName), action, bucketAllocation, intervalStart); err != nil {
		return Error.Wrap(err)
	}

	return nil
}

// CreateGetOrderLimits creates the order limits for downloading the pieces of a segment.
func (service *Service) CreateGetOrderLimits(ctx context.Context, bucket metabase.BucketLocation, segment metabase.Segment, overrideLimit int64) (_ []*pb.AddressedOrderLimit, privateKey storj.PiecePrivateKey, err error) {
	defer mon.Task()(&ctx)(&err)

	redundancy, err := eestream.NewRedundancyStrategyFromStorj(segment.Redundancy)
	if err != nil {
		return nil, storj.PiecePrivateKey{}, Error.Wrap(err)
	}
	orderLimit := eestream.CalcPieceSize(int64(segment.EncryptedSize), redundancy)
	if overrideLimit > 0 && overrideLimit < orderLimit {
		orderLimit = overrideLimit
	}

	nodeIDs := make([]storj.NodeID, len(segment.Pieces))
	for i, piece := range segment.Pieces {
		nodeIDs[i] = piece.StorageNode
	}

	nodes, err := service.overlay.GetOnlineNodesForGetDelete(ctx, nodeIDs)
	if err != nil {
		service.log.Debug("error getting nodes from overlay", zap.Error(err))
		return nil, storj.PiecePrivateKey{}, Error.Wrap(err)
	}

	signer, err := NewSignerGet(service, segment.RootPieceID, time.Now(), orderLimit, bucket)
	if err != nil {
		return nil, storj.PiecePrivateKey{}, Error.Wrap(err)
	}

	neededLimits := segment.Redundancy.DownloadNodes()

	pieces := segment.Pieces
	for _, pieceIndex := range service.perm(len(pieces)) {
		piece := pieces[pieceIndex]
		node, ok := nodes[piece.StorageNode]
		if !ok {
			continue
		}

		address := node.Address.Address
		if node.LastIPPort != "" {
			address = node.LastIPPort
		}

		_, err := signer.Sign(ctx, storj.NodeURL{
			ID:      piece.StorageNode,
			Address: address,
		}, int32(piece.Number))
		if err != nil {
			return nil, storj.PiecePrivateKey{}, Error.Wrap(err)
		}

		if len(signer.AddressedLimits) >= int(neededLimits) {
			break
		}
	}
	if len(signer.AddressedLimits) < redundancy.RequiredCount() {
		mon.Meter("download_failed_not_enough_pieces_uplink").Mark(1) //mon:locked
		return nil, storj.PiecePrivateKey{}, ErrDownloadFailedNotEnoughPieces.New("not enough orderlimits: got %d, required %d", len(signer.AddressedLimits), redundancy.RequiredCount())
	}

	if err := service.updateBandwidth(ctx, bucket, signer.AddressedLimits...); err != nil {
		return nil, storj.PiecePrivateKey{}, Error.Wrap(err)
	}

	signer.AddressedLimits, err = sortLimits(signer.AddressedLimits, segment)
	if err != nil {
		return nil, storj.PiecePrivateKey{}, err
	}
	// workaround to avoid sending nil values on top level
	for i := range signer.AddressedLimits {
		if signer.AddressedLimits[i] == nil {
			signer.AddressedLimits[i] = &pb.AddressedOrderLimit{}
		}
	}

	return signer.AddressedLimits, signer.PrivateKey, nil
}

func (service *Service) perm(n int) []int {
	service.rngMu.Lock()
	defer service.rngMu.Unlock()
	return service.rng.Perm(n)
}

// sortLimits sorts order limits and fill missing ones with nil values.
func sortLimits(limits []*pb.AddressedOrderLimit, segment metabase.Segment) ([]*pb.AddressedOrderLimit, error) {
	sorted := make([]*pb.AddressedOrderLimit, segment.Redundancy.TotalShares)
	for _, piece := range segment.Pieces {
		if int16(piece.Number) >= segment.Redundancy.TotalShares {
			return nil, Error.New("piece number is greater than redundancy total shares: got %d, max %d",
				piece.Number, (segment.Redundancy.TotalShares - 1))
		}
		sorted[piece.Number] = getLimitByStorageNodeID(limits, piece.StorageNode)
	}
	return sorted, nil
}

func getLimitByStorageNodeID(limits []*pb.AddressedOrderLimit, storageNodeID storj.NodeID) *pb.AddressedOrderLimit {
	for _, limit := range limits {
		if limit == nil || limit.GetLimit() == nil {
			continue
		}

		if limit.GetLimit().StorageNodeId == storageNodeID {
			return limit
		}
	}
	return nil
}

// CreatePutOrderLimits creates the order limits for uploading pieces to nodes.
func (service *Service) CreatePutOrderLimits(ctx context.Context, bucket metabase.BucketLocation, nodes []*overlay.SelectedNode, pieceExpiration time.Time, maxPieceSize int64) (_ storj.PieceID, _ []*pb.AddressedOrderLimit, privateKey storj.PiecePrivateKey, err error) {
	defer mon.Task()(&ctx)(&err)

	signer, err := NewSignerPut(service, pieceExpiration, time.Now(), maxPieceSize, bucket)
	if err != nil {
		return storj.PieceID{}, nil, storj.PiecePrivateKey{}, Error.Wrap(err)
	}

	for pieceNum, node := range nodes {
		address := node.Address.Address
		if node.LastIPPort != "" {
			address = node.LastIPPort
		}
		_, err := signer.Sign(ctx, storj.NodeURL{ID: node.ID, Address: address}, int32(pieceNum))
		if err != nil {
			return storj.PieceID{}, nil, storj.PiecePrivateKey{}, Error.Wrap(err)
		}
	}

	if err := service.updateBandwidth(ctx, bucket, signer.AddressedLimits...); err != nil {
		return storj.PieceID{}, nil, storj.PiecePrivateKey{}, Error.Wrap(err)
	}

	return signer.RootPieceID, signer.AddressedLimits, signer.PrivateKey, nil
}

// CreateAuditOrderLimits creates the order limits for auditing the pieces of a segment.
func (service *Service) CreateAuditOrderLimits(ctx context.Context, segment metabase.Segment, skip map[storj.NodeID]bool) (_ []*pb.AddressedOrderLimit, _ storj.PiecePrivateKey, cachedNodesInfo map[storj.NodeID]overlay.NodeReputation, err error) {
	defer mon.Task()(&ctx)(&err)

	nodeIDs := make([]storj.NodeID, len(segment.Pieces))
	for i, piece := range segment.Pieces {
		nodeIDs[i] = piece.StorageNode
	}

	nodes, err := service.overlay.GetOnlineNodesForAuditRepair(ctx, nodeIDs)
	if err != nil {
		service.log.Debug("error getting nodes from overlay", zap.Error(err))
		return nil, storj.PiecePrivateKey{}, nil, Error.Wrap(err)
	}

	bucket := metabase.BucketLocation{}
	signer, err := NewSignerAudit(service, segment.RootPieceID, time.Now(), int64(segment.Redundancy.ShareSize), bucket)
	if err != nil {
		return nil, storj.PiecePrivateKey{}, nil, Error.Wrap(err)
	}

	cachedNodesInfo = make(map[storj.NodeID]overlay.NodeReputation)
	var nodeErrors errs.Group
	var limitsCount int16
	limits := make([]*pb.AddressedOrderLimit, segment.Redundancy.TotalShares)
	for _, piece := range segment.Pieces {
		if skip[piece.StorageNode] {
			continue
		}
		node, ok := nodes[piece.StorageNode]
		if !ok {
			nodeErrors.Add(errs.New("node %q is not reliable", piece.StorageNode))
			continue
		}

		address := node.Address.Address
		cachedNodesInfo[piece.StorageNode] = *node

		limit, err := signer.Sign(ctx, storj.NodeURL{
			ID:      piece.StorageNode,
			Address: address,
		}, int32(piece.Number))
		if err != nil {
			return nil, storj.PiecePrivateKey{}, nil, Error.Wrap(err)
		}

		limits[piece.Number] = limit
		limitsCount++
	}

	if limitsCount < segment.Redundancy.RequiredShares {
		err = ErrDownloadFailedNotEnoughPieces.New("not enough nodes available: got %d, required %d", limitsCount, segment.Redundancy.RequiredShares)
		return nil, storj.PiecePrivateKey{}, nil, errs.Combine(err, nodeErrors.Err())
	}

	return limits, signer.PrivateKey, cachedNodesInfo, nil
}

// CreateAuditOrderLimit creates an order limit for auditing a single the piece from a segment.
func (service *Service) CreateAuditOrderLimit(ctx context.Context, nodeID storj.NodeID, pieceNum uint16, rootPieceID storj.PieceID, shareSize int32) (limit *pb.AddressedOrderLimit, _ storj.PiecePrivateKey, nodeInfo *overlay.NodeReputation, err error) {
	// TODO reduce number of params ?
	defer mon.Task()(&ctx)(&err)

	node, err := service.overlay.Get(ctx, nodeID)
	if err != nil {
		return nil, storj.PiecePrivateKey{}, nil, Error.Wrap(err)
	}

	nodeInfo = &overlay.NodeReputation{
		ID:         nodeID,
		Address:    node.Address,
		LastNet:    node.LastNet,
		LastIPPort: node.LastIPPort,
		Reputation: node.Reputation.Status,
	}

	if node.Disqualified != nil {
		return nil, storj.PiecePrivateKey{}, nodeInfo, overlay.ErrNodeDisqualified.New("%v", nodeID)
	}
	if node.ExitStatus.ExitFinishedAt != nil {
		return nil, storj.PiecePrivateKey{}, nodeInfo, overlay.ErrNodeFinishedGE.New("%v", nodeID)
	}
	if !service.overlay.IsOnline(node) {
		return nil, storj.PiecePrivateKey{}, nodeInfo, overlay.ErrNodeOffline.New("%v", nodeID)
	}

	signer, err := NewSignerAudit(service, rootPieceID, time.Now(), int64(shareSize), metabase.BucketLocation{})
	if err != nil {
		return nil, storj.PiecePrivateKey{}, nodeInfo, Error.Wrap(err)
	}

	orderLimit, err := signer.Sign(ctx, storj.NodeURL{
		ID:      nodeID,
		Address: node.Address.Address,
	}, int32(pieceNum))
	if err != nil {
		return nil, storj.PiecePrivateKey{}, nodeInfo, Error.Wrap(err)
	}

	return orderLimit, signer.PrivateKey, nodeInfo, nil
}

// CreateGetRepairOrderLimits creates the order limits for downloading the
// healthy pieces of segment as the source for repair.
//
// The length of the returned orders slice is the total number of pieces of the
// segment, setting to null the ones which don't correspond to a healthy piece.
func (service *Service) CreateGetRepairOrderLimits(ctx context.Context, bucket metabase.BucketLocation, segment metabase.Segment, healthy metabase.Pieces) (_ []*pb.AddressedOrderLimit, _ storj.PiecePrivateKey, cachedNodesInfo map[storj.NodeID]overlay.NodeReputation, err error) {
	defer mon.Task()(&ctx)(&err)

	redundancy, err := eestream.NewRedundancyStrategyFromStorj(segment.Redundancy)
	if err != nil {
		return nil, storj.PiecePrivateKey{}, nil, Error.Wrap(err)
	}

	pieceSize := eestream.CalcPieceSize(int64(segment.EncryptedSize), redundancy)
	totalPieces := redundancy.TotalCount()

	nodeIDs := make([]storj.NodeID, len(segment.Pieces))
	for i, piece := range segment.Pieces {
		nodeIDs[i] = piece.StorageNode
	}

	nodes, err := service.overlay.GetOnlineNodesForAuditRepair(ctx, nodeIDs)
	if err != nil {
		service.log.Debug("error getting nodes from overlay", zap.Error(err))
		return nil, storj.PiecePrivateKey{}, nil, Error.Wrap(err)
	}

	signer, err := NewSignerRepairGet(service, segment.RootPieceID, time.Now(), pieceSize, bucket)
	if err != nil {
		return nil, storj.PiecePrivateKey{}, nil, Error.Wrap(err)
	}

	cachedNodesInfo = make(map[storj.NodeID]overlay.NodeReputation, len(healthy))
	var nodeErrors errs.Group
	var limitsCount int
	limits := make([]*pb.AddressedOrderLimit, totalPieces)
	for _, piece := range healthy {
		node, ok := nodes[piece.StorageNode]
		if !ok {
			nodeErrors.Add(errs.New("node %q is not reliable", piece.StorageNode))
			continue
		}

		cachedNodesInfo[piece.StorageNode] = *node

		limit, err := signer.Sign(ctx, storj.NodeURL{
			ID:      piece.StorageNode,
			Address: node.Address.Address,
		}, int32(piece.Number))
		if err != nil {
			return nil, storj.PiecePrivateKey{}, nil, Error.Wrap(err)
		}

		limits[piece.Number] = limit
		limitsCount++
	}

	if limitsCount < redundancy.RequiredCount() {
		err = ErrDownloadFailedNotEnoughPieces.New("not enough nodes available: got %d, required %d", limitsCount, redundancy.RequiredCount())
		return nil, storj.PiecePrivateKey{}, nil, errs.Combine(err, nodeErrors.Err())
	}

	if err := service.updateBandwidth(ctx, bucket, limits...); err != nil {
		return nil, storj.PiecePrivateKey{}, nil, Error.Wrap(err)
	}

	return limits, signer.PrivateKey, cachedNodesInfo, nil
}

// CreatePutRepairOrderLimits creates the order limits for uploading the repaired pieces of segment to newNodes.
func (service *Service) CreatePutRepairOrderLimits(ctx context.Context, bucket metabase.BucketLocation, segment metabase.Segment, getOrderLimits []*pb.AddressedOrderLimit, newNodes []*overlay.SelectedNode, optimalThresholdMultiplier float64, numPiecesInExcludedCountries int) (_ []*pb.AddressedOrderLimit, _ storj.PiecePrivateKey, err error) {
	defer mon.Task()(&ctx)(&err)

	// Create the order limits for being used to upload the repaired pieces
	redundancy, err := eestream.NewRedundancyStrategyFromStorj(segment.Redundancy)
	if err != nil {
		return nil, storj.PiecePrivateKey{}, Error.Wrap(err)
	}
	pieceSize := eestream.CalcPieceSize(int64(segment.EncryptedSize), redundancy)

	totalPieces := redundancy.TotalCount()
	totalPiecesAfterRepair := int(math.Ceil(float64(redundancy.OptimalThreshold())*optimalThresholdMultiplier)) + numPiecesInExcludedCountries

	if totalPiecesAfterRepair > totalPieces {
		totalPiecesAfterRepair = totalPieces
	}

	var numCurrentPieces int
	for _, o := range getOrderLimits {
		if o != nil {
			numCurrentPieces++
		}
	}

	totalPiecesToRepair := totalPiecesAfterRepair - numCurrentPieces

	limits := make([]*pb.AddressedOrderLimit, totalPieces)

	expirationDate := time.Time{}
	if segment.ExpiresAt != nil {
		expirationDate = *segment.ExpiresAt
	}

	signer, err := NewSignerRepairPut(service, segment.RootPieceID, expirationDate, time.Now(), pieceSize, bucket)
	if err != nil {
		return nil, storj.PiecePrivateKey{}, Error.Wrap(err)
	}

	var pieceNum int32
	for _, node := range newNodes {
		for int(pieceNum) < totalPieces && getOrderLimits[pieceNum] != nil {
			pieceNum++
		}

		if int(pieceNum) >= totalPieces { // should not happen
			return nil, storj.PiecePrivateKey{}, Error.New("piece num greater than total pieces: %d >= %d", pieceNum, totalPieces)
		}

		limit, err := signer.Sign(ctx, storj.NodeURL{
			ID:      node.ID,
			Address: node.Address.Address,
		}, pieceNum)
		if err != nil {
			return nil, storj.PiecePrivateKey{}, Error.Wrap(err)
		}

		limits[pieceNum] = limit
		pieceNum++
		totalPiecesToRepair--

		if totalPiecesToRepair == 0 {
			break
		}
	}

	if err := service.updateBandwidth(ctx, bucket, limits...); err != nil {
		return nil, storj.PiecePrivateKey{}, Error.Wrap(err)
	}

	return limits, signer.PrivateKey, nil
}

// CreateGracefulExitPutOrderLimit creates an order limit for graceful exit put transfers.
func (service *Service) CreateGracefulExitPutOrderLimit(ctx context.Context, bucket metabase.BucketLocation, nodeID storj.NodeID, pieceNum int32, rootPieceID storj.PieceID, shareSize int32) (limit *pb.AddressedOrderLimit, _ storj.PiecePrivateKey, err error) {
	defer mon.Task()(&ctx)(&err)

	// should this use KnownReliable or similar?
	node, err := service.overlay.Get(ctx, nodeID)
	if err != nil {
		return nil, storj.PiecePrivateKey{}, Error.Wrap(err)
	}
	if node.Disqualified != nil {
		return nil, storj.PiecePrivateKey{}, overlay.ErrNodeDisqualified.New("%v", nodeID)
	}
	if !service.overlay.IsOnline(node) {
		return nil, storj.PiecePrivateKey{}, overlay.ErrNodeOffline.New("%v", nodeID)
	}

	signer, err := NewSignerGracefulExit(service, rootPieceID, time.Now(), shareSize, bucket)
	if err != nil {
		return nil, storj.PiecePrivateKey{}, Error.Wrap(err)
	}

	address := node.Address.Address
	if node.LastIPPort != "" {
		address = node.LastIPPort
	}
	nodeURL := storj.NodeURL{ID: nodeID, Address: address}
	limit, err = signer.Sign(ctx, nodeURL, pieceNum)
	if err != nil {
		return nil, storj.PiecePrivateKey{}, Error.Wrap(err)
	}

	if err := service.updateBandwidth(ctx, bucket, limit); err != nil {
		return nil, storj.PiecePrivateKey{}, Error.Wrap(err)
	}

	return limit, signer.PrivateKey, nil
}

// UpdateGetInlineOrder updates amount of inline GET bandwidth for given bucket.
func (service *Service) UpdateGetInlineOrder(ctx context.Context, bucket metabase.BucketLocation, amount int64) (err error) {
	defer mon.Task()(&ctx)(&err)
	now := time.Now().UTC()
	intervalStart := time.Date(now.Year(), now.Month(), now.Day(), now.Hour(), 0, 0, 0, now.Location())

	return service.orders.UpdateBucketBandwidthInline(ctx, bucket.ProjectID, []byte(bucket.BucketName), pb.PieceAction_GET, amount, intervalStart)
}

// UpdatePutInlineOrder updates amount of inline PUT bandwidth for given bucket.
func (service *Service) UpdatePutInlineOrder(ctx context.Context, bucket metabase.BucketLocation, amount int64) (err error) {
	defer mon.Task()(&ctx)(&err)
	now := time.Now().UTC()
	intervalStart := time.Date(now.Year(), now.Month(), now.Day(), now.Hour(), 0, 0, 0, now.Location())

	return service.orders.UpdateBucketBandwidthInline(ctx, bucket.ProjectID, []byte(bucket.BucketName), pb.PieceAction_PUT, amount, intervalStart)
}

// DecryptOrderMetadata decrypts the order metadata.
func (service *Service) DecryptOrderMetadata(ctx context.Context, order *pb.OrderLimit) (_ *internalpb.OrderLimitMetadata, err error) {
	defer mon.Task()(&ctx)(&err)

	var orderKeyID EncryptionKeyID
	copy(orderKeyID[:], order.EncryptedMetadataKeyId)

	key := service.encryptionKeys.Default
	if key.ID != orderKeyID {
		val, ok := service.encryptionKeys.KeyByID[orderKeyID]
		if !ok {
			return nil, ErrDecryptOrderMetadata.New("no encryption key found that matches the order.EncryptedMetadataKeyId")
		}
		key = EncryptionKey{
			ID:  orderKeyID,
			Key: val,
		}
	}
	return key.DecryptMetadata(order.SerialNumber, order.EncryptedMetadata)
}
