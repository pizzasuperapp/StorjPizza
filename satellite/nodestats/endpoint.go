// Copyright (C) 2019 Storj Labs, Inc.
// See LICENSE for copying information.

package nodestats

import (
	"context"

	"github.com/spacemonkeygo/monkit/v3"
	"go.uber.org/zap"

	"storj.io/common/identity"
	"storj.io/common/pb"
	"storj.io/common/rpc/rpcstatus"
	"storj.io/storj/satellite/accounting"
	"storj.io/storj/satellite/overlay"
	"storj.io/storj/satellite/payments/paymentsconfig"
	"storj.io/storj/satellite/reputation"
)

var (
	mon = monkit.Package()
)

// Endpoint for querying node stats for the SNO.
//
// architecture: Endpoint
type Endpoint struct {
	pb.DRPCNodeStatsUnimplementedServer

	log        *zap.Logger
	overlay    overlay.DB
	reputation *reputation.Service
	accounting accounting.StoragenodeAccounting
	config     paymentsconfig.Config
}

// NewEndpoint creates new endpoint.
func NewEndpoint(log *zap.Logger, overlay overlay.DB, reputation *reputation.Service, accounting accounting.StoragenodeAccounting, config paymentsconfig.Config) *Endpoint {
	return &Endpoint{
		log:        log,
		overlay:    overlay,
		reputation: reputation,
		accounting: accounting,
		config:     config,
	}
}

// GetStats sends node stats for client node.
func (e *Endpoint) GetStats(ctx context.Context, req *pb.GetStatsRequest) (_ *pb.GetStatsResponse, err error) {
	defer mon.Task()(&ctx)(&err)

	peer, err := identity.PeerIdentityFromContext(ctx)
	if err != nil {
		return nil, rpcstatus.Error(rpcstatus.Unauthenticated, err.Error())
	}
	node, err := e.overlay.Get(ctx, peer.ID)
	if err != nil {
		if overlay.ErrNodeNotFound.Has(err) {
			return nil, nil
		}
		e.log.Error("overlay.Get failed", zap.Error(err))
		return nil, rpcstatus.Error(rpcstatus.Internal, err.Error())
	}
	reputationInfo, err := e.reputation.Get(ctx, peer.ID)
	if err != nil {
		e.log.Error("reputation.Get failed", zap.Error(err))
		return nil, rpcstatus.Error(rpcstatus.Internal, err.Error())
	}

	auditScore := calculateReputationScore(
		reputationInfo.AuditReputationAlpha,
		reputationInfo.AuditReputationBeta)

	unknownScore := calculateReputationScore(
		reputationInfo.UnknownAuditReputationAlpha,
		reputationInfo.UnknownAuditReputationBeta)

	return &pb.GetStatsResponse{
		AuditCheck: &pb.ReputationStats{
			TotalCount:             reputationInfo.TotalAuditCount,
			SuccessCount:           reputationInfo.AuditSuccessCount,
			ReputationAlpha:        reputationInfo.AuditReputationAlpha,
			ReputationBeta:         reputationInfo.AuditReputationBeta,
			UnknownReputationAlpha: reputationInfo.UnknownAuditReputationAlpha,
			UnknownReputationBeta:  reputationInfo.UnknownAuditReputationBeta,
			ReputationScore:        auditScore,
			UnknownReputationScore: unknownScore,
		},
		OnlineScore:        reputationInfo.OnlineScore,
		Disqualified:       node.Disqualified,
		Suspended:          node.UnknownAuditSuspended,
		OfflineSuspended:   node.OfflineSuspended,
		OfflineUnderReview: reputationInfo.UnderReview,
		VettedAt:           node.Reputation.Status.VettedAt,
		AuditHistory:       reputation.DuplicateAuditHistory(reputationInfo.AuditHistory),
		JoinedAt:           node.CreatedAt,
	}, nil
}

// DailyStorageUsage returns slice of daily storage usage for given period of time sorted in ASC order by date.
func (e *Endpoint) DailyStorageUsage(ctx context.Context, req *pb.DailyStorageUsageRequest) (_ *pb.DailyStorageUsageResponse, err error) {
	defer mon.Task()(&ctx)(&err)

	peer, err := identity.PeerIdentityFromContext(ctx)
	if err != nil {
		return nil, rpcstatus.Error(rpcstatus.Unauthenticated, err.Error())
	}
	node, err := e.overlay.Get(ctx, peer.ID)
	if err != nil {
		if overlay.ErrNodeNotFound.Has(err) {
			return nil, nil
		}
		e.log.Error("overlay.Get failed", zap.Error(err))
		return nil, rpcstatus.Error(rpcstatus.Internal, err.Error())
	}

	nodeSpaceUsages, err := e.accounting.QueryStorageNodeUsage(ctx, node.Id, req.GetFrom(), req.GetTo())
	if err != nil {
		e.log.Error("accounting.QueryStorageNodeUsage failed", zap.Error(err))
		return nil, rpcstatus.Error(rpcstatus.Internal, err.Error())
	}

	return &pb.DailyStorageUsageResponse{
		NodeId:            node.Id,
		DailyStorageUsage: toProtoDailyStorageUsage(nodeSpaceUsages),
	}, nil
}

// PricingModel returns pricing model for storagenode.
func (e *Endpoint) PricingModel(ctx context.Context, req *pb.PricingModelRequest) (_ *pb.PricingModelResponse, err error) {
	defer mon.Task()(&ctx)(&err)

	return &pb.PricingModelResponse{
		EgressBandwidthPrice: e.config.NodeEgressBandwidthPrice,
		RepairBandwidthPrice: e.config.NodeRepairBandwidthPrice,
		DiskSpacePrice:       e.config.NodeDiskSpacePrice,
		AuditBandwidthPrice:  e.config.NodeAuditBandwidthPrice,
	}, nil
}

// toProtoDailyStorageUsage converts StorageNodeUsage to PB DailyStorageUsageResponse_StorageUsage.
func toProtoDailyStorageUsage(usages []accounting.StorageNodeUsage) []*pb.DailyStorageUsageResponse_StorageUsage {
	var pbUsages []*pb.DailyStorageUsageResponse_StorageUsage

	for _, usage := range usages {
		pbUsages = append(pbUsages, &pb.DailyStorageUsageResponse_StorageUsage{
			AtRestTotal: usage.StorageUsed,
			Timestamp:   usage.Timestamp,
		})
	}

	return pbUsages
}

// calculateReputationScore is helper method to calculate reputation score value.
func calculateReputationScore(alpha, beta float64) float64 {
	return alpha / (alpha + beta)
}
