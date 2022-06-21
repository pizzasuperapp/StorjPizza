// Copyright (C) 2020 Storj Labs, Inc.
// See LICENSE for copying information.

package testblobs

import (
	"context"

	"go.uber.org/zap"

	"storj.io/storj/storage"
	"storj.io/storj/storagenode"
)

// ensures that limitedSpaceDB implements storagenode.DB.
var _ storagenode.DB = (*limitedSpaceDB)(nil)

// limitedSpaceDB implements storage node DB with limited free space.
type limitedSpaceDB struct {
	storagenode.DB
	log   *zap.Logger
	blobs *LimitedSpaceBlobs
}

// NewLimitedSpaceDB creates a new storage node DB with limited free space.
func NewLimitedSpaceDB(log *zap.Logger, db storagenode.DB, freeSpace int64) storagenode.DB {
	return &limitedSpaceDB{
		DB:    db,
		blobs: newLimitedSpaceBlobs(log, db.Pieces(), freeSpace),
		log:   log,
	}
}

// Pieces returns the blob store.
func (lim *limitedSpaceDB) Pieces() storage.Blobs {
	return lim.blobs
}

// LimitedSpaceBlobs implements a limited space blob store.
type LimitedSpaceBlobs struct {
	storage.Blobs
	log       *zap.Logger
	freeSpace int64
}

// newLimitedSpaceBlobs creates a new limited space blob store wrapping the provided blobs.
func newLimitedSpaceBlobs(log *zap.Logger, blobs storage.Blobs, freeSpace int64) *LimitedSpaceBlobs {
	return &LimitedSpaceBlobs{
		log:       log,
		Blobs:     blobs,
		freeSpace: freeSpace,
	}
}

// FreeSpace returns how much free space left for writing.
func (limspace *LimitedSpaceBlobs) FreeSpace(ctx context.Context) (int64, error) {
	return limspace.freeSpace, nil
}
