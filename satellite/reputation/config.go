// Copyright (C) 2021 Storj Labs, Inc.
// See LICENSE for copying information.

package reputation

import (
	"fmt"
	"time"

	"github.com/spacemonkeygo/monkit/v3"
	"github.com/zeebo/errs"

	"storj.io/common/storj"
)

var (
	mon = monkit.Package()
	// Error is the default reputation errs class.
	Error = errs.Class("reputation")
	// ErrNodeNotFound is returned if a node does not exist in database.
	ErrNodeNotFound = errs.Class("node not found")
)

// Config contains all config values for the reputation service.
type Config struct {
	AuditRepairWeight     float64       `help:"weight to apply to audit reputation for total repair reputation calculation" default:"1.0"`
	AuditUplinkWeight     float64       `help:"weight to apply to audit reputation for total uplink reputation calculation" default:"1.0"`
	AuditLambda           float64       `help:"the forgetting factor used to calculate the audit SNs reputation" default:"0.95"`
	AuditWeight           float64       `help:"the normalization weight used to calculate the audit SNs reputation" default:"1.0"`
	AuditDQ               float64       `help:"the reputation cut-off for disqualifying SNs based on audit history" default:"0.6"`
	SuspensionGracePeriod time.Duration `help:"the time period that must pass before suspended nodes will be disqualified" releaseDefault:"168h" devDefault:"1h"`
	SuspensionDQEnabled   bool          `help:"whether nodes will be disqualified if they have been suspended for longer than the suspended grace period" releaseDefault:"false" devDefault:"true"`
	AuditCount            int64         `help:"the number of times a node has been audited to not be considered a New Node" releaseDefault:"100" devDefault:"0"`
	AuditHistory          AuditHistoryConfig
}

// UpdateRequest is used to update a node's reputation status.
type UpdateRequest struct {
	NodeID       storj.NodeID
	AuditOutcome AuditType
	// Config is a copy of the Config struct from the satellite.
	// It is part of the UpdateRequest struct in order to be more easily
	// accessible from satellitedb code.
	Config
}

// AuditHistoryConfig is a configuration struct defining time periods and thresholds for penalizing nodes for being offline.
// It is used for downtime suspension and disqualification.
type AuditHistoryConfig struct {
	WindowSize               time.Duration `help:"The length of time spanning a single audit window" releaseDefault:"12h" devDefault:"5m" testDefault:"10m"`
	TrackingPeriod           time.Duration `help:"The length of time to track audit windows for node suspension and disqualification" releaseDefault:"720h" devDefault:"1h"`
	GracePeriod              time.Duration `help:"The length of time to give suspended SNOs to diagnose and fix issues causing downtime. Afterwards, they will have one tracking period to reach the minimum online score before disqualification" releaseDefault:"168h" devDefault:"1h"`
	OfflineThreshold         float64       `help:"The point below which a node is punished for offline audits. Determined by calculating the ratio of online/total audits within each window and finding the average across windows within the tracking period." default:"0.6"`
	OfflineDQEnabled         bool          `help:"whether nodes will be disqualified if they have low online score after a review period" releaseDefault:"false" devDefault:"true"`
	OfflineSuspensionEnabled bool          `help:"whether nodes will be suspended if they have low online score" releaseDefault:"true" devDefault:"true"`
}

// AuditType is an enum representing the outcome of a particular audit.
type AuditType int

const (
	// AuditSuccess represents a successful audit.
	AuditSuccess AuditType = iota
	// AuditFailure represents a failed audit.
	AuditFailure
	// AuditUnknown represents an audit that resulted in an unknown error from the node.
	AuditUnknown
	// AuditOffline represents an audit where a node was offline.
	AuditOffline
)

func (auditType AuditType) String() string {
	switch auditType {
	case AuditSuccess:
		return "AuditSuccess"
	case AuditFailure:
		return "AuditFailure"
	case AuditUnknown:
		return "AuditUnknown"
	case AuditOffline:
		return "AuditOffline"
	}
	return fmt.Sprintf("<unregistered audittype %d>", auditType)
}
