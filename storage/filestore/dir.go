// Copyright (C) 2019 Storj Labs, Inc.
// See LICENSE for copying information.

package filestore

import (
	"bytes"
	"context"
	"encoding/base32"
	"errors"
	"io"
	"io/ioutil"
	"math"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/zeebo/errs"
	"go.uber.org/zap"

	"storj.io/common/storj"
	"storj.io/storj/storage"
)

const (
	blobPermission = 0600
	dirPermission  = 0700

	v0PieceFileSuffix      = ""
	v1PieceFileSuffix      = ".sj1"
	unknownPieceFileSuffix = "/..error_unknown_format../"
	verificationFileName   = "storage-dir-verification"
)

var pathEncoding = base32.NewEncoding("abcdefghijklmnopqrstuvwxyz234567").WithPadding(base32.NoPadding)

// Dir represents single folder for storing blobs.
type Dir struct {
	log  *zap.Logger
	path string

	mu          sync.Mutex
	deleteQueue []string
	trashnow    func() time.Time // the function used by trash to determine "now"
}

// OpenDir opens existing folder for storing blobs.
func OpenDir(log *zap.Logger, path string) (*Dir, error) {
	dir := &Dir{
		log:      log,
		path:     path,
		trashnow: time.Now,
	}

	stat := func(path string) error {
		_, err := os.Stat(path)
		return err
	}

	return dir, errs.Combine(
		stat(dir.blobsdir()),
		stat(dir.tempdir()),
		stat(dir.garbagedir()),
		stat(dir.trashdir()),
	)
}

// NewDir returns folder for storing blobs.
func NewDir(log *zap.Logger, path string) (*Dir, error) {
	dir := &Dir{
		log:      log,
		path:     path,
		trashnow: time.Now,
	}

	return dir, errs.Combine(
		os.MkdirAll(dir.blobsdir(), dirPermission),
		os.MkdirAll(dir.tempdir(), dirPermission),
		os.MkdirAll(dir.garbagedir(), dirPermission),
		os.MkdirAll(dir.trashdir(), dirPermission),
	)
}

// Path returns the directory path.
func (dir *Dir) Path() string { return dir.path }

// blobsdir is the sub-directory containing the blobs.
func (dir *Dir) blobsdir() string { return filepath.Join(dir.path, "blobs") }

// tempdir is used for temp files prior to being moved into blobsdir.
func (dir *Dir) tempdir() string { return filepath.Join(dir.path, "temp") }

// garbagedir contains files that failed to delete but should be deleted.
func (dir *Dir) garbagedir() string { return filepath.Join(dir.path, "garbage") }

// trashdir contains files staged for deletion for a period of time.
func (dir *Dir) trashdir() string { return filepath.Join(dir.path, "trash") }

// CreateVerificationFile creates a file to be used for storage directory verification.
func (dir *Dir) CreateVerificationFile(ctx context.Context, id storj.NodeID) error {
	f, err := os.Create(filepath.Join(dir.path, verificationFileName))
	if err != nil {
		return err
	}
	defer func() {
		err = errs.Combine(err, f.Close())
	}()
	_, err = f.Write(id.Bytes())
	return err
}

// Verify verifies that the storage directory is correct by checking for the existence and validity
// of the verification file.
func (dir *Dir) Verify(ctx context.Context, id storj.NodeID) error {
	content, err := ioutil.ReadFile(filepath.Join(dir.path, verificationFileName))
	if err != nil {
		return err
	}

	if !bytes.Equal(content, id.Bytes()) {
		verifyID, err := storj.NodeIDFromBytes(content)
		if err != nil {
			return errs.New("content of file is not a valid node ID: %x", content)
		}
		return errs.New("node ID in file (%s) does not match running node's ID (%s)", verifyID, id.String())
	}
	return nil
}

// CreateTemporaryFile creates a preallocated temporary file in the temp directory
// prealloc preallocates file to make writing faster.
func (dir *Dir) CreateTemporaryFile(ctx context.Context, prealloc int64) (_ *os.File, err error) {
	const preallocLimit = 5 << 20 // 5 MB
	if prealloc > preallocLimit {
		prealloc = preallocLimit
	}

	file, err := ioutil.TempFile(dir.tempdir(), "blob-*.partial")
	if err != nil {
		return nil, err
	}

	if prealloc >= 0 {
		if err := file.Truncate(prealloc); err != nil {
			return nil, errs.Combine(err, file.Close())
		}
	}
	return file, nil
}

// DeleteTemporary deletes a temporary file.
func (dir *Dir) DeleteTemporary(ctx context.Context, file *os.File) (err error) {
	defer mon.Task()(&ctx)(&err)
	closeErr := file.Close()
	return errs.Combine(closeErr, os.Remove(file.Name()))
}

// blobToBasePath converts a blob reference to a filepath in permanent storage. This may not be the
// entire path; blobPathForFormatVersion() must also be used. This is a separate call because this
// part of the filepath is constant, and blobPathForFormatVersion may need to be called multiple
// times with different storage.FormatVersion values.
func (dir *Dir) blobToBasePath(ref storage.BlobRef) (string, error) {
	return dir.refToDirPath(ref, dir.blobsdir())
}

// refToDirPath converts a blob reference to a filepath in the specified sub-directory.
func (dir *Dir) refToDirPath(ref storage.BlobRef, subDir string) (string, error) {
	if !ref.IsValid() {
		return "", storage.ErrInvalidBlobRef.New("")
	}

	namespace := pathEncoding.EncodeToString(ref.Namespace)
	key := pathEncoding.EncodeToString(ref.Key)
	if len(key) < 3 {
		// ensure we always have enough characters to split [:2] and [2:]
		key = "11" + key
	}
	return filepath.Join(subDir, namespace, key[:2], key[2:]), nil
}

// fileConfirmedInTrash returns true if it is able to confirm the file is in
// the trash. On errors, or if the file is not in the trash, it returns false.
func (dir *Dir) fileConfirmedInTrash(ctx context.Context, ref storage.BlobRef, formatVer storage.FormatVersion) bool {
	trashBasePath, err := dir.refToDirPath(ref, dir.trashdir())
	if err != nil {
		return false
	}
	trashVerPath := blobPathForFormatVersion(trashBasePath, formatVer)
	_, err = os.Stat(trashVerPath)
	return err == nil
}

// blobPathForFormatVersion adjusts a bare blob path (as might have been generated by a call to
// blobToBasePath()) to what it should be for the given storage format version.
func blobPathForFormatVersion(path string, formatVersion storage.FormatVersion) string {
	switch formatVersion {
	case FormatV0:
		return path + v0PieceFileSuffix
	case FormatV1:
		return path + v1PieceFileSuffix
	}
	return path + unknownPieceFileSuffix
}

// blobToGarbagePath converts a blob reference to a filepath in transient
// storage.  The files in garbage are deleted on an interval (in case the
// initial deletion didn't work for some reason).
func (dir *Dir) blobToGarbagePath(ref storage.BlobRef) string {
	var name []byte
	name = append(name, ref.Namespace...)
	name = append(name, ref.Key...)
	return filepath.Join(dir.garbagedir(), pathEncoding.EncodeToString(name))
}

// Commit commits the temporary file to permanent storage.
func (dir *Dir) Commit(ctx context.Context, file *os.File, ref storage.BlobRef, formatVersion storage.FormatVersion) (err error) {
	defer mon.Task()(&ctx)(&err)
	position, seekErr := file.Seek(0, io.SeekCurrent)
	truncErr := file.Truncate(position)
	syncErr := file.Sync()
	chmodErr := os.Chmod(file.Name(), blobPermission)
	closeErr := file.Close()

	if seekErr != nil || truncErr != nil || syncErr != nil || chmodErr != nil || closeErr != nil {
		removeErr := os.Remove(file.Name())
		return errs.Combine(seekErr, truncErr, syncErr, chmodErr, closeErr, removeErr)
	}

	path, err := dir.blobToBasePath(ref)
	if err != nil {
		removeErr := os.Remove(file.Name())
		return errs.Combine(err, removeErr)
	}
	path = blobPathForFormatVersion(path, formatVersion)

	mkdirErr := os.MkdirAll(filepath.Dir(path), dirPermission)
	if os.IsExist(mkdirErr) {
		mkdirErr = nil
	}

	if mkdirErr != nil {
		removeErr := os.Remove(file.Name())
		return errs.Combine(mkdirErr, removeErr)
	}

	renameErr := rename(file.Name(), path)
	if renameErr != nil {
		removeErr := os.Remove(file.Name())
		return errs.Combine(renameErr, removeErr)
	}

	return nil
}

// Open opens the file with the specified ref. It may need to check in more than one location in
// order to find the blob, if it was stored with an older version of the storage node software.
// In cases where the storage format version of a blob is already known, OpenWithStorageFormat()
// will generally be a better choice.
func (dir *Dir) Open(ctx context.Context, ref storage.BlobRef) (_ *os.File, _ storage.FormatVersion, err error) {
	defer mon.Task()(&ctx)(&err)
	path, err := dir.blobToBasePath(ref)
	if err != nil {
		return nil, FormatV0, err
	}
	for formatVer := MaxFormatVersionSupported; formatVer >= MinFormatVersionSupported; formatVer-- {
		vPath := blobPathForFormatVersion(path, formatVer)
		file, err := openFileReadOnly(vPath, blobPermission)
		if err == nil {
			return file, formatVer, nil
		}
		if os.IsNotExist(err) {
			// Check and monitor if the file is in the trash
			if dir.fileConfirmedInTrash(ctx, ref, formatVer) {
				monFileInTrash(ref.Namespace).Mark(1)
			}
		} else {
			return nil, FormatV0, Error.New("unable to open %q: %v", vPath, err)
		}
	}
	return nil, FormatV0, os.ErrNotExist
}

// OpenWithStorageFormat opens an already-located blob file with a known storage format version,
// which avoids the potential need to search through multiple storage formats to find the blob.
func (dir *Dir) OpenWithStorageFormat(ctx context.Context, ref storage.BlobRef, formatVer storage.FormatVersion) (_ *os.File, err error) {
	defer mon.Task()(&ctx)(&err)
	path, err := dir.blobToBasePath(ref)
	if err != nil {
		return nil, err
	}
	vPath := blobPathForFormatVersion(path, formatVer)
	file, err := openFileReadOnly(vPath, blobPermission)
	if err == nil {
		return file, nil
	}
	if os.IsNotExist(err) {
		// Check and monitor if the file is in the trash
		if dir.fileConfirmedInTrash(ctx, ref, formatVer) {
			monFileInTrash(ref.Namespace).Mark(1)
		}
		return nil, err
	}
	return nil, Error.New("unable to open %q: %v", vPath, err)
}

// Stat looks up disk metadata on the blob file. It may need to check in more than one location
// in order to find the blob, if it was stored with an older version of the storage node software.
// In cases where the storage format version of a blob is already known, StatWithStorageFormat()
// will generally be a better choice.
func (dir *Dir) Stat(ctx context.Context, ref storage.BlobRef) (_ storage.BlobInfo, err error) {
	defer mon.Task()(&ctx)(&err)
	path, err := dir.blobToBasePath(ref)
	if err != nil {
		return nil, err
	}
	for formatVer := MaxFormatVersionSupported; formatVer >= MinFormatVersionSupported; formatVer-- {
		vPath := blobPathForFormatVersion(path, formatVer)
		stat, err := os.Stat(vPath)
		if err == nil {
			return newBlobInfo(ref, vPath, stat, formatVer), nil
		}
		if !os.IsNotExist(err) {
			return nil, Error.New("unable to stat %q: %v", vPath, err)
		}
	}
	return nil, os.ErrNotExist
}

// StatWithStorageFormat looks up disk metadata on the blob file with the given storage format
// version. This avoids the need for checking for the file in multiple different storage format
// types.
func (dir *Dir) StatWithStorageFormat(ctx context.Context, ref storage.BlobRef, formatVer storage.FormatVersion) (_ storage.BlobInfo, err error) {
	defer mon.Task()(&ctx)(&err)
	path, err := dir.blobToBasePath(ref)
	if err != nil {
		return nil, err
	}
	vPath := blobPathForFormatVersion(path, formatVer)
	stat, err := os.Stat(vPath)
	if err == nil {
		return newBlobInfo(ref, vPath, stat, formatVer), nil
	}
	if os.IsNotExist(err) {
		return nil, err
	}
	return nil, Error.New("unable to stat %q: %v", vPath, err)
}

// Trash moves the piece specified by ref to the trashdir for every format version.
func (dir *Dir) Trash(ctx context.Context, ref storage.BlobRef) (err error) {
	defer mon.Task()(&ctx)(&err)
	return dir.iterateStorageFormatVersions(ctx, ref, dir.TrashWithStorageFormat)
}

// TrashWithStorageFormat moves the piece specified by ref to the trashdir for the specified format version.
func (dir *Dir) TrashWithStorageFormat(ctx context.Context, ref storage.BlobRef, formatVer storage.FormatVersion) (err error) {
	// Ensure trashdir exists so that we know any os.IsNotExist errors below
	// are not from a missing trash dir
	_, err = os.Stat(dir.trashdir())
	if err != nil {
		return err
	}

	blobsBasePath, err := dir.blobToBasePath(ref)
	if err != nil {
		return err
	}

	blobsVerPath := blobPathForFormatVersion(blobsBasePath, formatVer)

	trashBasePath, err := dir.refToDirPath(ref, dir.trashdir())
	if err != nil {
		return err
	}

	trashVerPath := blobPathForFormatVersion(trashBasePath, formatVer)

	// ensure the dirs exist for trash path
	err = os.MkdirAll(filepath.Dir(trashVerPath), dirPermission)
	if err != nil && !os.IsExist(err) {
		return err
	}

	// Change mtime to now. This allows us to check the mtime to know how long
	// the file has been in the trash. If the file is restored this may make it
	// take longer to be trashed again, but the simplicity is worth the
	// trade-off.
	//
	// We change the mtime prior to moving the file so that if this call fails
	// the file will not be in the trash with an unmodified mtime, which could
	// result in its permanent deletion too soon.
	now := dir.trashnow()
	err = os.Chtimes(blobsVerPath, now, now)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}

	// move to trash
	err = rename(blobsVerPath, trashVerPath)
	if os.IsNotExist(err) {
		// no piece at that path; either it has a different storage format
		// version or there was a concurrent call. (This function is expected
		// by callers to return a nil error in the case of concurrent calls.)
		return nil
	}
	return err
}

// ReplaceTrashnow is a helper for tests to replace the trashnow function used
// when moving files to the trash.
func (dir *Dir) ReplaceTrashnow(trashnow func() time.Time) {
	dir.trashnow = trashnow
}

// RestoreTrash moves every piece in the trash folder back into blobsdir.
func (dir *Dir) RestoreTrash(ctx context.Context, namespace []byte) (keysRestored [][]byte, err error) {
	err = dir.walkNamespaceInPath(ctx, namespace, dir.trashdir(), func(info storage.BlobInfo) error {
		blobsBasePath, err := dir.blobToBasePath(info.BlobRef())
		if err != nil {
			return err
		}

		blobsVerPath := blobPathForFormatVersion(blobsBasePath, info.StorageFormatVersion())

		trashBasePath, err := dir.refToDirPath(info.BlobRef(), dir.trashdir())
		if err != nil {
			return err
		}

		trashVerPath := blobPathForFormatVersion(trashBasePath, info.StorageFormatVersion())

		// ensure the dirs exist for blobs path
		err = os.MkdirAll(filepath.Dir(blobsVerPath), dirPermission)
		if err != nil && !os.IsExist(err) {
			return err
		}

		// move back to blobsdir
		err = rename(trashVerPath, blobsVerPath)
		if os.IsNotExist(err) {
			// no piece at that path; either it has a different storage format
			// version or there was a concurrent call. (This function is expected
			// by callers to return a nil error in the case of concurrent calls.)
			return nil
		}
		if err != nil {
			return err
		}

		keysRestored = append(keysRestored, info.BlobRef().Key)
		return nil
	})
	return keysRestored, err
}

// EmptyTrash walks the trash files for the given namespace and deletes any
// file whose mtime is older than trashedBefore. The mtime is modified when
// Trash is called.
func (dir *Dir) EmptyTrash(ctx context.Context, namespace []byte, trashedBefore time.Time) (bytesEmptied int64, deletedKeys [][]byte, err error) {
	defer mon.Task()(&ctx)(&err)
	err = dir.walkNamespaceInPath(ctx, namespace, dir.trashdir(), func(blobInfo storage.BlobInfo) error {
		fileInfo, err := blobInfo.Stat(ctx)
		if err != nil {
			return err
		}

		mtime := fileInfo.ModTime()
		if mtime.Before(trashedBefore) {
			err = dir.deleteWithStorageFormatInPath(ctx, dir.trashdir(), blobInfo.BlobRef(), blobInfo.StorageFormatVersion())
			if err != nil {
				return err
			}
			deletedKeys = append(deletedKeys, blobInfo.BlobRef().Key)
			bytesEmptied += fileInfo.Size()
		}
		return nil
	})
	if err != nil {
		return 0, nil, err
	}
	return bytesEmptied, deletedKeys, nil
}

// iterateStorageFormatVersions executes f for all storage format versions,
// starting with the oldest format version. It is more likely, in the general
// case, that we will find the piece with the newest format version instead,
// but if we iterate backward here then we run the risk of a race condition:
// the piece might have existed with _SomeOldVer before the call, and could
// then have been updated atomically with _MaxVer concurrently while we were
// iterating. If we iterate _forwards_, this race should not occur because it
// is assumed that pieces are never rewritten with an _older_ storage format
// version.
//
// f will be executed for every storage formate version regardless of the
// result, and will aggregate errors into a single returned error.
func (dir *Dir) iterateStorageFormatVersions(ctx context.Context, ref storage.BlobRef, f func(ctx context.Context, ref storage.BlobRef, i storage.FormatVersion) error) (err error) {
	defer mon.Task()(&ctx)(&err)
	var combinedErrors errs.Group
	for i := MinFormatVersionSupported; i <= MaxFormatVersionSupported; i++ {
		combinedErrors.Add(f(ctx, ref, i))
	}
	return combinedErrors.Err()
}

// Delete deletes blobs with the specified ref (in all supported storage formats).
//
// It doesn't return an error if the blob is not found for any reason or it
// cannot be deleted at this moment and it's delayed.
func (dir *Dir) Delete(ctx context.Context, ref storage.BlobRef) (err error) {
	defer mon.Task()(&ctx)(&err)
	return dir.iterateStorageFormatVersions(ctx, ref, dir.DeleteWithStorageFormat)
}

// DeleteWithStorageFormat deletes the blob with the specified ref for one
// specific format version. The method tries the following strategies, in order
// of preference until one succeeds:
//
// * moves the blob to garbage dir.
// * directly deletes the blob.
// * push the blobs to queue for retrying later.
//
// It doesn't return an error if the piece isn't found for any reason.
func (dir *Dir) DeleteWithStorageFormat(ctx context.Context, ref storage.BlobRef, formatVer storage.FormatVersion) (err error) {
	defer mon.Task()(&ctx)(&err)
	return dir.deleteWithStorageFormatInPath(ctx, dir.blobsdir(), ref, formatVer)
}

// DeleteNamespace deletes blobs folder for a specific namespace.
func (dir *Dir) DeleteNamespace(ctx context.Context, ref []byte) (err error) {
	defer mon.Task()(&ctx)(&err)
	return dir.deleteNamespace(ctx, dir.blobsdir(), ref)
}

func (dir *Dir) deleteWithStorageFormatInPath(ctx context.Context, path string, ref storage.BlobRef, formatVer storage.FormatVersion) (err error) {
	defer mon.Task()(&ctx)(&err)

	// Ensure garbage dir exists so that we know any os.IsNotExist errors below
	// are not from a missing garbage dir
	_, err = os.Stat(dir.garbagedir())
	if err != nil {
		return err
	}

	pathBase, err := dir.refToDirPath(ref, path)
	if err != nil {
		return err
	}

	garbagePath := dir.blobToGarbagePath(ref)
	verPath := blobPathForFormatVersion(pathBase, formatVer)

	// move to garbage folder, this is allowed for some OS-es
	moveErr := rename(verPath, garbagePath)
	if os.IsNotExist(moveErr) {
		// no piece at that path; either it has a different storage format
		// version or there was a concurrent delete. (this function is expected
		// by callers to return a nil error in the case of concurrent deletes.)
		return nil
	}
	if moveErr != nil {
		// piece could not be moved into the garbage dir; we'll try removing it
		// directly
		garbagePath = verPath
	}

	// try removing the file
	err = os.Remove(garbagePath)

	// ignore concurrent deletes
	if os.IsNotExist(err) {
		// something is happening at the same time as this; possibly a
		// concurrent delete, or possibly a rewrite of the blob.
		return nil
	}

	// the remove may have failed because of an open file handle. put it in a
	// queue to be retried later.
	if err != nil {
		dir.mu.Lock()
		dir.deleteQueue = append(dir.deleteQueue, garbagePath)
		dir.mu.Unlock()
		mon.Event("delete_deferred_to_queue")
	}

	// ignore is-busy errors, they are still in the queue but no need to notify
	if isBusy(err) {
		err = nil
	}
	return err
}

// deleteNamespace deletes folder with everything inside.
func (dir *Dir) deleteNamespace(ctx context.Context, path string, ref []byte) (err error) {
	defer mon.Task()(&ctx)(&err)

	namespace := pathEncoding.EncodeToString(ref)
	folderPath := filepath.Join(path, namespace)

	err = os.RemoveAll(folderPath)
	return err
}

// GarbageCollect collects files that are pending deletion.
func (dir *Dir) GarbageCollect(ctx context.Context) (err error) {
	defer mon.Task()(&ctx)(&err)
	offset := int(math.MaxInt32)
	// limited deletion loop to avoid blocking `Delete` for too long
	for offset >= 0 {
		dir.mu.Lock()
		limit := 100
		if offset >= len(dir.deleteQueue) {
			offset = len(dir.deleteQueue) - 1
		}
		for offset >= 0 && limit > 0 {
			path := dir.deleteQueue[offset]
			err := os.Remove(path)
			if os.IsNotExist(err) {
				err = nil
			}
			if err == nil {
				dir.deleteQueue = append(dir.deleteQueue[:offset], dir.deleteQueue[offset+1:]...)
			}

			offset--
			limit--
		}
		dir.mu.Unlock()
	}

	// remove anything left in the garbagedir
	_ = removeAllContent(ctx, dir.garbagedir())
	return nil
}

const nameBatchSize = 1024

// ListNamespaces finds all known namespace IDs in use in local storage. They are not
// guaranteed to contain any blobs.
func (dir *Dir) ListNamespaces(ctx context.Context) (ids [][]byte, err error) {
	defer mon.Task()(&ctx)(&err)
	return dir.listNamespacesInPath(ctx, dir.blobsdir())
}

func (dir *Dir) listNamespacesInPath(ctx context.Context, path string) (ids [][]byte, err error) {
	defer mon.Task()(&ctx)(&err)
	openDir, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer func() { err = errs.Combine(err, openDir.Close()) }()
	for {
		dirNames, err := openDir.Readdirnames(nameBatchSize)
		if err != nil {
			if errors.Is(err, io.EOF) || os.IsNotExist(err) {
				return ids, nil
			}
			return ids, err
		}
		if len(dirNames) == 0 {
			return ids, nil
		}
		for _, name := range dirNames {
			namespace, err := pathEncoding.DecodeString(name)
			if err != nil {
				// just an invalid directory entry, and not a namespace. probably
				// don't need to pass on this error
				continue
			}
			ids = append(ids, namespace)
		}
	}
}

// WalkNamespace executes walkFunc for each locally stored blob, stored with storage format V1 or
// greater, in the given namespace. If walkFunc returns a non-nil error, WalkNamespace will stop
// iterating and return the error immediately. The ctx parameter is intended specifically to allow
// canceling iteration early.
func (dir *Dir) WalkNamespace(ctx context.Context, namespace []byte, walkFunc func(storage.BlobInfo) error) (err error) {
	defer mon.Task()(&ctx)(&err)
	return dir.walkNamespaceInPath(ctx, namespace, dir.blobsdir(), walkFunc)
}

func (dir *Dir) walkNamespaceInPath(ctx context.Context, namespace []byte, path string, walkFunc func(storage.BlobInfo) error) (err error) {
	defer mon.Task()(&ctx)(&err)
	namespaceDir := pathEncoding.EncodeToString(namespace)
	nsDir := filepath.Join(path, namespaceDir)
	openDir, err := os.Open(nsDir)
	if err != nil {
		if os.IsNotExist(err) {
			// job accomplished: there are no blobs in this namespace!
			return nil
		}
		return err
	}
	defer func() { err = errs.Combine(err, openDir.Close()) }()
	for {
		// check for context done both before and after our readdir() call
		if err := ctx.Err(); err != nil {
			return err
		}
		subdirNames, err := openDir.Readdirnames(nameBatchSize)
		if err != nil {
			if errors.Is(err, io.EOF) || os.IsNotExist(err) {
				return nil
			}
			return err
		}
		if len(subdirNames) == 0 {
			return nil
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		for _, keyPrefix := range subdirNames {
			if len(keyPrefix) != 2 {
				// just an invalid subdir; could be garbage of many kinds. probably
				// don't need to pass on this error
				continue
			}
			err := walkNamespaceWithPrefix(ctx, dir.log, namespace, nsDir, keyPrefix, walkFunc)
			if err != nil {
				return err
			}
		}
	}
}

func decodeBlobInfo(namespace []byte, keyPrefix, keyDir string, keyInfo os.FileInfo) (info storage.BlobInfo, ok bool) {
	blobFileName := keyInfo.Name()
	encodedKey := keyPrefix + blobFileName
	formatVer := FormatV0
	if strings.HasSuffix(blobFileName, v1PieceFileSuffix) {
		formatVer = FormatV1
		encodedKey = encodedKey[0 : len(encodedKey)-len(v1PieceFileSuffix)]
	}
	key, err := pathEncoding.DecodeString(encodedKey)
	if err != nil {
		return nil, false
	}
	ref := storage.BlobRef{
		Namespace: namespace,
		Key:       key,
	}
	return newBlobInfo(ref, filepath.Join(keyDir, blobFileName), keyInfo, formatVer), true
}

func walkNamespaceWithPrefix(ctx context.Context, log *zap.Logger, namespace []byte, nsDir, keyPrefix string, walkFunc func(storage.BlobInfo) error) (err error) {
	keyDir := filepath.Join(nsDir, keyPrefix)
	openDir, err := os.Open(keyDir)
	if err != nil {
		return err
	}
	defer func() { err = errs.Combine(err, openDir.Close()) }()
	for {
		// check for context done both before and after our readdir() call
		if err := ctx.Err(); err != nil {
			return err
		}
		names, err := openDir.Readdirnames(nameBatchSize)
		if err != nil && !errors.Is(err, io.EOF) {
			return err
		}
		if os.IsNotExist(err) || len(names) == 0 {
			return nil
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		for _, name := range names {
			info, err := os.Lstat(keyDir + "/" + name)
			if err != nil {
				if os.IsNotExist(err) {
					continue
				}

				// convert to lowercase the perr.Op because Go reports inconsistently
				// "lstat" in Linux and "Lstat" in Windows
				var perr *os.PathError
				if errors.As(err, &perr) && strings.ToLower(perr.Op) == "lstat" {
					log.Error("Unable to read the disk, please verify the disk is not corrupt")
				}

				return errs.Wrap(err)
			}
			if info.Mode().IsDir() {
				continue
			}
			blobInfo, ok := decodeBlobInfo(namespace, keyPrefix, keyDir, info)
			if !ok {
				continue
			}
			err = walkFunc(blobInfo)
			if err != nil {
				return err
			}
			// also check for context done between every walkFunc callback.
			if err := ctx.Err(); err != nil {
				return err
			}
		}
	}
}

// removeAllContent deletes everything in the folder.
func removeAllContent(ctx context.Context, path string) (err error) {
	defer mon.Task()(&ctx)(&err)
	dir, err := os.Open(path)
	if err != nil {
		return err
	}

	for {
		files, err := dir.Readdirnames(100)
		for _, file := range files {
			// the file might be still in use, so ignore the error
			_ = os.RemoveAll(filepath.Join(path, file))
		}
		if errors.Is(err, io.EOF) || len(files) == 0 {
			return dir.Close()
		}
		if err != nil {
			return err
		}
	}
}

// DiskInfo contains statistics about this dir.
type DiskInfo struct {
	ID             string
	AvailableSpace int64
}

// Info returns information about the current state of the dir.
func (dir *Dir) Info(ctx context.Context) (DiskInfo, error) {
	path, err := filepath.Abs(dir.path)
	if err != nil {
		return DiskInfo{}, err
	}
	return diskInfoFromPath(path)
}

type blobInfo struct {
	ref           storage.BlobRef
	path          string
	fileInfo      os.FileInfo
	formatVersion storage.FormatVersion
}

func newBlobInfo(ref storage.BlobRef, path string, fileInfo os.FileInfo, formatVer storage.FormatVersion) storage.BlobInfo {
	return &blobInfo{
		ref:           ref,
		path:          path,
		fileInfo:      fileInfo,
		formatVersion: formatVer,
	}
}

func (info *blobInfo) BlobRef() storage.BlobRef {
	return info.ref
}

func (info *blobInfo) StorageFormatVersion() storage.FormatVersion {
	return info.formatVersion
}

func (info *blobInfo) Stat(ctx context.Context) (os.FileInfo, error) {
	return info.fileInfo, nil
}

func (info *blobInfo) FullPath(ctx context.Context) (string, error) {
	return info.path, nil
}
