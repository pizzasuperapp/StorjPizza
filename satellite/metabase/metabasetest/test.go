// Copyright (C) 2020 Storj Labs, Inc.
// See LICENSE for copying information.

package metabasetest

import (
	"bytes"
	"context"
	"sort"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"
	"github.com/stretchr/testify/require"
	"github.com/zeebo/errs"

	"storj.io/common/storj"
	"storj.io/common/testcontext"
	"storj.io/storj/satellite/metabase"
)

// BeginObjectNextVersion is for testing metabase.BeginObjectNextVersion.
type BeginObjectNextVersion struct {
	Opts     metabase.BeginObjectNextVersion
	Version  metabase.Version
	ErrClass *errs.Class
	ErrText  string
}

// Check runs the test.
func (step BeginObjectNextVersion) Check(ctx *testcontext.Context, t testing.TB, db *metabase.DB) {
	got, err := db.BeginObjectNextVersion(ctx, step.Opts)
	checkError(t, err, step.ErrClass, step.ErrText)
	require.Equal(t, step.Version, got)
}

// BeginObjectExactVersion is for testing metabase.BeginObjectExactVersion.
type BeginObjectExactVersion struct {
	Opts     metabase.BeginObjectExactVersion
	Version  metabase.Version
	ErrClass *errs.Class
	ErrText  string
}

// Check runs the test.
func (step BeginObjectExactVersion) Check(ctx *testcontext.Context, t testing.TB, db *metabase.DB) {
	got, err := db.BeginObjectExactVersion(ctx, step.Opts)
	checkError(t, err, step.ErrClass, step.ErrText)
	if step.ErrClass == nil {
		require.Equal(t, step.Version, got.Version)
		require.WithinDuration(t, time.Now(), got.CreatedAt, 5*time.Second)
		require.Equal(t, step.Opts.ObjectStream, got.ObjectStream)
		require.Equal(t, step.Opts.ExpiresAt, got.ExpiresAt)

		gotDeadline := got.ZombieDeletionDeadline
		optsDeadline := step.Opts.ZombieDeletionDeadline
		if optsDeadline == nil {
			require.WithinDuration(t, time.Now().Add(24*time.Hour), *gotDeadline, 5*time.Second)
		} else {
			require.WithinDuration(t, *optsDeadline, *gotDeadline, 5*time.Second)
		}
		require.Equal(t, step.Opts.Encryption, got.Encryption)
	}
}

// CommitObject is for testing metabase.CommitObject.
type CommitObject struct {
	Opts     metabase.CommitObject
	ErrClass *errs.Class
	ErrText  string
}

// Check runs the test.
func (step CommitObject) Check(ctx *testcontext.Context, t testing.TB, db *metabase.DB) metabase.Object {
	object, err := db.CommitObject(ctx, step.Opts)
	checkError(t, err, step.ErrClass, step.ErrText)
	if err == nil {
		require.Equal(t, step.Opts.ObjectStream, object.ObjectStream)
	}
	return object
}

// CommitObjectWithSegments is for testing metabase.CommitObjectWithSegments.
type CommitObjectWithSegments struct {
	Opts     metabase.CommitObjectWithSegments
	Deleted  []metabase.DeletedSegmentInfo
	ErrClass *errs.Class
	ErrText  string
}

// Check runs the test.
func (step CommitObjectWithSegments) Check(ctx *testcontext.Context, t testing.TB, db *metabase.DB) metabase.Object {
	object, deleted, err := db.CommitObjectWithSegments(ctx, step.Opts)
	checkError(t, err, step.ErrClass, step.ErrText)
	if err == nil {
		require.Equal(t, step.Opts.ObjectStream, object.ObjectStream)
	}
	require.Equal(t, step.Deleted, deleted)
	return object
}

// BeginSegment is for testing metabase.BeginSegment.
type BeginSegment struct {
	Opts     metabase.BeginSegment
	ErrClass *errs.Class
	ErrText  string
}

// Check runs the test.
func (step BeginSegment) Check(ctx *testcontext.Context, t testing.TB, db *metabase.DB) {
	err := db.BeginSegment(ctx, step.Opts)
	checkError(t, err, step.ErrClass, step.ErrText)
}

// CommitSegment is for testing metabase.CommitSegment.
type CommitSegment struct {
	Opts     metabase.CommitSegment
	ErrClass *errs.Class
	ErrText  string
}

// Check runs the test.
func (step CommitSegment) Check(ctx *testcontext.Context, t testing.TB, db *metabase.DB) {
	err := db.CommitSegment(ctx, step.Opts)
	checkError(t, err, step.ErrClass, step.ErrText)
}

// CommitInlineSegment is for testing metabase.CommitInlineSegment.
type CommitInlineSegment struct {
	Opts     metabase.CommitInlineSegment
	ErrClass *errs.Class
	ErrText  string
}

// Check runs the test.
func (step CommitInlineSegment) Check(ctx *testcontext.Context, t testing.TB, db *metabase.DB) {
	err := db.CommitInlineSegment(ctx, step.Opts)
	checkError(t, err, step.ErrClass, step.ErrText)
}

// DeleteBucketObjects is for testing metabase.DeleteBucketObjects.
type DeleteBucketObjects struct {
	Opts     metabase.DeleteBucketObjects
	Deleted  int64
	ErrClass *errs.Class
	ErrText  string
}

// Check runs the test.
func (step DeleteBucketObjects) Check(ctx *testcontext.Context, t testing.TB, db *metabase.DB) {
	deleted, err := db.DeleteBucketObjects(ctx, step.Opts)
	require.Equal(t, step.Deleted, deleted)
	checkError(t, err, step.ErrClass, step.ErrText)
}

// UpdateObjectMetadata is for testing metabase.UpdateObjectMetadata.
type UpdateObjectMetadata struct {
	Opts     metabase.UpdateObjectMetadata
	ErrClass *errs.Class
	ErrText  string
}

// Check runs the test.
func (step UpdateObjectMetadata) Check(ctx *testcontext.Context, t testing.TB, db *metabase.DB) {
	err := db.UpdateObjectMetadata(ctx, step.Opts)
	checkError(t, err, step.ErrClass, step.ErrText)
}

// UpdateSegmentPieces is for testing metabase.UpdateSegmentPieces.
type UpdateSegmentPieces struct {
	Opts     metabase.UpdateSegmentPieces
	ErrClass *errs.Class
	ErrText  string
}

// Check runs the test.
func (step UpdateSegmentPieces) Check(ctx *testcontext.Context, t testing.TB, db *metabase.DB) {
	err := db.UpdateSegmentPieces(ctx, step.Opts)
	checkError(t, err, step.ErrClass, step.ErrText)
}

// GetObjectExactVersion is for testing metabase.GetObjectExactVersion.
type GetObjectExactVersion struct {
	Opts     metabase.GetObjectExactVersion
	Result   metabase.Object
	ErrClass *errs.Class
	ErrText  string
}

// Check runs the test.
func (step GetObjectExactVersion) Check(ctx *testcontext.Context, t testing.TB, db *metabase.DB) {
	result, err := db.GetObjectExactVersion(ctx, step.Opts)
	checkError(t, err, step.ErrClass, step.ErrText)

	diff := cmp.Diff(step.Result, result, DefaultTimeDiff())
	require.Zero(t, diff)
}

// GetSegmentByPosition is for testing metabase.GetSegmentByPosition.
type GetSegmentByPosition struct {
	Opts     metabase.GetSegmentByPosition
	Result   metabase.Segment
	ErrClass *errs.Class
	ErrText  string
}

// Check runs the test.
func (step GetSegmentByPosition) Check(ctx *testcontext.Context, t testing.TB, db *metabase.DB) {
	result, err := db.GetSegmentByPosition(ctx, step.Opts)
	checkError(t, err, step.ErrClass, step.ErrText)

	diff := cmp.Diff(step.Result, result, DefaultTimeDiff())
	require.Zero(t, diff)
}

// GetLatestObjectLastSegment is for testing metabase.GetLatestObjectLastSegment.
type GetLatestObjectLastSegment struct {
	Opts     metabase.GetLatestObjectLastSegment
	Result   metabase.Segment
	ErrClass *errs.Class
	ErrText  string
}

// Check runs the test.
func (step GetLatestObjectLastSegment) Check(ctx *testcontext.Context, t testing.TB, db *metabase.DB) {
	result, err := db.GetLatestObjectLastSegment(ctx, step.Opts)
	checkError(t, err, step.ErrClass, step.ErrText)

	diff := cmp.Diff(step.Result, result, DefaultTimeDiff())
	require.Zero(t, diff)
}

// BucketEmpty is for testing metabase.BucketEmpty.
type BucketEmpty struct {
	Opts     metabase.BucketEmpty
	Result   bool
	ErrClass *errs.Class
	ErrText  string
}

// Check runs the test.
func (step BucketEmpty) Check(ctx *testcontext.Context, t testing.TB, db *metabase.DB) {
	result, err := db.BucketEmpty(ctx, step.Opts)
	checkError(t, err, step.ErrClass, step.ErrText)

	require.Equal(t, step.Result, result)
}

// ListSegments is for testing metabase.ListSegments.
type ListSegments struct {
	Opts     metabase.ListSegments
	Result   metabase.ListSegmentsResult
	ErrClass *errs.Class
	ErrText  string
}

// Check runs the test.
func (step ListSegments) Check(ctx *testcontext.Context, t testing.TB, db *metabase.DB) {
	result, err := db.ListSegments(ctx, step.Opts)
	checkError(t, err, step.ErrClass, step.ErrText)

	if len(step.Result.Segments) == 0 && len(result.Segments) == 0 {
		return
	}

	diff := cmp.Diff(step.Result, result, DefaultTimeDiff())
	require.Zero(t, diff)
}

// ListStreamPositions is for testing metabase.ListStreamPositions.
type ListStreamPositions struct {
	Opts     metabase.ListStreamPositions
	Result   metabase.ListStreamPositionsResult
	ErrClass *errs.Class
	ErrText  string
}

// Check runs the test.
func (step ListStreamPositions) Check(ctx *testcontext.Context, t testing.TB, db *metabase.DB) {
	result, err := db.ListStreamPositions(ctx, step.Opts)
	checkError(t, err, step.ErrClass, step.ErrText)

	diff := cmp.Diff(step.Result, result, DefaultTimeDiff())
	require.Zero(t, diff)
}

// GetStreamPieceCountByNodeID is for testing metabase.GetStreamPieceCountByNodeID.
type GetStreamPieceCountByNodeID struct {
	Opts     metabase.GetStreamPieceCountByNodeID
	Result   map[storj.NodeID]int64
	ErrClass *errs.Class
	ErrText  string
}

// Check runs the test.
func (step GetStreamPieceCountByNodeID) Check(ctx *testcontext.Context, t testing.TB, db *metabase.DB) {
	result, err := db.GetStreamPieceCountByNodeID(ctx, step.Opts)
	checkError(t, err, step.ErrClass, step.ErrText)

	diff := cmp.Diff(step.Result, result)
	require.Zero(t, diff)
}

// IterateLoopSegments is for testing metabase.IterateLoopSegments.
type IterateLoopSegments struct {
	Opts     metabase.IterateLoopSegments
	Result   []metabase.LoopSegmentEntry
	ErrClass *errs.Class
	ErrText  string
}

// Check runs the test.
func (step IterateLoopSegments) Check(ctx *testcontext.Context, t testing.TB, db *metabase.DB) {
	result := make([]metabase.LoopSegmentEntry, 0, 10)
	err := db.IterateLoopSegments(ctx, step.Opts,
		func(ctx context.Context, iterator metabase.LoopSegmentsIterator) error {
			var entry metabase.LoopSegmentEntry
			for iterator.Next(ctx, &entry) {
				result = append(result, entry)
			}
			return nil
		})
	checkError(t, err, step.ErrClass, step.ErrText)

	if len(result) == 0 {
		result = nil
	}

	sort.Slice(step.Result, func(i, j int) bool {
		return bytes.Compare(step.Result[i].StreamID[:], step.Result[j].StreamID[:]) < 0
	})
	diff := cmp.Diff(step.Result, result, DefaultTimeDiff())
	require.Zero(t, diff)
}

// DeleteObjectExactVersion is for testing metabase.DeleteObjectExactVersion.
type DeleteObjectExactVersion struct {
	Opts     metabase.DeleteObjectExactVersion
	Result   metabase.DeleteObjectResult
	ErrClass *errs.Class
	ErrText  string
}

// Check runs the test.
func (step DeleteObjectExactVersion) Check(ctx *testcontext.Context, t testing.TB, db *metabase.DB) {
	result, err := db.DeleteObjectExactVersion(ctx, step.Opts)
	checkError(t, err, step.ErrClass, step.ErrText)

	sortObjects(result.Objects)
	sortObjects(step.Result.Objects)

	sortDeletedSegments(result.Segments)
	sortDeletedSegments(step.Result.Segments)

	diff := cmp.Diff(step.Result, result, DefaultTimeDiff(), cmpopts.EquateEmpty())
	require.Zero(t, diff)
}

// DeletePendingObject is for testing metabase.DeletePendingObject.
type DeletePendingObject struct {
	Opts     metabase.DeletePendingObject
	Result   metabase.DeleteObjectResult
	ErrClass *errs.Class
	ErrText  string
}

// Check runs the test.
func (step DeletePendingObject) Check(ctx *testcontext.Context, t testing.TB, db *metabase.DB) {
	result, err := db.DeletePendingObject(ctx, step.Opts)
	checkError(t, err, step.ErrClass, step.ErrText)

	sortObjects(result.Objects)
	sortObjects(step.Result.Objects)

	sortDeletedSegments(result.Segments)
	sortDeletedSegments(step.Result.Segments)

	diff := cmp.Diff(step.Result, result, DefaultTimeDiff())
	require.Zero(t, diff)
}

// DeleteObjectAnyStatusAllVersions is for testing metabase.DeleteObjectAnyStatusAllVersions.
type DeleteObjectAnyStatusAllVersions struct {
	Opts     metabase.DeleteObjectAnyStatusAllVersions
	Result   metabase.DeleteObjectResult
	ErrClass *errs.Class
	ErrText  string
}

// Check runs the test.
func (step DeleteObjectAnyStatusAllVersions) Check(ctx *testcontext.Context, t testing.TB, db *metabase.DB) {
	result, err := db.DeleteObjectAnyStatusAllVersions(ctx, step.Opts)
	checkError(t, err, step.ErrClass, step.ErrText)

	sortObjects(result.Objects)
	sortObjects(step.Result.Objects)

	sortDeletedSegments(result.Segments)
	sortDeletedSegments(step.Result.Segments)

	diff := cmp.Diff(step.Result, result, DefaultTimeDiff())
	require.Zero(t, diff)
}

// DeleteObjectsAllVersions is for testing metabase.DeleteObjectsAllVersions.
type DeleteObjectsAllVersions struct {
	Opts     metabase.DeleteObjectsAllVersions
	Result   metabase.DeleteObjectResult
	ErrClass *errs.Class
	ErrText  string
}

// Check runs the test.
func (step DeleteObjectsAllVersions) Check(ctx *testcontext.Context, t testing.TB, db *metabase.DB) {
	result, err := db.DeleteObjectsAllVersions(ctx, step.Opts)
	checkError(t, err, step.ErrClass, step.ErrText)

	sortObjects(result.Objects)
	sortObjects(step.Result.Objects)

	sortDeletedSegments(result.Segments)
	sortDeletedSegments(step.Result.Segments)

	diff := cmp.Diff(step.Result, result, DefaultTimeDiff())
	require.Zero(t, diff)
}

// DeleteExpiredObjects is for testing metabase.DeleteExpiredObjects.
type DeleteExpiredObjects struct {
	Opts metabase.DeleteExpiredObjects

	ErrClass *errs.Class
	ErrText  string
}

// Check runs the test.
func (step DeleteExpiredObjects) Check(ctx *testcontext.Context, t testing.TB, db *metabase.DB) {
	err := db.DeleteExpiredObjects(ctx, step.Opts)
	checkError(t, err, step.ErrClass, step.ErrText)
}

// DeleteZombieObjects is for testing metabase.DeleteZombieObjects.
type DeleteZombieObjects struct {
	Opts metabase.DeleteZombieObjects

	ErrClass *errs.Class
	ErrText  string
}

// Check runs the test.
func (step DeleteZombieObjects) Check(ctx *testcontext.Context, t testing.TB, db *metabase.DB) {
	err := db.DeleteZombieObjects(ctx, step.Opts)
	checkError(t, err, step.ErrClass, step.ErrText)
}

// IterateCollector is for testing metabase.IterateCollector.
type IterateCollector []metabase.ObjectEntry

// Add adds object entries from iterator to the collection.
func (coll *IterateCollector) Add(ctx context.Context, it metabase.ObjectsIterator) error {
	var item metabase.ObjectEntry

	for it.Next(ctx, &item) {
		*coll = append(*coll, item)
	}
	return nil
}

// LoopIterateCollector is for testing metabase.LoopIterateCollector.
type LoopIterateCollector []metabase.LoopObjectEntry

// Add adds object entries from iterator to the collection.
func (coll *LoopIterateCollector) Add(ctx context.Context, it metabase.LoopObjectsIterator) error {
	var item metabase.LoopObjectEntry

	for it.Next(ctx, &item) {
		*coll = append(*coll, item)
	}
	return nil
}

// IteratePendingObjectsByKey is for testing metabase.IteratePendingObjectsByKey.
type IteratePendingObjectsByKey struct {
	Opts metabase.IteratePendingObjectsByKey

	Result   []metabase.ObjectEntry
	ErrClass *errs.Class
	ErrText  string
}

// Check runs the test.
func (step IteratePendingObjectsByKey) Check(ctx *testcontext.Context, t *testing.T, db *metabase.DB) {
	var collector IterateCollector

	err := db.IteratePendingObjectsByKey(ctx, step.Opts, collector.Add)
	checkError(t, err, step.ErrClass, step.ErrText)

	result := []metabase.ObjectEntry(collector)

	diff := cmp.Diff(step.Result, result, DefaultTimeDiff())
	require.Zero(t, diff)
}

// IterateObjectsWithStatus is for testing metabase.IterateObjectsWithStatus.
type IterateObjectsWithStatus struct {
	Opts metabase.IterateObjectsWithStatus

	Result   []metabase.ObjectEntry
	ErrClass *errs.Class
	ErrText  string
}

// Check runs the test.
func (step IterateObjectsWithStatus) Check(ctx *testcontext.Context, t testing.TB, db *metabase.DB) {
	var result IterateCollector

	err := db.IterateObjectsAllVersionsWithStatus(ctx, step.Opts, result.Add)
	checkError(t, err, step.ErrClass, step.ErrText)

	diff := cmp.Diff(step.Result, []metabase.ObjectEntry(result), DefaultTimeDiff())
	require.Zero(t, diff)
}

// IterateLoopObjects is for testing metabase.IterateLoopObjects.
type IterateLoopObjects struct {
	Opts metabase.IterateLoopObjects

	Result   []metabase.LoopObjectEntry
	ErrClass *errs.Class
	ErrText  string
}

// Check runs the test.
func (step IterateLoopObjects) Check(ctx *testcontext.Context, t testing.TB, db *metabase.DB) {
	var result LoopIterateCollector

	err := db.IterateLoopObjects(ctx, step.Opts, result.Add)
	checkError(t, err, step.ErrClass, step.ErrText)

	diff := cmp.Diff(step.Result, []metabase.LoopObjectEntry(result), DefaultTimeDiff())
	require.Zero(t, diff)
}

// EnsureNodeAliases is for testing metabase.EnsureNodeAliases.
type EnsureNodeAliases struct {
	Opts metabase.EnsureNodeAliases

	ErrClass *errs.Class
	ErrText  string
}

// Check runs the test.
func (step EnsureNodeAliases) Check(ctx *testcontext.Context, t testing.TB, db *metabase.DB) {
	err := db.EnsureNodeAliases(ctx, step.Opts)
	checkError(t, err, step.ErrClass, step.ErrText)
}

// ListNodeAliases is for testing metabase.ListNodeAliases.
type ListNodeAliases struct {
	ErrClass *errs.Class
	ErrText  string
}

// Check runs the test.
func (step ListNodeAliases) Check(ctx *testcontext.Context, t testing.TB, db *metabase.DB) []metabase.NodeAliasEntry {
	result, err := db.ListNodeAliases(ctx)
	checkError(t, err, step.ErrClass, step.ErrText)
	return result
}

// GetTableStats is for testing metabase.GetTableStats.
type GetTableStats struct {
	Opts     metabase.GetTableStats
	Result   metabase.TableStats
	ErrClass *errs.Class
	ErrText  string
}

// Check runs the test.
func (step GetTableStats) Check(ctx *testcontext.Context, t testing.TB, db *metabase.DB) metabase.TableStats {
	result, err := db.GetTableStats(ctx, step.Opts)
	checkError(t, err, step.ErrClass, step.ErrText)

	diff := cmp.Diff(step.Result, result)
	require.Zero(t, diff)

	return result
}

// BeginMoveObject is for testing metabase.BeginMoveObject.
type BeginMoveObject struct {
	Opts     metabase.BeginMoveObject
	Result   metabase.BeginMoveObjectResult
	ErrClass *errs.Class
	ErrText  string
}

// Check runs the test.
func (step BeginMoveObject) Check(ctx *testcontext.Context, t testing.TB, db *metabase.DB) {
	result, err := db.BeginMoveObject(ctx, step.Opts)
	checkError(t, err, step.ErrClass, step.ErrText)

	diff := cmp.Diff(step.Result, result)
	require.Zero(t, diff)
}

// FinishMoveObject is for testing metabase.FinishMoveObject.
type FinishMoveObject struct {
	Opts     metabase.FinishMoveObject
	ErrClass *errs.Class
	ErrText  string
}

// Check runs the test.
func (step FinishMoveObject) Check(ctx *testcontext.Context, t testing.TB, db *metabase.DB) {
	err := db.FinishMoveObject(ctx, step.Opts)
	checkError(t, err, step.ErrClass, step.ErrText)
}

// BeginCopyObject is for testing metabase.BeginCopyObject.
type BeginCopyObject struct {
	Opts     metabase.BeginCopyObject
	Result   metabase.BeginCopyObjectResult
	ErrClass *errs.Class
	ErrText  string
}

// Check runs the test.
func (step BeginCopyObject) Check(ctx *testcontext.Context, t testing.TB, db *metabase.DB) {
	result, err := db.BeginCopyObject(ctx, step.Opts)
	checkError(t, err, step.ErrClass, step.ErrText)

	diff := cmp.Diff(step.Result, result)
	require.Zero(t, diff)
}

// FinishCopyObject is for testing metabase.FinishCopyObject.
type FinishCopyObject struct {
	Opts     metabase.FinishCopyObject
	Result   metabase.Object
	ErrClass *errs.Class
	ErrText  string
}

// Check runs the test.
func (step FinishCopyObject) Check(ctx *testcontext.Context, t testing.TB, db *metabase.DB) metabase.Object {
	result, err := db.FinishCopyObject(ctx, step.Opts)
	checkError(t, err, step.ErrClass, step.ErrText)

	diff := cmp.Diff(step.Result, result, DefaultTimeDiff())
	require.Zero(t, diff)
	return result
}
