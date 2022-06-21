// Copyright (C) 2019 Storj Labs, Inc.
// See LICENSE for copying information.

package contact

import (
	"context"
	"math/rand"
	"sync"
	"time"

	"github.com/spacemonkeygo/monkit/v3"
	"github.com/zeebo/errs"
	"go.uber.org/zap"
	"golang.org/x/sync/errgroup"

	"storj.io/common/pb"
	"storj.io/common/rpc"
	"storj.io/common/storj"
	"storj.io/common/sync2"
	"storj.io/storj/storagenode/trust"
)

var (
	mon = monkit.Package()

	// Error is the default error class for contact package.
	Error = errs.Class("contact")

	errPingSatellite = errs.Class("ping satellite")
)

const initialBackOff = time.Second

// Config contains configurable values for contact service.
type Config struct {
	ExternalAddress string `user:"true" help:"the public address of the node, useful for nodes behind NAT" default:""`

	// Chore config values
	Interval time.Duration `help:"how frequently the node contact chore should run" releaseDefault:"1h" devDefault:"30s"`
}

// NodeInfo contains information necessary for introducing storagenode to satellite.
type NodeInfo struct {
	ID       storj.NodeID
	Address  string
	Version  pb.NodeVersion
	Capacity pb.NodeCapacity
	Operator pb.NodeOperator
}

// Service is the contact service between storage nodes and satellites.
type Service struct {
	log    *zap.Logger
	rand   *rand.Rand
	dialer rpc.Dialer

	mu   sync.Mutex
	self NodeInfo

	trust *trust.Pool

	initialized sync2.Fence
}

// NewService creates a new contact service.
func NewService(log *zap.Logger, dialer rpc.Dialer, self NodeInfo, trust *trust.Pool) *Service {
	return &Service{
		log:    log,
		rand:   rand.New(rand.NewSource(time.Now().UnixNano())),
		dialer: dialer,
		trust:  trust,
		self:   self,
	}
}

// PingSatellites attempts to ping all satellites in trusted list until backoff reaches maxInterval.
func (service *Service) PingSatellites(ctx context.Context, maxInterval time.Duration) (err error) {
	defer mon.Task()(&ctx)(&err)
	satellites := service.trust.GetSatellites(ctx)
	var group errgroup.Group
	for _, satellite := range satellites {
		satellite := satellite
		group.Go(func() error {
			return service.pingSatellite(ctx, satellite, maxInterval)
		})
	}
	return group.Wait()
}

func (service *Service) pingSatellite(ctx context.Context, satellite storj.NodeID, maxInterval time.Duration) error {
	interval := initialBackOff
	attempts := 0
	for {

		mon.Meter("satellite_contact_request").Mark(1) //mon:locked

		err := service.pingSatelliteOnce(ctx, satellite)
		attempts++
		if err == nil {
			return nil
		}
		service.log.Error("ping satellite failed ", zap.Stringer("Satellite ID", satellite), zap.Int("attempts", attempts), zap.Error(err))

		// Sleeps until interval times out, then continue. Returns if context is cancelled.
		if !sync2.Sleep(ctx, interval) {
			service.log.Info("context cancelled", zap.Stringer("Satellite ID", satellite))
			return nil
		}
		interval *= 2
		if interval >= maxInterval {
			service.log.Info("retries timed out for this cycle", zap.Stringer("Satellite ID", satellite))
			return nil
		}
	}

}

func (service *Service) pingSatelliteOnce(ctx context.Context, id storj.NodeID) (err error) {
	defer mon.Task()(&ctx, id)(&err)

	conn, err := service.dialSatellite(ctx, id)
	if err != nil {
		return errPingSatellite.Wrap(err)
	}
	defer func() { err = errs.Combine(err, conn.Close()) }()

	self := service.Local()
	resp, err := pb.NewDRPCNodeClient(conn).CheckIn(ctx, &pb.CheckInRequest{
		Address:  self.Address,
		Version:  &self.Version,
		Capacity: &self.Capacity,
		Operator: &self.Operator,
	})
	if err != nil {
		return errPingSatellite.Wrap(err)
	}
	if resp != nil && !resp.PingNodeSuccess {
		return errPingSatellite.New("%s", resp.PingErrorMessage)
	}
	if resp.PingErrorMessage != "" {
		service.log.Warn("Your node is still considered to be online but encountered an error.", zap.Stringer("Satellite ID", id), zap.String("Error", resp.GetPingErrorMessage()))
	}
	return nil
}

// RequestPingMeQUIC sends pings request to satellite for a pingBack via QUIC.
func (service *Service) RequestPingMeQUIC(ctx context.Context) (err error) {
	defer mon.Task()(&ctx)(&err)

	satellites := service.trust.GetSatellites(ctx)
	if len(satellites) < 1 {
		return errPingSatellite.New("no trusted satellite available")
	}

	// Shuffle the satellites
	// All the Storagenodes get a default list of trusted satellites (The Storj DCS ones) and
	// most of the SN operators don't change the list, hence if it always starts with
	// the same satellite we are going to put always more pressure on the first trusted
	// satellite on the list. So we iterate over the list of trusted satellites in a
	// random order to avoid putting pressure on the first trusted on the list
	service.rand.Shuffle(len(satellites), func(i, j int) {
		satellites[i], satellites[j] = satellites[j], satellites[i]
	})

	for _, satellite := range satellites {
		err = service.requestPingMeOnce(ctx, satellite)
		if err != nil {
			// log warning and try the next trusted satellite
			service.log.Warn("failed PingMe request to satellite", zap.Stringer("Satellite ID", satellite), zap.Error(err))
			continue
		}
		return nil
	}

	return errPingSatellite.New("failed to ping storage node using QUIC: %q", err)
}

func (service *Service) requestPingMeOnce(ctx context.Context, satellite storj.NodeID) (err error) {
	defer mon.Task()(&ctx, satellite)(&err)

	conn, err := service.dialSatellite(ctx, satellite)
	if err != nil {
		return errPingSatellite.Wrap(err)
	}
	defer func() { err = errs.Combine(err, conn.Close()) }()

	node := service.Local()
	_, err = pb.NewDRPCNodeClient(conn).PingMe(ctx, &pb.PingMeRequest{
		Address:   node.Address,
		Transport: pb.NodeTransport_QUIC_GRPC,
	})
	if err != nil {
		return errPingSatellite.Wrap(err)
	}

	return nil
}

func (service *Service) dialSatellite(ctx context.Context, id storj.NodeID) (*rpc.Conn, error) {
	nodeurl, err := service.trust.GetNodeURL(ctx, id)
	if err != nil {
		return nil, errPingSatellite.Wrap(err)
	}

	return service.dialer.DialNodeURL(ctx, nodeurl)
}

// Local returns the storagenode info.
func (service *Service) Local() NodeInfo {
	service.mu.Lock()
	defer service.mu.Unlock()
	return service.self
}

// UpdateSelf updates the local node with the capacity.
func (service *Service) UpdateSelf(capacity *pb.NodeCapacity) {
	service.mu.Lock()
	defer service.mu.Unlock()
	if capacity != nil {
		service.self.Capacity = *capacity
	}
	service.initialized.Release()
}
