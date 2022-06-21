// Copyright (C) 2019 Storj Labs, Inc.
// See LICENSE for copying information.

package boltdb

import (
	"context"
	"fmt"
	"io/ioutil"
	"os"
	"path/filepath"
	"testing"

	"github.com/zeebo/errs"

	"storj.io/storj/storage"
	"storj.io/storj/storage/testsuite"
)

func TestSuite(t *testing.T) {
	tempdir, err := ioutil.TempDir("", "storj-bolt")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = os.RemoveAll(tempdir) }()

	dbname := filepath.Join(tempdir, "bolt.db")
	store, err := New(dbname, "bucket")
	if err != nil {
		t.Fatalf("failed to create db: %v", err)
	}
	defer func() {
		if err := store.Close(); err != nil {
			t.Fatalf("failed to close db: %v", err)
		}
	}()

	store.SetLookupLimit(500)
	testsuite.RunTests(t, store)
}

func BenchmarkSuite(b *testing.B) {
	tempdir, err := ioutil.TempDir("", "storj-bolt")
	if err != nil {
		b.Fatal(err)
	}
	defer func() { _ = os.RemoveAll(tempdir) }()

	dbname := filepath.Join(tempdir, "bolt.db")
	store, err := New(dbname, "bucket")
	if err != nil {
		b.Fatalf("failed to create db: %v", err)
	}
	defer func() {
		if err := store.Close(); err != nil {
			b.Fatalf("failed to close db: %v", err)
		}
	}()

	testsuite.RunBenchmarks(b, store)
}

type boltLongBenchmarkStore struct {
	*Client
	dirPath string
}

func (store *boltLongBenchmarkStore) BulkImport(ctx context.Context, iter storage.Iterator) (err error) {
	// turn off syncing during import
	oldval := store.db.NoSync
	store.db.NoSync = true
	defer func() { store.db.NoSync = oldval }()

	var item storage.ListItem
	for iter.Next(ctx, &item) {
		if err := store.Put(ctx, item.Key, item.Value); err != nil {
			return fmt.Errorf("Failed to insert data (%q, %q): %w", item.Key, item.Value, err)
		}
	}

	return store.db.Sync()
}

func (store *boltLongBenchmarkStore) BulkDeleteAll(ctx context.Context) error {
	// do nothing here; everything will be cleaned up later after the test completes. it's not
	// worth it to wait for BoltDB to remove every key, one by one, and we can't just
	// os.RemoveAll() the whole test directory at this point because those files are still open
	// and unremoveable on Windows.
	return nil
}

var _ testsuite.BulkImporter = &boltLongBenchmarkStore{}
var _ testsuite.BulkCleaner = &boltLongBenchmarkStore{}

func BenchmarkSuiteLong(b *testing.B) {
	tempdir, err := ioutil.TempDir("", "storj-bolt")
	if err != nil {
		b.Fatal(err)
	}
	defer func() {
		if err := os.RemoveAll(tempdir); err != nil {
			b.Fatal(err)
		}
	}()

	dbname := filepath.Join(tempdir, "bolt.db")
	store, err := New(dbname, "bucket")
	if err != nil {
		b.Fatalf("failed to create db: %v", err)
	}
	defer func() {
		if err := errs.Combine(store.Close(), os.RemoveAll(tempdir)); err != nil {
			b.Fatalf("failed to close db: %v", err)
		}
	}()

	longStore := &boltLongBenchmarkStore{
		Client:  store,
		dirPath: tempdir,
	}
	testsuite.BenchmarkPathOperationsInLargeDb(b, longStore)
}
