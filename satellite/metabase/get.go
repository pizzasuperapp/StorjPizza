// Copyright (C) 2020 Storj Labs, Inc.
// See LICENSE for copying information.

package metabase

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/zeebo/errs"

	"storj.io/common/storj"
	"storj.io/common/uuid"
)

// ErrSegmentNotFound is an error class for non-existing segment.
var ErrSegmentNotFound = errs.Class("segment not found")

// Object object metadata.
// TODO define separated struct.
type Object RawObject

// IsMigrated returns whether the object comes from PointerDB.
// Pointer objects are special that they are missing some information.
//
//   * TotalPlainSize = 0 and FixedSegmentSize = 0.
//   * Segment.PlainOffset = 0, Segment.PlainSize = 0
func (obj *Object) IsMigrated() bool {
	return obj.TotalPlainSize <= 0
}

// Segment segment metadata.
// TODO define separated struct.
type Segment RawSegment

// Inline returns true if segment is inline.
func (s Segment) Inline() bool {
	return s.Redundancy.IsZero() && len(s.Pieces) == 0
}

// PiecesInAncestorSegment returns true if remote alias pieces are to be found in an ancestor segment.
func (s Segment) PiecesInAncestorSegment() bool {
	return s.EncryptedSize != 0 && len(s.InlineData) == 0 && len(s.Pieces) == 0
}

// Expired checks if segment is expired relative to now.
func (s Segment) Expired(now time.Time) bool {
	return s.ExpiresAt != nil && s.ExpiresAt.Before(now)
}

// GetObjectExactVersion contains arguments necessary for fetching an information
// about exact object version.
type GetObjectExactVersion struct {
	Version Version
	ObjectLocation
}

// Verify verifies get object reqest fields.
func (obj *GetObjectExactVersion) Verify() error {
	if err := obj.ObjectLocation.Verify(); err != nil {
		return err
	}
	if obj.Version <= 0 {
		return ErrInvalidRequest.New("Version invalid: %v", obj.Version)
	}
	return nil
}

// GetObjectExactVersion returns object information for exact version.
func (db *DB) GetObjectExactVersion(ctx context.Context, opts GetObjectExactVersion) (_ Object, err error) {
	defer mon.Task()(&ctx)(&err)

	if err := opts.Verify(); err != nil {
		return Object{}, err
	}

	object := Object{}
	err = db.db.QueryRowContext(ctx, `
		SELECT
			stream_id,
			created_at, expires_at,
			segment_count,
			encrypted_metadata_nonce, encrypted_metadata, encrypted_metadata_encrypted_key,
			total_plain_size, total_encrypted_size, fixed_segment_size,
			encryption
		FROM objects
		WHERE
			project_id   = $1 AND
			bucket_name  = $2 AND
			object_key   = $3 AND
			version      = $4 AND
			status       = `+committedStatus,
		opts.ProjectID, []byte(opts.BucketName), opts.ObjectKey, opts.Version).
		Scan(
			&object.StreamID,
			&object.CreatedAt, &object.ExpiresAt,
			&object.SegmentCount,
			&object.EncryptedMetadataNonce, &object.EncryptedMetadata, &object.EncryptedMetadataEncryptedKey,
			&object.TotalPlainSize, &object.TotalEncryptedSize, &object.FixedSegmentSize,
			encryptionParameters{&object.Encryption},
		)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return Object{}, storj.ErrObjectNotFound.Wrap(Error.Wrap(err))
		}
		return Object{}, Error.New("unable to query object status: %w", err)
	}

	object.ProjectID = opts.ProjectID
	object.BucketName = opts.BucketName
	object.ObjectKey = opts.ObjectKey
	object.Version = opts.Version

	object.Status = Committed

	return object, nil
}

// GetSegmentByPosition contains arguments necessary for fetching a segment on specific position.
type GetSegmentByPosition struct {
	StreamID uuid.UUID
	Position SegmentPosition
}

// Verify verifies get segment request fields.
func (seg *GetSegmentByPosition) Verify() error {
	if seg.StreamID.IsZero() {
		return ErrInvalidRequest.New("StreamID missing")
	}
	return nil
}

// GetSegmentByPosition returns information about segment on the specified position.
func (db *DB) GetSegmentByPosition(ctx context.Context, opts GetSegmentByPosition) (segment Segment, err error) {
	defer mon.Task()(&ctx)(&err)

	if err := opts.Verify(); err != nil {
		return Segment{}, err
	}

	var aliasPieces AliasPieces
	err = db.db.QueryRowContext(ctx, `
		SELECT
			created_at, expires_at, repaired_at,
			root_piece_id, encrypted_key_nonce, encrypted_key,
			encrypted_size, plain_offset, plain_size,
			encrypted_etag,
			redundancy,
			inline_data, remote_alias_pieces,
			placement
		FROM segments
		WHERE
			stream_id = $1 AND
			position  = $2
	`, opts.StreamID, opts.Position.Encode()).
		Scan(
			&segment.CreatedAt, &segment.ExpiresAt, &segment.RepairedAt,
			&segment.RootPieceID, &segment.EncryptedKeyNonce, &segment.EncryptedKey,
			&segment.EncryptedSize, &segment.PlainOffset, &segment.PlainSize,
			&segment.EncryptedETag,
			redundancyScheme{&segment.Redundancy},
			&segment.InlineData, &aliasPieces,
			&segment.Placement,
		)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return Segment{}, ErrSegmentNotFound.New("segment missing")
		}
		return Segment{}, Error.New("unable to query segment: %w", err)
	}

	if len(aliasPieces) > 0 {
		segment.Pieces, err = db.aliasCache.ConvertAliasesToPieces(ctx, aliasPieces)
		if err != nil {
			return Segment{}, Error.New("unable to convert aliases to pieces: %w", err)
		}
	}

	segment.StreamID = opts.StreamID
	segment.Position = opts.Position

	if db.config.ServerSideCopy {
		err = db.updateWithAncestorSegment(ctx, &segment)
		if err != nil {
			return Segment{}, err
		}
	}

	return segment, nil
}

// GetLatestObjectLastSegment contains arguments necessary for fetching a last segment information.
type GetLatestObjectLastSegment struct {
	ObjectLocation
}

// GetLatestObjectLastSegment returns an object last segment information.
func (db *DB) GetLatestObjectLastSegment(ctx context.Context, opts GetLatestObjectLastSegment) (segment Segment, err error) {
	defer mon.Task()(&ctx)(&err)

	if err := opts.Verify(); err != nil {
		return Segment{}, err
	}

	var aliasPieces AliasPieces
	err = db.db.QueryRowContext(ctx, `
		SELECT
			stream_id, position,
			created_at, repaired_at,
			root_piece_id, encrypted_key_nonce, encrypted_key,
			encrypted_size, plain_offset, plain_size,
			encrypted_etag,
			redundancy,
			inline_data, remote_alias_pieces,
			placement
		FROM segments
		WHERE
			stream_id IN (SELECT stream_id FROM objects WHERE
				project_id   = $1 AND
				bucket_name  = $2 AND
				object_key   = $3 AND
				status       = `+committedStatus+`
				ORDER BY version DESC
				LIMIT 1
			)
		ORDER BY position DESC
		LIMIT 1
	`, opts.ProjectID, []byte(opts.BucketName), opts.ObjectKey).
		Scan(
			&segment.StreamID, &segment.Position,
			&segment.CreatedAt, &segment.RepairedAt,
			&segment.RootPieceID, &segment.EncryptedKeyNonce, &segment.EncryptedKey,
			&segment.EncryptedSize, &segment.PlainOffset, &segment.PlainSize,
			&segment.EncryptedETag,
			redundancyScheme{&segment.Redundancy},
			&segment.InlineData, &aliasPieces,
			&segment.Placement,
		)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return Segment{}, storj.ErrObjectNotFound.Wrap(Error.New("object or segment missing"))
		}
		return Segment{}, Error.New("unable to query segment: %w", err)
	}

	if len(aliasPieces) > 0 {
		segment.Pieces, err = db.aliasCache.ConvertAliasesToPieces(ctx, aliasPieces)
		if err != nil {
			return Segment{}, Error.New("unable to convert aliases to pieces: %w", err)
		}
	}

	if db.config.ServerSideCopy {
		err = db.updateWithAncestorSegment(ctx, &segment)
		if err != nil {
			return Segment{}, err
		}
	}

	return segment, nil
}

func (db *DB) updateWithAncestorSegment(ctx context.Context, segment *Segment) (err error) {
	if !segment.PiecesInAncestorSegment() {
		return nil
	}

	var aliasPieces AliasPieces

	err = db.db.QueryRowContext(ctx, `
			SELECT
				root_piece_id,
				repaired_at,
				remote_alias_pieces
			FROM segments
			WHERE
				stream_id IN (SELECT ancestor_stream_id FROM segment_copies WHERE stream_id = $1)
				AND position = $2
			`, segment.StreamID, segment.Position.Encode()).Scan(
		&segment.RootPieceID,
		&segment.RepairedAt,
		&aliasPieces,
	)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return ErrSegmentNotFound.New("segment missing")
		}
		return Error.New("unable to query segment: %w", err)
	}

	segment.Pieces, err = db.aliasCache.ConvertAliasesToPieces(ctx, aliasPieces)
	if err != nil {
		return Error.New("unable to convert aliases to pieces: %w", err)
	}

	return nil
}

// BucketEmpty contains arguments necessary for checking if bucket is empty.
type BucketEmpty struct {
	ProjectID  uuid.UUID
	BucketName string
}

// BucketEmpty returns true if bucket does not contain objects (pending or committed).
// This method doesn't check bucket existence.
func (db *DB) BucketEmpty(ctx context.Context, opts BucketEmpty) (empty bool, err error) {
	defer mon.Task()(&ctx)(&err)

	switch {
	case opts.ProjectID.IsZero():
		return false, ErrInvalidRequest.New("ProjectID missing")
	case opts.BucketName == "":
		return false, ErrInvalidRequest.New("BucketName missing")
	}

	var value int
	err = db.db.QueryRowContext(ctx, `
		SELECT
			1
		FROM objects
		WHERE
			project_id   = $1 AND
			bucket_name  = $2
		LIMIT 1
	`, opts.ProjectID, []byte(opts.BucketName)).Scan(&value)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return true, nil
		}
		return false, Error.New("unable to query objects: %w", err)
	}

	return false, nil
}

// TestingAllCommittedObjects gets all objects from bucket.
// Use only for testing purposes.
func (db *DB) TestingAllCommittedObjects(ctx context.Context, projectID uuid.UUID, bucketName string) (objects []ObjectEntry, err error) {
	defer mon.Task()(&ctx)(&err)

	return db.testingAllObjectsByStatus(ctx, projectID, bucketName, Committed)
}

// TestingAllPendingObjects gets all objects from bucket.
// Use only for testing purposes.
func (db *DB) TestingAllPendingObjects(ctx context.Context, projectID uuid.UUID, bucketName string) (objects []ObjectEntry, err error) {
	defer mon.Task()(&ctx)(&err)

	return db.testingAllObjectsByStatus(ctx, projectID, bucketName, Pending)
}

func (db *DB) testingAllObjectsByStatus(ctx context.Context, projectID uuid.UUID, bucketName string, status ObjectStatus) (objects []ObjectEntry, err error) {
	defer mon.Task()(&ctx)(&err)

	err = db.IterateObjectsAllVersionsWithStatus(ctx,
		IterateObjectsWithStatus{
			ProjectID:             projectID,
			BucketName:            bucketName,
			Recursive:             true,
			Status:                status,
			IncludeCustomMetadata: true,
			IncludeSystemMetadata: true,
		}, func(ctx context.Context, it ObjectsIterator) error {
			entry := ObjectEntry{}
			for it.Next(ctx, &entry) {
				objects = append(objects, entry)
			}
			return nil
		},
	)
	if err != nil {
		return nil, Error.Wrap(err)
	}

	return objects, nil
}

// TestingAllObjectSegments gets all segments for given object.
// Use only for testing purposes.
func (db *DB) TestingAllObjectSegments(ctx context.Context, objectLocation ObjectLocation) (segments []Segment, err error) {
	defer mon.Task()(&ctx)(&err)

	object, err := db.GetObjectExactVersion(ctx, GetObjectExactVersion{
		ObjectLocation: objectLocation,
		Version:        DefaultVersion,
	})
	if err != nil {
		return nil, Error.Wrap(err)
	}

	response, err := db.ListSegments(ctx, ListSegments{
		StreamID: object.StreamID,
	})
	if err != nil {
		return nil, Error.Wrap(err)
	}

	return response.Segments, nil
}

// TestingAllObjects gets all objects.
// Use only for testing purposes.
func (db *DB) TestingAllObjects(ctx context.Context) (objects []Object, err error) {
	defer mon.Task()(&ctx)(&err)

	rawObjects, err := db.testingGetAllObjects(ctx)
	if err != nil {
		return nil, Error.Wrap(err)
	}

	for _, o := range rawObjects {
		objects = append(objects, Object(o))
	}

	return objects, nil
}

// TestingAllSegments gets all segments.
// Use only for testing purposes.
func (db *DB) TestingAllSegments(ctx context.Context) (segments []Segment, err error) {
	defer mon.Task()(&ctx)(&err)

	rawSegments, err := db.testingGetAllSegments(ctx)
	if err != nil {
		return nil, Error.Wrap(err)
	}

	for _, s := range rawSegments {
		segments = append(segments, Segment(s))
	}

	return segments, nil
}
