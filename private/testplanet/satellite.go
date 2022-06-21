// Copyright (C) 2019 Storj Labs, Inc.
// See LICENSE for copying information

package testplanet

import (
	"context"
	"net"
	"os"
	"path/filepath"
	"runtime/pprof"
	"strconv"

	"github.com/spf13/pflag"
	"github.com/zeebo/errs"
	"go.uber.org/zap"
	"golang.org/x/sync/errgroup"

	"storj.io/common/errs2"
	"storj.io/common/identity"
	"storj.io/common/rpc"
	"storj.io/common/storj"
	"storj.io/common/uuid"
	"storj.io/private/cfgstruct"
	"storj.io/private/version"
	"storj.io/storj/private/revocation"
	"storj.io/storj/private/server"
	"storj.io/storj/private/testredis"
	versionchecker "storj.io/storj/private/version/checker"
	"storj.io/storj/satellite"
	"storj.io/storj/satellite/accounting"
	"storj.io/storj/satellite/accounting/live"
	"storj.io/storj/satellite/accounting/nodetally"
	"storj.io/storj/satellite/accounting/projectbwcleanup"
	"storj.io/storj/satellite/accounting/rollup"
	"storj.io/storj/satellite/accounting/rolluparchive"
	"storj.io/storj/satellite/accounting/tally"
	"storj.io/storj/satellite/audit"
	"storj.io/storj/satellite/compensation"
	"storj.io/storj/satellite/console"
	"storj.io/storj/satellite/console/consoleweb"
	"storj.io/storj/satellite/contact"
	"storj.io/storj/satellite/gc"
	"storj.io/storj/satellite/gracefulexit"
	"storj.io/storj/satellite/inspector"
	"storj.io/storj/satellite/mailservice"
	"storj.io/storj/satellite/metabase"
	"storj.io/storj/satellite/metabase/segmentloop"
	"storj.io/storj/satellite/metabase/zombiedeletion"
	"storj.io/storj/satellite/metainfo"
	"storj.io/storj/satellite/metainfo/expireddeletion"
	"storj.io/storj/satellite/metrics"
	"storj.io/storj/satellite/nodestats"
	"storj.io/storj/satellite/orders"
	"storj.io/storj/satellite/overlay"
	"storj.io/storj/satellite/overlay/straynodes"
	"storj.io/storj/satellite/repair/checker"
	"storj.io/storj/satellite/repair/repairer"
	"storj.io/storj/satellite/reputation"
	"storj.io/storj/satellite/satellitedb/satellitedbtest"
)

// Satellite contains all the processes needed to run a full Satellite setup.
type Satellite struct {
	Name   string
	Config satellite.Config

	Core     *satellite.Core
	API      *satellite.API
	Repairer *satellite.Repairer
	Admin    *satellite.Admin
	GC       *satellite.GarbageCollection

	Log      *zap.Logger
	Identity *identity.FullIdentity
	DB       satellite.DB

	Dialer rpc.Dialer

	Server *server.Server

	Version *versionchecker.Service

	Contact struct {
		Service  *contact.Service
		Endpoint *contact.Endpoint
	}

	Overlay struct {
		DB           overlay.DB
		Service      *overlay.Service
		DQStrayNodes *straynodes.Chore
	}

	Metainfo struct {
		// TODO remove when uplink will be adjusted to use Metabase.DB
		Metabase *metabase.DB
		Endpoint *metainfo.Endpoint
		// TODO remove when uplink will be adjusted to use Metabase.SegmentLoop
		SegmentLoop *segmentloop.Service
	}

	Metabase struct {
		DB          *metabase.DB
		SegmentLoop *segmentloop.Service
	}

	Inspector struct {
		Endpoint *inspector.Endpoint
	}

	Orders struct {
		DB       orders.DB
		Endpoint *orders.Endpoint
		Service  *orders.Service
		Chore    *orders.Chore
	}

	Repair struct {
		Checker  *checker.Checker
		Repairer *repairer.Service
	}

	Audit struct {
		Queues   *audit.Queues
		Worker   *audit.Worker
		Chore    *audit.Chore
		Verifier *audit.Verifier
		Reporter audit.Reporter
	}

	Reputation struct {
		Service *reputation.Service
	}

	GarbageCollection struct {
		Service *gc.Service
	}

	ExpiredDeletion struct {
		Chore *expireddeletion.Chore
	}

	ZombieDeletion struct {
		Chore *zombiedeletion.Chore
	}

	Accounting struct {
		Tally            *tally.Service
		NodeTally        *nodetally.Service
		Rollup           *rollup.Service
		ProjectUsage     *accounting.Service
		ProjectBWCleanup *projectbwcleanup.Chore
		RollupArchive    *rolluparchive.Chore
	}

	LiveAccounting struct {
		Cache accounting.Cache
	}

	ProjectLimits struct {
		Cache *accounting.ProjectLimitCache
	}

	Mail struct {
		Service *mailservice.Service
	}

	Console struct {
		Listener net.Listener
		Service  *console.Service
		Endpoint *consoleweb.Server
	}

	NodeStats struct {
		Endpoint *nodestats.Endpoint
	}

	GracefulExit struct {
		Chore    *gracefulexit.Chore
		Endpoint *gracefulexit.Endpoint
	}

	Metrics struct {
		Chore *metrics.Chore
	}
}

// Label returns name for debugger.
func (system *Satellite) Label() string { return system.Name }

// ID returns the ID of the Satellite system.
func (system *Satellite) ID() storj.NodeID { return system.API.Identity.ID }

// Addr returns the public address from the Satellite system API.
func (system *Satellite) Addr() string { return system.API.Server.Addr().String() }

// URL returns the node url from the Satellite system API.
func (system *Satellite) URL() string { return system.NodeURL().String() }

// ConsoleURL returns the console URL.
func (system *Satellite) ConsoleURL() string {
	return "http://" + system.API.Console.Listener.Addr().String()
}

// NodeURL returns the storj.NodeURL from the Satellite system API.
func (system *Satellite) NodeURL() storj.NodeURL {
	return storj.NodeURL{ID: system.API.ID(), Address: system.API.Addr()}
}

// AddUser adds user to a satellite. Password from newUser will be always overridden by FullName to have
// known password which can be used automatically.
func (system *Satellite) AddUser(ctx context.Context, newUser console.CreateUser, maxNumberOfProjects int) (_ *console.User, err error) {
	defer mon.Task()(&ctx)(&err)

	regToken, err := system.API.Console.Service.CreateRegToken(ctx, maxNumberOfProjects)
	if err != nil {
		return nil, err
	}

	newUser.Password = newUser.FullName
	user, err := system.API.Console.Service.CreateUser(ctx, newUser, regToken.Secret)
	if err != nil {
		return nil, err
	}

	activationToken, err := system.API.Console.Service.GenerateActivationToken(ctx, user.ID, user.Email)
	if err != nil {
		return nil, err
	}

	_, err = system.API.Console.Service.ActivateAccount(ctx, activationToken)
	if err != nil {
		return nil, err
	}

	userCtx, err := system.UserContext(ctx, user.ID)
	if err != nil {
		return nil, err
	}
	_, err = system.API.Console.Service.Payments().SetupAccount(userCtx)
	if err != nil {
		return nil, err
	}

	return user, nil
}

// AddProject adds project to a satellite and makes specified user an owner.
func (system *Satellite) AddProject(ctx context.Context, ownerID uuid.UUID, name string) (_ *console.Project, err error) {
	defer mon.Task()(&ctx)(&err)

	ctx, err = system.UserContext(ctx, ownerID)
	if err != nil {
		return nil, err
	}
	project, err := system.API.Console.Service.CreateProject(ctx, console.ProjectInfo{
		Name: name,
	})
	if err != nil {
		return nil, err
	}
	return project, nil
}

// UserContext creates context with user.
func (system *Satellite) UserContext(ctx context.Context, userID uuid.UUID) (_ context.Context, err error) {
	defer mon.Task()(&ctx)(&err)

	user, err := system.API.Console.Service.GetUser(ctx, userID)
	if err != nil {
		return nil, err
	}

	return console.WithUser(ctx, user), nil
}

// Close closes all the subsystems in the Satellite system.
func (system *Satellite) Close() error {
	return errs.Combine(
		system.API.Close(),
		system.Core.Close(),
		system.Repairer.Close(),
		system.Admin.Close(),
		system.GC.Close(),
	)
}

// Run runs all the subsystems in the Satellite system.
func (system *Satellite) Run(ctx context.Context) (err error) {
	group, ctx := errgroup.WithContext(ctx)

	group.Go(func() error {
		return errs2.IgnoreCanceled(system.Core.Run(ctx))
	})
	group.Go(func() error {
		return errs2.IgnoreCanceled(system.API.Run(ctx))
	})
	group.Go(func() error {
		return errs2.IgnoreCanceled(system.Repairer.Run(ctx))
	})
	group.Go(func() error {
		return errs2.IgnoreCanceled(system.Admin.Run(ctx))
	})
	group.Go(func() error {
		return errs2.IgnoreCanceled(system.GC.Run(ctx))
	})
	return group.Wait()
}

// PrivateAddr returns the private address from the Satellite system API.
func (system *Satellite) PrivateAddr() string { return system.API.Server.PrivateAddr().String() }

// newSatellites initializes satellites.
func (planet *Planet) newSatellites(ctx context.Context, count int, databases satellitedbtest.SatelliteDatabases) (_ []*Satellite, err error) {
	defer mon.Task()(&ctx)(&err)

	var satellites []*Satellite

	for i := 0; i < count; i++ {
		index := i
		prefix := "satellite" + strconv.Itoa(index)
		log := planet.log.Named(prefix)

		var system *Satellite
		var err error

		pprof.Do(ctx, pprof.Labels("peer", prefix), func(ctx context.Context) {
			system, err = planet.newSatellite(ctx, prefix, index, log, databases)
		})
		if err != nil {
			return nil, err
		}

		log.Debug("id=" + system.ID().String() + " addr=" + system.Addr())
		satellites = append(satellites, system)
		planet.peers = append(planet.peers, newClosablePeer(system))
	}

	return satellites, nil
}

func (planet *Planet) newSatellite(ctx context.Context, prefix string, index int, log *zap.Logger, databases satellitedbtest.SatelliteDatabases) (_ *Satellite, err error) {
	defer mon.Task()(&ctx)(&err)

	storageDir := filepath.Join(planet.directory, prefix)
	if err := os.MkdirAll(storageDir, 0700); err != nil {
		return nil, err
	}

	identity, err := planet.NewIdentity()
	if err != nil {
		return nil, err
	}

	db, err := satellitedbtest.CreateMasterDB(ctx, log.Named("db"), planet.config.Name, "S", index, databases.MasterDB)
	if err != nil {
		return nil, err
	}

	if planet.config.Reconfigure.SatelliteDB != nil {
		var newdb satellite.DB
		newdb, err = planet.config.Reconfigure.SatelliteDB(log.Named("db"), index, db)
		if err != nil {
			return nil, errs.Combine(err, db.Close())
		}
		db = newdb
	}
	planet.databases = append(planet.databases, db)

	redis, err := testredis.Mini(ctx)
	if err != nil {
		return nil, err
	}
	encryptionKeys, err := orders.NewEncryptionKeys(orders.EncryptionKey{
		ID:  orders.EncryptionKeyID{1},
		Key: storj.Key{1},
	})
	if err != nil {
		return nil, err
	}

	var config satellite.Config
	cfgstruct.Bind(pflag.NewFlagSet("", pflag.PanicOnError), &config,
		cfgstruct.UseTestDefaults(),
		cfgstruct.ConfDir(storageDir),
		cfgstruct.IdentityDir(storageDir),
		cfgstruct.ConfigVar("TESTINTERVAL", defaultInterval.String()))

	// TODO: these are almost certainly mistakenly set to the zero value
	// in tests due to a prior mismatch between testplanet config and
	// cfgstruct devDefaults. we need to make sure it's safe to remove
	// these lines and then remove them.
	config.Debug.Control = false
	config.Reputation.AuditHistory.OfflineDQEnabled = false
	config.Server.Config.Extensions.Revocation = false
	config.Orders.OrdersSemaphoreSize = 0
	config.Checker.NodeFailureRate = 0
	config.Audit.MaxRetriesStatDB = 0
	config.GarbageCollection.RetainSendTimeout = 0
	config.ExpiredDeletion.ListLimit = 0
	config.Tally.SaveRollupBatchSize = 0
	config.Tally.ReadRollupBatchSize = 0
	config.Rollup.DeleteTallies = false
	config.Payments.BonusRate = 0
	config.Payments.NodeEgressBandwidthPrice = 0
	config.Payments.NodeRepairBandwidthPrice = 0
	config.Payments.NodeAuditBandwidthPrice = 0
	config.Payments.NodeDiskSpacePrice = 0
	config.Identity.CertPath = ""
	config.Identity.KeyPath = ""
	config.Metainfo.DatabaseURL = ""
	config.Console.ContactInfoURL = ""
	config.Console.FrameAncestors = ""
	config.Console.LetUsKnowURL = ""
	config.Console.SEO = ""
	config.Console.SatelliteOperator = ""
	config.Console.TermsAndConditionsURL = ""
	config.Console.GeneralRequestURL = ""
	config.Console.ProjectLimitsIncreaseRequestURL = ""
	config.Console.GatewayCredentialsRequestURL = ""
	config.Console.DocumentationURL = ""
	config.Console.LinksharingURL = ""
	config.Console.PathwayOverviewEnabled = false
	config.Compensation.Rates.AtRestGBHours = compensation.Rate{}
	config.Compensation.Rates.GetTB = compensation.Rate{}
	config.Compensation.Rates.GetRepairTB = compensation.Rate{}
	config.Compensation.Rates.GetAuditTB = compensation.Rate{}
	config.Compensation.WithheldPercents = nil
	config.Compensation.DisposePercent = 0
	config.ProjectLimit.CacheCapacity = 0
	config.ProjectLimit.CacheExpiration = 0
	config.Metainfo.SegmentLoop.ListLimit = 0

	// Actual testplanet-specific configuration
	config.Server.Address = planet.NewListenAddress()
	config.Server.PrivateAddress = planet.NewListenAddress()
	config.Admin.Address = planet.NewListenAddress()
	config.Console.Address = planet.NewListenAddress()
	config.Server.Config.PeerCAWhitelistPath = planet.whitelistPath
	config.Server.Config.UsePeerCAWhitelist = true
	config.Version = planet.NewVersionConfig()
	config.Metainfo.RS.Min = atLeastOne(planet.config.StorageNodeCount * 1 / 5)
	config.Metainfo.RS.Repair = atLeastOne(planet.config.StorageNodeCount * 2 / 5)
	config.Metainfo.RS.Success = atLeastOne(planet.config.StorageNodeCount * 3 / 5)
	config.Metainfo.RS.Total = atLeastOne(planet.config.StorageNodeCount * 4 / 5)
	config.Orders.EncryptionKeys = *encryptionKeys
	config.LiveAccounting.StorageBackend = "redis://" + redis.Addr() + "?db=0"
	config.Mail.TemplatePath = filepath.Join(developmentRoot, "web/satellite/static/emails")
	config.Console.StaticDir = filepath.Join(developmentRoot, "web/satellite")

	if planet.config.Reconfigure.Satellite != nil {
		planet.config.Reconfigure.Satellite(log, index, &config)
	}

	metabaseDB, err := satellitedbtest.CreateMetabaseDB(context.TODO(), log.Named("metabase"), planet.config.Name, "M", index, databases.MetabaseDB, metabase.Config{
		ApplicationName:  "satellite-testplanet",
		MinPartSize:      config.Metainfo.MinPartSize,
		MaxNumberOfParts: config.Metainfo.MaxNumberOfParts,
		ServerSideCopy:   config.Metainfo.ServerSideCopy,
	})
	if err != nil {
		return nil, err
	}

	if planet.config.Reconfigure.SatelliteMetabaseDB != nil {
		var newMetabaseDB *metabase.DB
		newMetabaseDB, err = planet.config.Reconfigure.SatelliteMetabaseDB(log.Named("metabase"), index, metabaseDB)
		if err != nil {
			return nil, errs.Combine(err, metabaseDB.Close())
		}
		metabaseDB = newMetabaseDB
	}
	planet.databases = append(planet.databases, metabaseDB)

	versionInfo := planet.NewVersionInfo()

	revocationDB, err := revocation.OpenDBFromCfg(ctx, config.Server.Config)
	if err != nil {
		return nil, errs.Wrap(err)
	}

	planet.databases = append(planet.databases, revocationDB)

	liveAccounting, err := live.OpenCache(ctx, log.Named("live-accounting"), config.LiveAccounting)
	if err != nil {
		return nil, errs.Wrap(err)
	}
	planet.databases = append(planet.databases, liveAccounting)

	rollupsWriteCache := orders.NewRollupsWriteCache(log.Named("orders-write-cache"), db.Orders(), config.Orders.FlushBatchSize)
	planet.databases = append(planet.databases, rollupsWriteCacheCloser{rollupsWriteCache})

	peer, err := satellite.New(log, identity, db, metabaseDB, revocationDB, liveAccounting, rollupsWriteCache, versionInfo, &config, nil)
	if err != nil {
		return nil, err
	}

	err = db.TestingMigrateToLatest(ctx)
	if err != nil {
		return nil, err
	}

	err = metabaseDB.TestMigrateToLatest(ctx)
	if err != nil {
		return nil, err
	}

	api, err := planet.newAPI(ctx, index, identity, db, metabaseDB, config, versionInfo)
	if err != nil {
		return nil, err
	}

	adminPeer, err := planet.newAdmin(ctx, index, identity, db, metabaseDB, config, versionInfo)
	if err != nil {
		return nil, err
	}

	repairerPeer, err := planet.newRepairer(ctx, index, identity, db, metabaseDB, config, versionInfo)
	if err != nil {
		return nil, err
	}

	gcPeer, err := planet.newGarbageCollection(ctx, index, identity, db, metabaseDB, config, versionInfo)
	if err != nil {
		return nil, err
	}

	if config.EmailReminders.Enable {
		peer.Mail.EmailReminders.TestSetLinkAddress("http://" + api.Console.Listener.Addr().String() + "/")
	}

	return createNewSystem(prefix, log, config, peer, api, repairerPeer, adminPeer, gcPeer), nil
}

// createNewSystem makes a new Satellite System and exposes the same interface from
// before we split out the API. In the short term this will help keep all the tests passing
// without much modification needed. However long term, we probably want to rework this
// so it represents how the satellite will run when it is made up of many prrocesses.
func createNewSystem(name string, log *zap.Logger, config satellite.Config, peer *satellite.Core, api *satellite.API, repairerPeer *satellite.Repairer, adminPeer *satellite.Admin, gcPeer *satellite.GarbageCollection) *Satellite {
	system := &Satellite{
		Name:     name,
		Config:   config,
		Core:     peer,
		API:      api,
		Repairer: repairerPeer,
		Admin:    adminPeer,
		GC:       gcPeer,
	}
	system.Log = log
	system.Identity = peer.Identity
	system.DB = api.DB

	system.Dialer = api.Dialer

	system.Contact.Service = api.Contact.Service
	system.Contact.Endpoint = api.Contact.Endpoint

	system.Overlay.DB = api.Overlay.DB
	system.Overlay.Service = api.Overlay.Service
	system.Overlay.DQStrayNodes = peer.Overlay.DQStrayNodes

	system.Reputation.Service = peer.Reputation.Service

	// system.Metainfo.Metabase = api.Metainfo.Metabase
	system.Metainfo.Endpoint = api.Metainfo.Endpoint
	// system.Metainfo.SegmentLoop = peer.Metainfo.SegmentLoop

	system.Metabase.DB = api.Metainfo.Metabase
	system.Metabase.SegmentLoop = peer.Metainfo.SegmentLoop

	system.Inspector.Endpoint = api.Inspector.Endpoint

	system.Orders.DB = api.Orders.DB
	system.Orders.Endpoint = api.Orders.Endpoint
	system.Orders.Service = peer.Orders.Service
	system.Orders.Chore = api.Orders.Chore

	system.Repair.Checker = peer.Repair.Checker
	system.Repair.Repairer = repairerPeer.Repairer

	system.Audit.Queues = peer.Audit.Queues
	system.Audit.Worker = peer.Audit.Worker
	system.Audit.Chore = peer.Audit.Chore
	system.Audit.Verifier = peer.Audit.Verifier
	system.Audit.Reporter = peer.Audit.Reporter

	system.GarbageCollection.Service = gcPeer.GarbageCollection.Service

	system.ExpiredDeletion.Chore = peer.ExpiredDeletion.Chore
	system.ZombieDeletion.Chore = peer.ZombieDeletion.Chore

	system.Accounting.Tally = peer.Accounting.Tally
	system.Accounting.NodeTally = peer.Accounting.NodeTally
	system.Accounting.Rollup = peer.Accounting.Rollup
	system.Accounting.ProjectUsage = api.Accounting.ProjectUsage
	system.Accounting.ProjectBWCleanup = peer.Accounting.ProjectBWCleanupChore
	system.Accounting.RollupArchive = peer.Accounting.RollupArchiveChore

	system.LiveAccounting = peer.LiveAccounting

	system.ProjectLimits.Cache = api.ProjectLimits.Cache

	system.GracefulExit.Chore = peer.GracefulExit.Chore
	system.GracefulExit.Endpoint = api.GracefulExit.Endpoint

	system.Metrics.Chore = peer.Metrics.Chore

	return system
}

func (planet *Planet) newAPI(ctx context.Context, index int, identity *identity.FullIdentity, db satellite.DB, metabaseDB *metabase.DB, config satellite.Config, versionInfo version.Info) (_ *satellite.API, err error) {
	defer mon.Task()(&ctx)(&err)

	prefix := "satellite-api" + strconv.Itoa(index)
	log := planet.log.Named(prefix)

	revocationDB, err := revocation.OpenDBFromCfg(ctx, config.Server.Config)
	if err != nil {
		return nil, errs.Wrap(err)
	}
	planet.databases = append(planet.databases, revocationDB)

	liveAccounting, err := live.OpenCache(ctx, log.Named("live-accounting"), config.LiveAccounting)
	if err != nil {
		return nil, errs.Wrap(err)
	}
	planet.databases = append(planet.databases, liveAccounting)

	rollupsWriteCache := orders.NewRollupsWriteCache(log.Named("orders-write-cache"), db.Orders(), config.Orders.FlushBatchSize)
	planet.databases = append(planet.databases, rollupsWriteCacheCloser{rollupsWriteCache})

	return satellite.NewAPI(log, identity, db, metabaseDB, revocationDB, liveAccounting, rollupsWriteCache, &config, versionInfo, nil)
}

func (planet *Planet) newAdmin(ctx context.Context, index int, identity *identity.FullIdentity, db satellite.DB, metabaseDB *metabase.DB, config satellite.Config, versionInfo version.Info) (_ *satellite.Admin, err error) {
	defer mon.Task()(&ctx)(&err)

	prefix := "satellite-admin" + strconv.Itoa(index)
	log := planet.log.Named(prefix)

	return satellite.NewAdmin(log, identity, db, metabaseDB, versionInfo, &config, nil)
}

func (planet *Planet) newRepairer(ctx context.Context, index int, identity *identity.FullIdentity, db satellite.DB, metabaseDB *metabase.DB, config satellite.Config, versionInfo version.Info) (_ *satellite.Repairer, err error) {
	defer mon.Task()(&ctx)(&err)

	prefix := "satellite-repairer" + strconv.Itoa(index)
	log := planet.log.Named(prefix)

	revocationDB, err := revocation.OpenDBFromCfg(ctx, config.Server.Config)
	if err != nil {
		return nil, errs.Wrap(err)
	}
	planet.databases = append(planet.databases, revocationDB)

	rollupsWriteCache := orders.NewRollupsWriteCache(log.Named("orders-write-cache"), db.Orders(), config.Orders.FlushBatchSize)
	planet.databases = append(planet.databases, rollupsWriteCacheCloser{rollupsWriteCache})

	return satellite.NewRepairer(log, identity, metabaseDB, revocationDB, db.RepairQueue(), db.Buckets(), db.OverlayCache(), db.Reputation(), db.Containment(), rollupsWriteCache, versionInfo, &config, nil)
}

type rollupsWriteCacheCloser struct {
	*orders.RollupsWriteCache
}

func (cache rollupsWriteCacheCloser) Close() error {
	return cache.RollupsWriteCache.CloseAndFlush(context.TODO())
}

func (planet *Planet) newGarbageCollection(ctx context.Context, index int, identity *identity.FullIdentity, db satellite.DB, metabaseDB *metabase.DB, config satellite.Config, versionInfo version.Info) (_ *satellite.GarbageCollection, err error) {
	defer mon.Task()(&ctx)(&err)

	prefix := "satellite-gc" + strconv.Itoa(index)
	log := planet.log.Named(prefix)

	revocationDB, err := revocation.OpenDBFromCfg(ctx, config.Server.Config)
	if err != nil {
		return nil, errs.Wrap(err)
	}
	planet.databases = append(planet.databases, revocationDB)
	return satellite.NewGarbageCollection(log, identity, db, metabaseDB, revocationDB, versionInfo, &config, nil)
}

// atLeastOne returns 1 if value < 1, or value otherwise.
func atLeastOne(value int) int {
	if value < 1 {
		return 1
	}
	return value
}
