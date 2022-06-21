// Copyright (C) 2019 Storj Labs, Inc.
// See LICENSE for copying information.

package satellitedb

import (
	"context"
	"sync"

	"github.com/zeebo/errs"
	"go.uber.org/zap"

	"storj.io/common/lrucache"
	"storj.io/private/dbutil"
	"storj.io/private/dbutil/pgutil"
	"storj.io/private/tagsql"
	"storj.io/storj/private/migrate"
	"storj.io/storj/satellite"
	"storj.io/storj/satellite/accounting"
	"storj.io/storj/satellite/attribution"
	"storj.io/storj/satellite/audit"
	"storj.io/storj/satellite/buckets"
	"storj.io/storj/satellite/compensation"
	"storj.io/storj/satellite/console"
	"storj.io/storj/satellite/gracefulexit"
	"storj.io/storj/satellite/nodeapiversion"
	"storj.io/storj/satellite/oidc"
	"storj.io/storj/satellite/orders"
	"storj.io/storj/satellite/overlay"
	"storj.io/storj/satellite/payments/billing"
	"storj.io/storj/satellite/payments/storjscan"
	"storj.io/storj/satellite/payments/stripecoinpayments"
	"storj.io/storj/satellite/repair/queue"
	"storj.io/storj/satellite/reputation"
	"storj.io/storj/satellite/revocation"
	"storj.io/storj/satellite/satellitedb/dbx"
	"storj.io/storj/satellite/snopayouts"
)

// Error is the default satellitedb errs class.
var Error = errs.Class("satellitedb")

type satelliteDBCollection struct {
	dbs map[string]*satelliteDB
}

// satelliteDB combines access to different database tables with a record
// of the db driver, db implementation, and db source URL.
type satelliteDB struct {
	*dbx.DB

	migrationDB tagsql.DB

	opts   Options
	log    *zap.Logger
	driver string
	impl   dbutil.Implementation
	source string

	consoleDBOnce sync.Once
	consoleDB     *ConsoleDB

	revocationDBOnce sync.Once
	revocationDB     *revocationDB
}

// Options includes options for how a satelliteDB runs.
type Options struct {
	ApplicationName      string
	APIKeysLRUOptions    lrucache.Options
	RevocationLRUOptions lrucache.Options

	// How many storage node rollups to save/read in one batch.
	SaveRollupBatchSize int
	ReadRollupBatchSize int
}

var _ dbx.DBMethods = &satelliteDB{}

var safelyPartitionableDBs = map[string]bool{
	// WARNING: only list additional db names here after they have been
	// validated to be safely partitionable and that they do not do
	// cross-db queries.
	"repairqueue": true,
}

// Open creates instance of satellite.DB.
func Open(ctx context.Context, log *zap.Logger, databaseURL string, opts Options) (rv satellite.DB, err error) {
	dbMapping, err := dbutil.ParseDBMapping(databaseURL)
	if err != nil {
		return nil, err
	}

	dbc := &satelliteDBCollection{dbs: map[string]*satelliteDB{}}
	defer func() {
		if err != nil {
			err = errs.Combine(err, dbc.Close())
		}
	}()

	for key, val := range dbMapping {
		db, err := open(ctx, log, val, opts, key)
		if err != nil {
			return nil, err
		}
		dbc.dbs[key] = db
	}

	return dbc, nil
}

func open(ctx context.Context, log *zap.Logger, databaseURL string, opts Options, override string) (*satelliteDB, error) {
	driver, source, impl, err := dbutil.SplitConnStr(databaseURL)
	if err != nil {
		return nil, err
	}
	if impl != dbutil.Postgres && impl != dbutil.Cockroach {
		return nil, Error.New("unsupported driver %q", driver)
	}

	source, err = pgutil.CheckApplicationName(source, opts.ApplicationName)
	if err != nil {
		return nil, err
	}

	dbxDB, err := dbx.Open(driver, source)
	if err != nil {
		return nil, Error.New("failed opening database via DBX at %q: %v",
			source, err)
	}
	log.Debug("Connected to:", zap.String("db source", source))

	name := "satellitedb"
	if override != "" {
		name += ":" + override
	}
	dbutil.Configure(ctx, dbxDB.DB, name, mon)

	core := &satelliteDB{
		DB: dbxDB,

		opts:   opts,
		log:    log,
		driver: driver,
		impl:   impl,
		source: source,
	}

	core.migrationDB = core

	return core, nil
}

func (dbc *satelliteDBCollection) getByName(name string) *satelliteDB {
	if safelyPartitionableDBs[name] {
		if db, exists := dbc.dbs[name]; exists {
			return db
		}
	}
	return dbc.dbs[""]
}

// TestDBAccess for raw database access,
// should not be used outside of migration tests.
func (db *satelliteDB) TestDBAccess() *dbx.DB { return db.DB }

// MigrationTestingDefaultDB assists in testing migrations themselves against
// the default database.
func (dbc *satelliteDBCollection) MigrationTestingDefaultDB() interface {
	TestDBAccess() *dbx.DB
	TestPostgresMigration() *migrate.Migration
	PostgresMigration() *migrate.Migration
} {
	return dbc.getByName("")
}

// PeerIdentities returns a storage for peer identities.
func (dbc *satelliteDBCollection) PeerIdentities() overlay.PeerIdentities {
	return &peerIdentities{db: dbc.getByName("peeridentities")}
}

// Attribution is a getter for value attribution repository.
func (dbc *satelliteDBCollection) Attribution() attribution.DB {
	return &attributionDB{db: dbc.getByName("attribution")}
}

// OverlayCache is a getter for overlay cache repository.
func (dbc *satelliteDBCollection) OverlayCache() overlay.DB {
	return &overlaycache{db: dbc.getByName("overlaycache")}
}

// Reputation is a getter for overlay cache repository.
func (dbc *satelliteDBCollection) Reputation() reputation.DB {
	return &reputations{db: dbc.getByName("reputations")}
}

// RepairQueue is a getter for RepairQueue repository.
func (dbc *satelliteDBCollection) RepairQueue() queue.RepairQueue {
	return &repairQueue{db: dbc.getByName("repairqueue")}
}

// StoragenodeAccounting returns database for tracking storagenode usage.
func (dbc *satelliteDBCollection) StoragenodeAccounting() accounting.StoragenodeAccounting {
	return &StoragenodeAccounting{db: dbc.getByName("storagenodeaccounting")}
}

// ProjectAccounting returns database for tracking project data use.
func (dbc *satelliteDBCollection) ProjectAccounting() accounting.ProjectAccounting {
	return &ProjectAccounting{db: dbc.getByName("projectaccounting")}
}

// Revocation returns the database to deal with macaroon revocation.
func (dbc *satelliteDBCollection) Revocation() revocation.DB {
	db := dbc.getByName("revocation")
	db.revocationDBOnce.Do(func() {
		db.revocationDB = &revocationDB{
			db:      db,
			lru:     lrucache.New(db.opts.RevocationLRUOptions),
			methods: db,
		}
	})
	return db.revocationDB
}

// Console returns database for storing users, projects and api keys.
func (dbc *satelliteDBCollection) Console() console.DB {
	db := dbc.getByName("console")
	db.consoleDBOnce.Do(func() {
		db.consoleDB = &ConsoleDB{
			apikeysLRUOptions: db.opts.APIKeysLRUOptions,

			db:      db,
			methods: db,

			apikeysOnce: new(sync.Once),
		}
	})

	return db.consoleDB
}

// OIDC returns the database for storing OAuth and OIDC information.
func (dbc *satelliteDBCollection) OIDC() oidc.DB {
	db := dbc.getByName("oidc")
	return oidc.NewDB(db.DB)
}

// Orders returns database for storing orders.
func (dbc *satelliteDBCollection) Orders() orders.DB {
	db := dbc.getByName("orders")
	return &ordersDB{db: db}
}

// Containment returns database for storing pending audit info.
func (dbc *satelliteDBCollection) Containment() audit.Containment {
	return &containment{db: dbc.getByName("containment")}
}

// GracefulExit returns database for graceful exit.
func (dbc *satelliteDBCollection) GracefulExit() gracefulexit.DB {
	return &gracefulexitDB{db: dbc.getByName("gracefulexit")}
}

// StripeCoinPayments returns database for stripecoinpayments.
func (dbc *satelliteDBCollection) StripeCoinPayments() stripecoinpayments.DB {
	return &stripeCoinPaymentsDB{db: dbc.getByName("stripecoinpayments")}
}

// Billing returns database for billing and payment transactions.
func (dbc *satelliteDBCollection) Billing() billing.TransactionsDB {
	return &billingDB{db: dbc.getByName("billing")}
}

// Wallets returns database for storjscan wallets.
func (dbc *satelliteDBCollection) Wallets() storjscan.WalletsDB {
	return &storjscanWalletsDB{db: dbc.getByName("storjscan")}
}

// SNOPayouts returns database for storagenode payStubs and payments info.
func (dbc *satelliteDBCollection) SNOPayouts() snopayouts.DB {
	return &snopayoutsDB{db: dbc.getByName("snopayouts")}
}

// Compensation returns database for storage node compensation.
func (dbc *satelliteDBCollection) Compensation() compensation.DB {
	return &compensationDB{db: dbc.getByName("compensation")}
}

// NodeAPIVersion returns database for storage node api version lower bounds.
func (dbc *satelliteDBCollection) NodeAPIVersion() nodeapiversion.DB {
	return &nodeAPIVersionDB{db: dbc.getByName("nodeapiversion")}
}

// Buckets returns database for interacting with buckets.
func (dbc *satelliteDBCollection) Buckets() buckets.DB {
	return &bucketsDB{db: dbc.getByName("buckets")}
}

// StorjscanPayments returns database for storjscan payments.
func (dbc *satelliteDBCollection) StorjscanPayments() storjscan.PaymentsDB {
	return &storjscanPayments{db: dbc.getByName("storjscan_payments")}
}

// CheckVersion confirms all databases are at the desired version.
func (dbc *satelliteDBCollection) CheckVersion(ctx context.Context) error {
	var eg errs.Group
	for _, db := range dbc.dbs {
		eg.Add(db.CheckVersion(ctx))
	}
	return eg.Err()
}

// MigrateToLatest migrates all databases to the latest version.
func (dbc *satelliteDBCollection) MigrateToLatest(ctx context.Context) error {
	var eg errs.Group
	for _, db := range dbc.dbs {
		eg.Add(db.MigrateToLatest(ctx))
	}
	return eg.Err()
}

// TestingMigrateToLatest is a method for creating all tables for all database for testing.
func (dbc *satelliteDBCollection) TestingMigrateToLatest(ctx context.Context) error {
	var eg errs.Group
	for _, db := range dbc.dbs {
		eg.Add(db.TestingMigrateToLatest(ctx))
	}
	return eg.Err()
}

// Close closes all satellite dbs.
func (dbc *satelliteDBCollection) Close() error {
	var eg errs.Group
	for _, db := range dbc.dbs {
		eg.Add(db.Close())
	}
	return eg.Err()
}
