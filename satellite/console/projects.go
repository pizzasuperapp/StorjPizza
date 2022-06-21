// Copyright (C) 2019 Storj Labs, Inc.
// See LICENSE for copying information.

package console

import (
	"context"
	"errors"
	"time"

	"storj.io/common/memory"
	"storj.io/common/uuid"
)

// Projects exposes methods to manage Project table in database.
//
// architecture: Database
type Projects interface {
	// GetAll is a method for querying all projects from the database.
	GetAll(ctx context.Context) ([]Project, error)
	// GetCreatedBefore retrieves all projects created before provided date.
	GetCreatedBefore(ctx context.Context, before time.Time) ([]Project, error)
	// GetByUserID returns a list of projects where user is a project member.
	GetByUserID(ctx context.Context, userID uuid.UUID) ([]Project, error)
	// GetOwn returns a list of projects where user is an owner.
	GetOwn(ctx context.Context, userID uuid.UUID) ([]Project, error)
	// Get is a method for querying project from the database by id.
	Get(ctx context.Context, id uuid.UUID) (*Project, error)
	// Insert is a method for inserting project into the database.
	Insert(ctx context.Context, project *Project) (*Project, error)
	// Delete is a method for deleting project by Id from the database.
	Delete(ctx context.Context, id uuid.UUID) error
	// Update is a method for updating project entity.
	Update(ctx context.Context, project *Project) error
	// List returns paginated projects, created before provided timestamp.
	List(ctx context.Context, offset int64, limit int, before time.Time) (ProjectsPage, error)
	// ListByOwnerID is a method for querying all projects from the database by ownerID. It also includes the number of members for each project.
	ListByOwnerID(ctx context.Context, userID uuid.UUID, cursor ProjectsCursor) (ProjectsPage, error)

	// UpdateRateLimit is a method for updating projects rate limit.
	UpdateRateLimit(ctx context.Context, id uuid.UUID, newLimit int) error

	// UpdateBurstLimit is a method for updating projects burst limit.
	UpdateBurstLimit(ctx context.Context, id uuid.UUID, newLimit int) error

	// GetMaxBuckets is a method to get the maximum number of buckets allowed for the project
	GetMaxBuckets(ctx context.Context, id uuid.UUID) (*int, error)
	// UpdateBucketLimit is a method for updating projects bucket limit.
	UpdateBucketLimit(ctx context.Context, id uuid.UUID, newLimit int) error
}

// UsageLimitsConfig is a configuration struct for default per-project usage limits.
type UsageLimitsConfig struct {
	Storage   StorageLimitConfig
	Bandwidth BandwidthLimitConfig
	Segment   SegmentLimitConfig
	Project   ProjectLimitConfig
}

// StorageLimitConfig is a configuration struct for default storage per-project usage limits.
type StorageLimitConfig struct {
	Free memory.Size `help:"the default free-tier storage usage limit" default:"150.00GB" testDefault:"25.00 GB"`
	Paid memory.Size `help:"the default paid-tier storage usage limit" default:"25.00TB" testDefault:"25.00 GB"`
}

// BandwidthLimitConfig is a configuration struct for default bandwidth per-project usage limits.
type BandwidthLimitConfig struct {
	Free memory.Size `help:"the default free-tier bandwidth usage limit" default:"150.00GB" testDefault:"25.00 GB"`
	Paid memory.Size `help:"the default paid-tier bandwidth usage limit" default:"100.00TB" testDefault:"25.00 GB"`
}

// SegmentLimitConfig is a configuration struct for default segments per-project usage limits.
type SegmentLimitConfig struct {
	Free int64 `help:"the default free-tier segment usage limit" default:"150000"`
	Paid int64 `help:"the default paid-tier segment usage limit" default:"100000000"`
}

// ProjectLimitConfig is a configuration struct for default project limits.
type ProjectLimitConfig struct {
	Free int `help:"the default free-tier project limit" default:"1"`
	Paid int `help:"the default paid-tier project limit" default:"3"`
}

// Project is a database object that describes Project entity.
type Project struct {
	ID uuid.UUID `json:"id"`

	Name           string       `json:"name"`
	Description    string       `json:"description"`
	PartnerID      uuid.UUID    `json:"partnerId"`
	UserAgent      []byte       `json:"userAgent"`
	OwnerID        uuid.UUID    `json:"ownerId"`
	RateLimit      *int         `json:"rateLimit"`
	BurstLimit     *int         `json:"burstLimit"`
	MaxBuckets     *int         `json:"maxBuckets"`
	CreatedAt      time.Time    `json:"createdAt"`
	MemberCount    int          `json:"memberCount"`
	StorageLimit   *memory.Size `json:"storageLimit"`
	BandwidthLimit *memory.Size `json:"bandwidthLimit"`
	SegmentLimit   *int64       `json:"segmentLimit"`
}

// ProjectInfo holds data needed to create/update Project.
type ProjectInfo struct {
	Name           string      `json:"name"`
	Description    string      `json:"description"`
	StorageLimit   memory.Size `json:"storageLimit"`
	BandwidthLimit memory.Size `json:"bandwidthLimit"`
	CreatedAt      time.Time   `json:"createdAt"`
}

// ProjectsCursor holds info for project
// cursor pagination.
type ProjectsCursor struct {
	Limit int
	Page  int
}

// ProjectsPage returns paginated projects,
// providing next offset if there are more projects
// to retrieve.
type ProjectsPage struct {
	Projects   []Project
	Next       bool
	NextOffset int64

	Limit  int
	Offset int64

	PageCount   int
	CurrentPage int
	TotalCount  int64
}

// ValidateNameAndDescription validates project name and description strings.
// Project name must have more than 0 and less than 21 symbols.
// Project description can't have more than hundred symbols.
func ValidateNameAndDescription(name string, description string) error {
	if len(name) == 0 {
		return errors.New("project name can't be empty")
	}

	if len(name) > 20 {
		return errors.New("project name can't have more than 20 symbols")
	}

	if len(description) > 100 {
		return errors.New("project description can't have more than 100 symbols")
	}

	return nil
}
