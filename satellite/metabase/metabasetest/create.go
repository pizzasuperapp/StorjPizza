// Copyright (C) 2021 Storj Labs, Inc.
// See LICENSE for copying information.

package metabasetest

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"storj.io/common/storj"
	"storj.io/common/testcontext"
	"storj.io/common/testrand"
	"storj.io/common/uuid"
	"storj.io/storj/satellite/metabase"
)

// RandObjectStream returns a random object stream.
func RandObjectStream() metabase.ObjectStream {
	return metabase.ObjectStream{
		ProjectID:  testrand.UUID(),
		BucketName: testrand.BucketName(),
		ObjectKey:  RandObjectKey(),
		Version:    1,
		StreamID:   testrand.UUID(),
	}
}

// RandObjectKey returns a random object key.
func RandObjectKey() metabase.ObjectKey {
	return metabase.ObjectKey(testrand.Bytes(16))
}

// CreatePendingObject creates a new pending object with the specified number of segments.
func CreatePendingObject(ctx *testcontext.Context, t *testing.T, db *metabase.DB, obj metabase.ObjectStream, numberOfSegments byte) {
	BeginObjectExactVersion{
		Opts: metabase.BeginObjectExactVersion{
			ObjectStream: obj,
			Encryption:   DefaultEncryption,
		},
		Version: obj.Version,
	}.Check(ctx, t, db)

	for i := byte(0); i < numberOfSegments; i++ {
		BeginSegment{
			Opts: metabase.BeginSegment{
				ObjectStream: obj,
				Position:     metabase.SegmentPosition{Part: 0, Index: uint32(i)},
				RootPieceID:  storj.PieceID{i + 1},
				Pieces: []metabase.Piece{{
					Number:      1,
					StorageNode: testrand.NodeID(),
				}},
			},
		}.Check(ctx, t, db)

		CommitSegment{
			Opts: metabase.CommitSegment{
				ObjectStream: obj,
				Position:     metabase.SegmentPosition{Part: 0, Index: uint32(i)},
				RootPieceID:  storj.PieceID{1},
				Pieces:       metabase.Pieces{{Number: 0, StorageNode: storj.NodeID{2}}},

				EncryptedKey:      []byte{3},
				EncryptedKeyNonce: []byte{4},
				EncryptedETag:     []byte{5},

				EncryptedSize: 1024,
				PlainSize:     512,
				PlainOffset:   0,
				Redundancy:    DefaultRedundancy,
			},
		}.Check(ctx, t, db)
	}
}

// CreateObject creates a new committed object with the specified number of segments.
func CreateObject(ctx *testcontext.Context, t *testing.T, db *metabase.DB, obj metabase.ObjectStream, numberOfSegments byte) metabase.Object {
	BeginObjectExactVersion{
		Opts: metabase.BeginObjectExactVersion{
			ObjectStream: obj,
			Encryption:   DefaultEncryption,
		},
		Version: obj.Version,
	}.Check(ctx, t, db)

	for i := byte(0); i < numberOfSegments; i++ {
		BeginSegment{
			Opts: metabase.BeginSegment{
				ObjectStream: obj,
				Position:     metabase.SegmentPosition{Part: 0, Index: uint32(i)},
				RootPieceID:  storj.PieceID{i + 1},
				Pieces: []metabase.Piece{{
					Number:      1,
					StorageNode: testrand.NodeID(),
				}},
			},
		}.Check(ctx, t, db)

		CommitSegment{
			Opts: metabase.CommitSegment{
				ObjectStream: obj,
				Position:     metabase.SegmentPosition{Part: 0, Index: uint32(i)},
				RootPieceID:  storj.PieceID{1},
				Pieces:       metabase.Pieces{{Number: 0, StorageNode: storj.NodeID{2}}},

				EncryptedKey:      []byte{3},
				EncryptedKeyNonce: []byte{4},
				EncryptedETag:     []byte{5},

				EncryptedSize: 1024,
				PlainSize:     512,
				PlainOffset:   0,
				Redundancy:    DefaultRedundancy,
			},
		}.Check(ctx, t, db)
	}

	return CommitObject{
		Opts: metabase.CommitObject{
			ObjectStream: obj,
		},
	}.Check(ctx, t, db)
}

// CreateExpiredObject creates a new committed expired object with the specified number of segments.
func CreateExpiredObject(ctx *testcontext.Context, t *testing.T, db *metabase.DB, obj metabase.ObjectStream, numberOfSegments byte, expiresAt time.Time) metabase.Object {
	BeginObjectExactVersion{
		Opts: metabase.BeginObjectExactVersion{
			ObjectStream: obj,
			Encryption:   DefaultEncryption,
			ExpiresAt:    &expiresAt,
		},
		Version: obj.Version,
	}.Check(ctx, t, db)

	for i := byte(0); i < numberOfSegments; i++ {
		BeginSegment{
			Opts: metabase.BeginSegment{
				ObjectStream: obj,
				Position:     metabase.SegmentPosition{Part: 0, Index: uint32(i)},
				RootPieceID:  storj.PieceID{i + 1},
				Pieces: []metabase.Piece{{
					Number:      1,
					StorageNode: testrand.NodeID(),
				}},
			},
		}.Check(ctx, t, db)

		CommitSegment{
			Opts: metabase.CommitSegment{
				ObjectStream: obj,
				Position:     metabase.SegmentPosition{Part: 0, Index: uint32(i)},
				ExpiresAt:    &expiresAt,
				RootPieceID:  storj.PieceID{1},
				Pieces:       metabase.Pieces{{Number: 0, StorageNode: storj.NodeID{2}}},

				EncryptedKey:      []byte{3},
				EncryptedKeyNonce: []byte{4},
				EncryptedETag:     []byte{5},

				EncryptedSize: 1024,
				PlainSize:     512,
				PlainOffset:   0,
				Redundancy:    DefaultRedundancy,
			},
		}.Check(ctx, t, db)
	}

	return CommitObject{
		Opts: metabase.CommitObject{
			ObjectStream: obj,
		},
	}.Check(ctx, t, db)
}

// CreateFullObjectsWithKeys creates multiple objects with the specified keys.
func CreateFullObjectsWithKeys(ctx *testcontext.Context, t *testing.T, db *metabase.DB, projectID uuid.UUID, bucketName string, keys []metabase.ObjectKey) map[metabase.ObjectKey]metabase.LoopObjectEntry {
	objects := make(map[metabase.ObjectKey]metabase.LoopObjectEntry, len(keys))
	for _, key := range keys {
		obj := RandObjectStream()
		obj.ProjectID = projectID
		obj.BucketName = bucketName
		obj.ObjectKey = key

		CreateObject(ctx, t, db, obj, 0)

		objects[key] = metabase.LoopObjectEntry{
			ObjectStream: obj,
			Status:       metabase.Committed,
			CreatedAt:    time.Now(),
		}
	}

	return objects
}

// CreateTestObject is for testing metabase.CreateTestObject.
type CreateTestObject struct {
	BeginObjectExactVersion *metabase.BeginObjectExactVersion
	CommitObject            *metabase.CommitObject
	// TODO add BeginSegment, CommitSegment
}

// Run runs the test.
func (co CreateTestObject) Run(ctx *testcontext.Context, t testing.TB, db *metabase.DB, obj metabase.ObjectStream, numberOfSegments byte) (metabase.Object, []metabase.Segment) {
	boeOpts := metabase.BeginObjectExactVersion{
		ObjectStream: obj,
		Encryption:   DefaultEncryption,
	}
	if co.BeginObjectExactVersion != nil {
		boeOpts = *co.BeginObjectExactVersion
	}

	BeginObjectExactVersion{
		Opts:    boeOpts,
		Version: obj.Version,
	}.Check(ctx, t, db)

	createdSegments := []metabase.Segment{}

	for i := byte(0); i < numberOfSegments; i++ {
		BeginSegment{
			Opts: metabase.BeginSegment{
				ObjectStream: obj,
				Position:     metabase.SegmentPosition{Part: 0, Index: uint32(i)},
				RootPieceID:  storj.PieceID{i + 1},
				Pieces: []metabase.Piece{{
					Number:      1,
					StorageNode: testrand.NodeID(),
				}},
			},
		}.Check(ctx, t, db)

		commitSegmentOpts := metabase.CommitSegment{
			ObjectStream: obj,
			ExpiresAt:    boeOpts.ExpiresAt,
			Position:     metabase.SegmentPosition{Part: 0, Index: uint32(i)},
			RootPieceID:  storj.PieceID{1},
			Pieces:       metabase.Pieces{{Number: 0, StorageNode: storj.NodeID{2}}},

			EncryptedKey:      []byte{3},
			EncryptedKeyNonce: []byte{4},
			EncryptedETag:     []byte{5},

			EncryptedSize: 1060,
			PlainSize:     512,
			PlainOffset:   int64(i) * 512,
			Redundancy:    DefaultRedundancy,
		}

		CommitSegment{
			Opts: commitSegmentOpts,
		}.Check(ctx, t, db)

		segment, err := db.GetSegmentByPosition(ctx, metabase.GetSegmentByPosition{
			StreamID: commitSegmentOpts.StreamID,
			Position: commitSegmentOpts.Position,
		})
		require.NoError(t, err)

		createdSegments = append(createdSegments, metabase.Segment{
			StreamID: obj.StreamID,
			Position: commitSegmentOpts.Position,

			CreatedAt:  segment.CreatedAt,
			RepairedAt: nil,
			ExpiresAt:  nil,

			RootPieceID:       commitSegmentOpts.RootPieceID,
			EncryptedKeyNonce: commitSegmentOpts.EncryptedKeyNonce,
			EncryptedKey:      commitSegmentOpts.EncryptedKey,

			EncryptedSize: commitSegmentOpts.EncryptedSize,
			PlainSize:     commitSegmentOpts.PlainSize,
			PlainOffset:   commitSegmentOpts.PlainOffset,
			EncryptedETag: commitSegmentOpts.EncryptedETag,

			Redundancy: commitSegmentOpts.Redundancy,

			InlineData: nil,
			Pieces:     commitSegmentOpts.Pieces,

			Placement: segment.Placement,
		})
	}

	coOpts := metabase.CommitObject{
		ObjectStream: obj,
	}
	if co.CommitObject != nil {
		coOpts = *co.CommitObject
	}

	createdObject := CommitObject{
		Opts: coOpts,
	}.Check(ctx, t, db)

	return createdObject, createdSegments
}

// CreateObjectCopy is for testing object copy.
type CreateObjectCopy struct {
	OriginalObject metabase.Object
	// if empty, creates fake segments if necessary
	OriginalSegments []metabase.Segment
	FinishObject     *metabase.FinishCopyObject
	CopyObjectStream *metabase.ObjectStream
}

// Run creates the copy.
func (cc CreateObjectCopy) Run(ctx *testcontext.Context, t testing.TB, db *metabase.DB) (copyObj metabase.Object, expectedOriginalSegments []metabase.RawSegment, expectedCopySegments []metabase.RawSegment) {
	var copyStream metabase.ObjectStream
	if cc.CopyObjectStream != nil {
		copyStream = *cc.CopyObjectStream
	} else {
		copyStream = RandObjectStream()
	}

	newEncryptedKeysNonces := make([]metabase.EncryptedKeyAndNonce, cc.OriginalObject.SegmentCount)
	expectedOriginalSegments = make([]metabase.RawSegment, cc.OriginalObject.SegmentCount)
	expectedCopySegments = make([]metabase.RawSegment, cc.OriginalObject.SegmentCount)
	expectedEncryptedSize := 1060

	for i := 0; i < int(cc.OriginalObject.SegmentCount); i++ {
		newEncryptedKeysNonces[i] = metabase.EncryptedKeyAndNonce{
			Position:          metabase.SegmentPosition{Index: uint32(i)},
			EncryptedKeyNonce: testrand.Nonce().Bytes(),
			EncryptedKey:      testrand.Bytes(32),
		}

		expectedOriginalSegments[i] = DefaultRawSegment(cc.OriginalObject.ObjectStream, metabase.SegmentPosition{Index: uint32(i)})

		// TODO: place this calculation in metabasetest.
		expectedOriginalSegments[i].PlainOffset = int64(int32(i) * expectedOriginalSegments[i].PlainSize)
		// TODO: we should use the same value for encrypted size in both test methods.
		expectedOriginalSegments[i].EncryptedSize = int32(expectedEncryptedSize)

		expectedCopySegments[i] = metabase.RawSegment{}
		expectedCopySegments[i].StreamID = copyStream.StreamID
		expectedCopySegments[i].EncryptedKeyNonce = newEncryptedKeysNonces[i].EncryptedKeyNonce
		expectedCopySegments[i].EncryptedKey = newEncryptedKeysNonces[i].EncryptedKey
		expectedCopySegments[i].EncryptedSize = expectedOriginalSegments[i].EncryptedSize
		expectedCopySegments[i].Position = expectedOriginalSegments[i].Position
		expectedCopySegments[i].RootPieceID = expectedOriginalSegments[i].RootPieceID
		expectedCopySegments[i].Redundancy = expectedOriginalSegments[i].Redundancy
		expectedCopySegments[i].PlainSize = expectedOriginalSegments[i].PlainSize
		expectedCopySegments[i].PlainOffset = expectedOriginalSegments[i].PlainOffset
		expectedCopySegments[i].CreatedAt = time.Now().UTC()
		if len(expectedOriginalSegments[i].InlineData) > 0 {
			expectedCopySegments[i].InlineData = expectedOriginalSegments[i].InlineData
		} else {
			expectedCopySegments[i].InlineData = []byte{}
		}
	}

	opts := cc.FinishObject
	if opts == nil {
		opts = &metabase.FinishCopyObject{
			NewStreamID:                  copyStream.StreamID,
			NewBucket:                    copyStream.BucketName,
			ObjectStream:                 cc.OriginalObject.ObjectStream,
			NewSegmentKeys:               newEncryptedKeysNonces,
			NewEncryptedObjectKey:        copyStream.ObjectKey,
			NewEncryptedMetadataKeyNonce: testrand.Nonce(),
			NewEncryptedMetadataKey:      testrand.Bytes(32),
		}
	}

	copyObj, err := db.FinishCopyObject(ctx, *opts)
	require.NoError(t, err)

	return copyObj, expectedOriginalSegments, expectedCopySegments
}

// SegmentsToRaw converts a slice of Segment to a slice of RawSegment.
func SegmentsToRaw(segments []metabase.Segment) []metabase.RawSegment {
	rawSegments := []metabase.RawSegment{}

	for _, segment := range segments {
		rawSegments = append(rawSegments, metabase.RawSegment(segment))
	}

	return rawSegments
}
