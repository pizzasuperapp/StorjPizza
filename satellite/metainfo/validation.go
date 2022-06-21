// Copyright (C) 2019 Storj Labs, Inc.
// See LICENSE for copying information.

package metainfo

import (
	"bytes"
	"context"
	"crypto/subtle"
	"regexp"
	"strconv"
	"time"

	"github.com/zeebo/errs"
	"go.uber.org/zap"
	"golang.org/x/time/rate"

	"storj.io/common/encryption"
	"storj.io/common/errs2"
	"storj.io/common/macaroon"
	"storj.io/common/pb"
	"storj.io/common/rpc/rpcstatus"
	"storj.io/common/storj"
	"storj.io/common/uuid"
	"storj.io/storj/satellite/console"
	"storj.io/storj/satellite/console/consoleauth"
	"storj.io/storj/satellite/metabase"
)

var (
	ipRegexp = regexp.MustCompile(`^(([0-9]|[1-9][0-9]|1[0-9]{2}|2[0-4][0-9]|25[0-5])\.){3}([0-9]|[1-9][0-9]|1[0-9]{2}|2[0-4][0-9]|25[0-5])$`)
)

func getAPIKey(ctx context.Context, header *pb.RequestHeader) (key *macaroon.APIKey, err error) {
	defer mon.Task()(&ctx)(&err)
	if header != nil {
		return macaroon.ParseRawAPIKey(header.ApiKey)
	}

	keyData, ok := consoleauth.GetAPIKey(ctx)
	if !ok {
		return nil, errs.New("missing credentials")
	}

	return macaroon.ParseAPIKey(string(keyData))
}

// validateAuth validates things like API key, user permissions and rate limit and always returns valid rpc error.
func (endpoint *Endpoint) validateAuth(ctx context.Context, header *pb.RequestHeader, action macaroon.Action) (_ *console.APIKeyInfo, err error) {
	defer mon.Task()(&ctx)(&err)

	key, keyInfo, err := endpoint.validateBasic(ctx, header)
	if err != nil {
		return nil, err
	}

	err = key.Check(ctx, keyInfo.Secret, action, endpoint.revocations)
	if err != nil {
		endpoint.log.Debug("unauthorized request", zap.Error(err))
		return nil, rpcstatus.Error(rpcstatus.PermissionDenied, "Unauthorized API credentials")
	}

	return keyInfo, nil
}

type verifyPermission struct {
	action          macaroon.Action
	actionPermitted *bool
	optional        bool
}

// validateAuthN validates things like API keys, rate limit and user permissions
// for each permission from permissions. It returns an error for the first
// required (not optional) permission that the check fails for. There must be at
// least one required (not optional) permission. In case all permissions are
// optional, it will return an error. It always returns valid RPC errors.
func (endpoint *Endpoint) validateAuthN(ctx context.Context, header *pb.RequestHeader, permissions ...verifyPermission) (_ *console.APIKeyInfo, err error) {
	defer mon.Task()(&ctx)(&err)

	allOptional := true

	for _, p := range permissions {
		if !p.optional {
			allOptional = false
			break
		}
	}

	if allOptional {
		return nil, rpcstatus.Error(rpcstatus.Internal, "All permissions are optional")
	}

	key, keyInfo, err := endpoint.validateBasic(ctx, header)
	if err != nil {
		return nil, err
	}

	for _, p := range permissions {
		err = key.Check(ctx, keyInfo.Secret, p.action, endpoint.revocations)
		if p.actionPermitted != nil {
			*p.actionPermitted = err == nil
		}
		if err != nil && !p.optional {
			endpoint.log.Debug("unauthorized request", zap.Error(err))
			return nil, rpcstatus.Error(rpcstatus.PermissionDenied, "Unauthorized API credentials")
		}
	}

	return keyInfo, nil
}

func (endpoint *Endpoint) validateBasic(ctx context.Context, header *pb.RequestHeader) (_ *macaroon.APIKey, _ *console.APIKeyInfo, err error) {
	defer mon.Task()(&ctx)(&err)

	key, err := getAPIKey(ctx, header)
	if err != nil {
		endpoint.log.Debug("invalid request", zap.Error(err))
		return nil, nil, rpcstatus.Error(rpcstatus.InvalidArgument, "Invalid API credentials")
	}

	keyInfo, err := endpoint.apiKeys.GetByHead(ctx, key.Head())
	if err != nil {
		endpoint.log.Debug("unauthorized request", zap.Error(err))
		return nil, nil, rpcstatus.Error(rpcstatus.PermissionDenied, "Unauthorized API credentials")
	}

	if err = endpoint.checkRate(ctx, keyInfo.ProjectID); err != nil {
		endpoint.log.Debug("rate check failed", zap.Error(err))
		return nil, nil, err
	}

	return key, keyInfo, nil
}

func (endpoint *Endpoint) validateRevoke(ctx context.Context, header *pb.RequestHeader, macToRevoke *macaroon.Macaroon) (_ *console.APIKeyInfo, err error) {
	defer mon.Task()(&ctx)(&err)
	key, keyInfo, err := endpoint.validateBasic(ctx, header)
	if err != nil {
		return nil, err
	}

	// The macaroon to revoke must be valid with the same secret as the key.
	if !macToRevoke.Validate(keyInfo.Secret) {
		return nil, rpcstatus.Error(rpcstatus.InvalidArgument, "Macaroon to revoke invalid")
	}

	keyTail := key.Tail()
	tails := macToRevoke.Tails(keyInfo.Secret)

	// A macaroon cannot revoke itself. So we only check len(tails-1), skipping
	// the final tail.  To be valid, the final tail of the auth key must be
	// contained within the checked tails of the macaroon we want to revoke.
	for i := 0; i < len(tails)-1; i++ {
		if subtle.ConstantTimeCompare(tails[i], keyTail) == 1 {
			return keyInfo, nil
		}
	}
	return nil, rpcstatus.Error(rpcstatus.PermissionDenied, "Unauthorized attempt to revoke macaroon")
}

func (endpoint *Endpoint) checkRate(ctx context.Context, projectID uuid.UUID) (err error) {
	defer mon.Task()(&ctx)(&err)
	if !endpoint.config.RateLimiter.Enabled {
		return nil
	}
	limiter, err := endpoint.limiterCache.Get(projectID.String(), func() (interface{}, error) {
		rateLimit := rate.Limit(endpoint.config.RateLimiter.Rate)
		burstLimit := int(endpoint.config.RateLimiter.Rate)

		project, err := endpoint.projects.Get(ctx, projectID)
		if err != nil {
			return false, err
		}
		if project.RateLimit != nil {
			rateLimit = rate.Limit(*project.RateLimit)
			burstLimit = *project.RateLimit
		}
		// use the explicitly set burst value if it's defined
		if project.BurstLimit != nil {
			burstLimit = *project.BurstLimit
		}

		return rate.NewLimiter(rateLimit, burstLimit), nil
	})
	if err != nil {
		return rpcstatus.Error(rpcstatus.Unavailable, err.Error())
	}

	if !limiter.(*rate.Limiter).Allow() {
		endpoint.log.Warn("too many requests for project",
			zap.Stringer("projectID", projectID),
			zap.Float64("rate limit", float64(limiter.(*rate.Limiter).Limit())),
			zap.Float64("burst limit", float64(limiter.(*rate.Limiter).Burst())))

		mon.Event("metainfo_rate_limit_exceeded") //mon:locked

		return rpcstatus.Error(rpcstatus.ResourceExhausted, "Too Many Requests")
	}

	return nil
}

func (endpoint *Endpoint) validateBucket(ctx context.Context, bucket []byte) (err error) {
	defer mon.Task()(&ctx)(&err)

	if len(bucket) == 0 {
		return Error.Wrap(storj.ErrNoBucket.New(""))
	}

	if len(bucket) < 3 || len(bucket) > 63 {
		return Error.New("bucket name must be at least 3 and no more than 63 characters long")
	}

	// Regexp not used because benchmark shows it will be slower for valid bucket names
	// https://gist.github.com/mniewrzal/49de3af95f36e63e88fac24f565e444c
	labels := bytes.Split(bucket, []byte("."))
	for _, label := range labels {
		err = validateBucketLabel(label)
		if err != nil {
			return err
		}
	}

	if ipRegexp.MatchString(string(bucket)) {
		return Error.New("bucket name cannot be formatted as an IP address")
	}

	return nil
}

func validateBucketLabel(label []byte) error {
	if len(label) == 0 {
		return Error.New("bucket label cannot be empty")
	}

	if !isLowerLetter(label[0]) && !isDigit(label[0]) {
		return Error.New("bucket label must start with a lowercase letter or number")
	}

	if label[0] == '-' || label[len(label)-1] == '-' {
		return Error.New("bucket label cannot start or end with a hyphen")
	}

	for i := 1; i < len(label)-1; i++ {
		if !isLowerLetter(label[i]) && !isDigit(label[i]) && (label[i] != '-') && (label[i] != '.') {
			return Error.New("bucket name must contain only lowercase letters, numbers or hyphens")
		}
	}

	return nil
}

func isLowerLetter(r byte) bool {
	return r >= 'a' && r <= 'z'
}

func isDigit(r byte) bool {
	return r >= '0' && r <= '9'
}

func (endpoint *Endpoint) validateRemoteSegment(ctx context.Context, commitRequest metabase.CommitSegment, originalLimits []*pb.OrderLimit) (err error) {
	defer mon.Task()(&ctx)(&err)

	if len(originalLimits) == 0 {
		return Error.New("no order limits")
	}
	if len(originalLimits) != int(commitRequest.Redundancy.TotalShares) {
		return Error.New("invalid no order limit for piece")
	}

	maxAllowed, err := encryption.CalcEncryptedSize(endpoint.config.MaxSegmentSize.Int64(), storj.EncryptionParameters{
		CipherSuite: storj.EncAESGCM,
		BlockSize:   128, // intentionally low block size to allow maximum possible encryption overhead
	})
	if err != nil {
		return err
	}

	if int64(commitRequest.EncryptedSize) > maxAllowed || commitRequest.EncryptedSize < 0 {
		return Error.New("encrypted segment size %v is out of range, maximum allowed is %v", commitRequest.EncryptedSize, maxAllowed)
	}

	// TODO more validation for plain size and plain offset
	if commitRequest.PlainSize > commitRequest.EncryptedSize {
		return Error.New("plain segment size %v is out of range, maximum allowed is %v", commitRequest.PlainSize, commitRequest.EncryptedSize)
	}

	pieceNums := make(map[uint16]struct{})
	nodeIds := make(map[storj.NodeID]struct{})
	deriver := commitRequest.RootPieceID.Deriver()
	for _, piece := range commitRequest.Pieces {
		if int(piece.Number) >= len(originalLimits) {
			return Error.New("invalid piece number")
		}

		limit := originalLimits[piece.Number]
		if limit == nil {
			return Error.New("empty order limit for piece")
		}

		err := endpoint.orders.VerifyOrderLimitSignature(ctx, limit)
		if err != nil {
			return err
		}

		// expect that too much time has not passed between order limit creation and now
		if time.Since(limit.OrderCreation) > endpoint.config.MaxCommitInterval {
			return Error.New("Segment not committed before max commit interval of %f minutes.", endpoint.config.MaxCommitInterval.Minutes())
		}

		derivedPieceID := deriver.Derive(piece.StorageNode, int32(piece.Number))
		if limit.PieceId.IsZero() || limit.PieceId != derivedPieceID {
			return Error.New("invalid order limit piece id")
		}
		if piece.StorageNode != limit.StorageNodeId {
			return Error.New("piece NodeID != order limit NodeID")
		}

		if _, ok := pieceNums[piece.Number]; ok {
			return Error.New("piece num %d is duplicated", piece.Number)
		}

		if _, ok := nodeIds[piece.StorageNode]; ok {
			return Error.New("node id %s for piece num %d is duplicated", piece.StorageNode.String(), piece.Number)
		}

		pieceNums[piece.Number] = struct{}{}
		nodeIds[piece.StorageNode] = struct{}{}
	}

	return nil
}

func (endpoint *Endpoint) checkUploadLimits(ctx context.Context, projectID uuid.UUID) error {
	validateSegments := endpoint.config.ProjectLimits.ValidateSegmentLimit

	if limit, err := endpoint.projectUsage.ExceedsUploadLimits(ctx, projectID, validateSegments); err != nil {
		if errs2.IsCanceled(err) {
			return rpcstatus.Wrap(rpcstatus.Canceled, err)
		}

		endpoint.log.Error(
			"Retrieving project upload limit failed; limit won't be enforced",
			zap.Stringer("Project ID", projectID),
			zap.Error(err),
		)
	} else {
		if validateSegments && limit.ExceedsSegments {
			endpoint.log.Warn("Segment limit exceeded",
				zap.String("Limit", strconv.Itoa(int(limit.SegmentsLimit))),
				zap.Stringer("Project ID", projectID),
			)
			return rpcstatus.Error(rpcstatus.ResourceExhausted, "Exceeded Segments Limit")
		}

		if limit.ExceedsStorage {
			endpoint.log.Warn("Storage limit exceeded",
				zap.String("Limit", strconv.Itoa(limit.StorageLimit.Int())),
				zap.Stringer("Project ID", projectID),
			)
			return rpcstatus.Error(rpcstatus.ResourceExhausted, "Exceeded Storage Limit")
		}
	}

	return nil
}

func (endpoint *Endpoint) updateUploadLimits(ctx context.Context, projectID uuid.UUID, segmentSize int64) error {
	if err := endpoint.projectUsage.AddProjectStorageUsage(ctx, projectID, segmentSize); err != nil {
		// log it and continue. it's most likely our own fault that we couldn't
		// track it, and the only thing that will be affected is our per-project
		// bandwidth and storage limits.
		endpoint.log.Error("Could not track new project's storage usage",
			zap.Stringer("Project ID", projectID),
			zap.Error(err),
		)
	}

	if endpoint.config.ProjectLimits.ValidateSegmentLimit {
		// Update the current segment cache value incrementing by 1 as we commit single segment.
		err := endpoint.projectUsage.UpdateProjectSegmentUsage(ctx, projectID, 1)
		if err != nil {
			if errs2.IsCanceled(err) {
				return rpcstatus.Wrap(rpcstatus.Canceled, err)
			}

			// log it and continue. it's most likely our own fault that we couldn't
			// track it, and the only thing that will be affected is our per-project
			// segment limits.
			endpoint.log.Error(
				"Could not track the new project's segment usage when committing segment",
				zap.Stringer("Project ID", projectID),
				zap.Error(err),
			)
		}
	}

	return nil
}
