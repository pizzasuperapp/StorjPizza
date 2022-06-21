// Copyright (C) 2018 Storj Labs, Inc.
// See LICENSE for copying information.

package buckets

import (
	"context"
	"time"

	"storj.io/common/macaroon"
	"storj.io/common/storj"
	"storj.io/common/uuid"
	"storj.io/storj/satellite/metabase"
)

// Bucket contains minimal bucket fields for metainfo protocol.
type Bucket struct {
	Name      []byte
	CreatedAt time.Time
}

// DB is the interface for the database to interact with buckets.
//
// architecture: Database
type DB interface {
	// CreateBucket creates a new bucket
	CreateBucket(ctx context.Context, bucket storj.Bucket) (_ storj.Bucket, err error)
	// GetBucket returns an existing bucket
	GetBucket(ctx context.Context, bucketName []byte, projectID uuid.UUID) (bucket storj.Bucket, err error)
	// GetBucketPlacement returns with the placement constraint identifier.
	GetBucketPlacement(ctx context.Context, bucketName []byte, projectID uuid.UUID) (placement storj.PlacementConstraint, err error)
	// GetMinimalBucket returns existing bucket with minimal number of fields.
	GetMinimalBucket(ctx context.Context, bucketName []byte, projectID uuid.UUID) (bucket Bucket, err error)
	// HasBucket returns if a bucket exists.
	HasBucket(ctx context.Context, bucketName []byte, projectID uuid.UUID) (exists bool, err error)
	// GetBucketID returns an existing bucket id.
	GetBucketID(ctx context.Context, bucket metabase.BucketLocation) (id uuid.UUID, err error)
	// UpdateBucket updates an existing bucket
	UpdateBucket(ctx context.Context, bucket storj.Bucket) (_ storj.Bucket, err error)
	// DeleteBucket deletes a bucket
	DeleteBucket(ctx context.Context, bucketName []byte, projectID uuid.UUID) (err error)
	// ListBuckets returns all buckets for a project
	ListBuckets(ctx context.Context, projectID uuid.UUID, listOpts storj.BucketListOptions, allowedBuckets macaroon.AllowedBuckets) (bucketList storj.BucketList, err error)
	// CountBuckets returns the number of buckets a project currently has
	CountBuckets(ctx context.Context, projectID uuid.UUID) (int, error)
}
