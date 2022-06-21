// Copyright (C) 2019 Storj Labs, Inc.
// See LICENSE for copying information.

package tally

import (
	"context"
	"time"

	"github.com/spacemonkeygo/monkit/v3"
	"github.com/zeebo/errs"
	"go.uber.org/zap"

	"storj.io/common/sync2"
	"storj.io/common/uuid"
	"storj.io/storj/satellite/accounting"
	"storj.io/storj/satellite/metabase"
)

// Error is a standard error class for this package.
var (
	Error = errs.Class("tally")
	mon   = monkit.Package()
)

// Config contains configurable values for the tally service.
type Config struct {
	Interval            time.Duration `help:"how frequently the tally service should run" releaseDefault:"1h" devDefault:"30s" testDefault:"$TESTINTERVAL"`
	SaveRollupBatchSize int           `help:"how large of batches SaveRollup should process at a time" default:"1000"`
	ReadRollupBatchSize int           `help:"how large of batches GetBandwidthSince should process at a time" default:"10000"`

	ListLimit          int           `help:"how many objects to query in a batch" default:"2500"`
	AsOfSystemInterval time.Duration `help:"as of system interval" releaseDefault:"-5m" devDefault:"-1us" testDefault:"-1us"`
}

// Service is the tally service for data stored on each storage node.
//
// architecture: Chore
type Service struct {
	log    *zap.Logger
	config Config
	Loop   *sync2.Cycle

	metabase                *metabase.DB
	liveAccounting          accounting.Cache
	storagenodeAccountingDB accounting.StoragenodeAccounting
	projectAccountingDB     accounting.ProjectAccounting
	nowFn                   func() time.Time
}

// New creates a new tally Service.
func New(log *zap.Logger, sdb accounting.StoragenodeAccounting, pdb accounting.ProjectAccounting, liveAccounting accounting.Cache, metabase *metabase.DB, config Config) *Service {
	return &Service{
		log:    log,
		config: config,
		Loop:   sync2.NewCycle(config.Interval),

		metabase:                metabase,
		liveAccounting:          liveAccounting,
		storagenodeAccountingDB: sdb,
		projectAccountingDB:     pdb,
		nowFn:                   time.Now,
	}
}

// Run the tally service loop.
func (service *Service) Run(ctx context.Context) (err error) {
	defer mon.Task()(&ctx)(&err)

	return service.Loop.Run(ctx, func(ctx context.Context) error {
		err := service.Tally(ctx)
		if err != nil {
			service.log.Error("tally failed", zap.Error(err))
		}
		return nil
	})
}

// Close stops the service and releases any resources.
func (service *Service) Close() error {
	service.Loop.Close()
	return nil
}

// SetNow allows tests to have the Service act as if the current time is whatever
// they want. This avoids races and sleeping, making tests more reliable and efficient.
func (service *Service) SetNow(now func() time.Time) {
	service.nowFn = now
}

// Tally calculates data-at-rest usage once.
//
// How live accounting is calculated:
//
// At the beginning of the tally iteration, we get a map containing the current
// project totals from the cache- initialLiveTotals (our current estimation of
// the project totals). At the end of the tally iteration, we have the totals
// from what we saw during the metainfo loop.
//
// However, data which was uploaded during the loop may or may not have been
// seen in the metainfo loop. For this reason, we also read the live accounting
// totals again at the end of the tally iteration- latestLiveTotals.
//
// The difference between latest and initial indicates how much data was
// uploaded during the metainfo loop and is assigned to delta. However, again,
// we aren't certain how much of the delta is accounted for in the metainfo
// totals. For the reason we make an assumption that 50% of the data is
// accounted for. So to calculate the new live accounting totals, we sum the
// metainfo totals and 50% of the deltas.
func (service *Service) Tally(ctx context.Context) (err error) {
	defer mon.Task()(&ctx)(&err)

	// No-op unless that there isn't an error getting the
	// liveAccounting.GetAllProjectTotals
	updateLiveAccountingTotals := func(_ map[uuid.UUID]accounting.Usage) {}

	initialLiveTotals, err := service.liveAccounting.GetAllProjectTotals(ctx)
	if err != nil {
		service.log.Error(
			"tally won't update the live accounting storage usages of the projects in this cycle",
			zap.Error(err),
		)
	} else {
		updateLiveAccountingTotals = func(tallyProjectTotals map[uuid.UUID]accounting.Usage) {
			latestLiveTotals, err := service.liveAccounting.GetAllProjectTotals(ctx)
			if err != nil {
				service.log.Error(
					"tally isn't updating the live accounting storage usages of the projects in this cycle",
					zap.Error(err),
				)
				return
			}

			// empty projects are not returned by the metainfo observer. If a project exists
			// in live accounting, but not in tally projects, we would not update it in live accounting.
			// Thus, we add them and set the total to 0.
			for projectID := range latestLiveTotals {
				if _, ok := tallyProjectTotals[projectID]; !ok {
					tallyProjectTotals[projectID] = accounting.Usage{}
				}
			}

			for projectID, tallyTotal := range tallyProjectTotals {
				delta := latestLiveTotals[projectID].Storage - initialLiveTotals[projectID].Storage
				if delta < 0 {
					delta = 0
				}

				// read the method documentation why the increase passed to this method
				// is calculated in this way
				err = service.liveAccounting.AddProjectStorageUsage(ctx, projectID, -latestLiveTotals[projectID].Storage+tallyTotal.Storage+(delta/2))
				if err != nil {
					if accounting.ErrSystemOrNetError.Has(err) {
						service.log.Error(
							"tally isn't updating the live accounting storage usages of the projects in this cycle",
							zap.Error(err),
						)
						return
					}

					service.log.Error(
						"tally isn't updating the live accounting storage usage of the project in this cycle",
						zap.String("projectID", projectID.String()),
						zap.Error(err),
					)
				}

				// difference between cached project totals and latest tally collector
				increment := tallyTotal.Segments - latestLiveTotals[projectID].Segments

				err = service.liveAccounting.UpdateProjectSegmentUsage(ctx, projectID, increment)
				if err != nil {
					if accounting.ErrSystemOrNetError.Has(err) {
						service.log.Error(
							"tally isn't updating the live accounting segment usages of the projects in this cycle",
							zap.Error(err),
						)
						return
					}

					service.log.Error(
						"tally isn't updating the live accounting segment usage of the project in this cycle",
						zap.String("projectID", projectID.String()),
						zap.Error(err),
					)
				}
			}
		}
	}

	// add up all buckets
	collector := NewBucketTallyCollector(service.log.Named("observer"), service.nowFn(), service.metabase, service.config)
	err = collector.Run(ctx)
	if err != nil {
		return Error.Wrap(err)
	}
	finishTime := service.nowFn()

	// save the new results
	var errAtRest error
	if len(collector.Bucket) > 0 {
		// record bucket tallies to DB
		err = service.projectAccountingDB.SaveTallies(ctx, finishTime, collector.Bucket)
		if err != nil {
			errAtRest = Error.New("ProjectAccounting.SaveTallies failed: %v", err)
		}

		updateLiveAccountingTotals(projectTotalsFromBuckets(collector.Bucket))
	}

	if len(collector.Bucket) > 0 {
		var total accounting.BucketTally
		// TODO for now we don't have access to inline/remote stats per bucket
		// but that may change in the future. To get back those stats we would
		// most probably need to add inline/remote information to object in
		// metabase. We didn't decide yet if that is really needed right now.
		for _, bucket := range collector.Bucket {
			monAccounting.IntVal("bucket_objects").Observe(bucket.ObjectCount) //mon:locked
			monAccounting.IntVal("bucket_segments").Observe(bucket.Segments()) //mon:locked
			// monAccounting.IntVal("bucket_inline_segments").Observe(bucket.InlineSegments) //mon:locked
			// monAccounting.IntVal("bucket_remote_segments").Observe(bucket.RemoteSegments) //mon:locked

			monAccounting.IntVal("bucket_bytes").Observe(bucket.Bytes()) //mon:locked
			// monAccounting.IntVal("bucket_inline_bytes").Observe(bucket.InlineBytes) //mon:locked
			// monAccounting.IntVal("bucket_remote_bytes").Observe(bucket.RemoteBytes) //mon:locked
			total.Combine(bucket)
		}
		monAccounting.IntVal("total_objects").Observe(total.ObjectCount) //mon:locked
		monAccounting.IntVal("total_segments").Observe(total.Segments()) //mon:locked
		monAccounting.IntVal("total_bytes").Observe(total.Bytes())       //mon:locked
		monAccounting.IntVal("total_pending_objects").Observe(total.PendingObjectCount)
	}

	// return errors if something went wrong.
	return errAtRest
}

// BucketTallyCollector collects and adds up tallies for buckets.
type BucketTallyCollector struct {
	Now    time.Time
	Log    *zap.Logger
	Bucket map[metabase.BucketLocation]*accounting.BucketTally

	metabase *metabase.DB
	config   Config
}

// NewBucketTallyCollector returns an collector that adds up totals for buckets.
// The now argument controls when the collector considers objects to be expired.
func NewBucketTallyCollector(log *zap.Logger, now time.Time, db *metabase.DB, config Config) *BucketTallyCollector {
	return &BucketTallyCollector{
		Now:    now,
		Log:    log,
		Bucket: make(map[metabase.BucketLocation]*accounting.BucketTally),

		metabase: db,
		config:   config,
	}
}

// Run runs collecting bucket tallies.
func (observer *BucketTallyCollector) Run(ctx context.Context) (err error) {
	defer mon.Task()(&ctx)(&err)

	startingTime, err := observer.metabase.Now(ctx)
	if err != nil {
		return err
	}

	return observer.metabase.IterateLoopObjects(ctx, metabase.IterateLoopObjects{
		BatchSize:          observer.config.ListLimit,
		AsOfSystemTime:     startingTime,
		AsOfSystemInterval: observer.config.AsOfSystemInterval,
	}, func(ctx context.Context, it metabase.LoopObjectsIterator) (err error) {
		var entry metabase.LoopObjectEntry
		for it.Next(ctx, &entry) {
			err = observer.object(ctx, entry)
			if err != nil {
				return err
			}
		}
		return nil
	})
}

// ensureBucket returns bucket corresponding to the passed in path.
func (observer *BucketTallyCollector) ensureBucket(location metabase.ObjectLocation) *accounting.BucketTally {
	bucketLocation := location.Bucket()
	bucket, exists := observer.Bucket[bucketLocation]
	if !exists {
		bucket = &accounting.BucketTally{}
		bucket.BucketLocation = bucketLocation
		observer.Bucket[bucketLocation] = bucket
	}

	return bucket
}

// Object is called for each object once.
func (observer *BucketTallyCollector) object(ctx context.Context, object metabase.LoopObjectEntry) (err error) {
	defer mon.Task()(&ctx)(&err)

	if object.Expired(observer.Now) {
		return nil
	}

	bucket := observer.ensureBucket(object.ObjectStream.Location())
	bucket.TotalSegments += int64(object.SegmentCount)
	bucket.TotalBytes += object.TotalEncryptedSize
	bucket.MetadataSize += int64(object.EncryptedMetadataSize)
	bucket.ObjectCount++
	if object.Status == metabase.Pending {
		bucket.PendingObjectCount++
	}

	return nil
}

func projectTotalsFromBuckets(buckets map[metabase.BucketLocation]*accounting.BucketTally) map[uuid.UUID]accounting.Usage {
	projectTallyTotals := make(map[uuid.UUID]accounting.Usage)
	for _, bucket := range buckets {
		projectUsage := projectTallyTotals[bucket.ProjectID]
		projectUsage.Storage += bucket.TotalBytes
		projectUsage.Segments += bucket.TotalSegments
		projectTallyTotals[bucket.ProjectID] = projectUsage
	}
	return projectTallyTotals
}

// using custom name to avoid breaking monitoring.
var monAccounting = monkit.ScopeNamed("storj.io/storj/satellite/accounting")
