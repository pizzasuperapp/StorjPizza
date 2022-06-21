// Copyright (C) 2019 Storj Labs, Inc.
// See LICENSE for copying information.

package console

import (
	"context"
	"time"

	"storj.io/common/uuid"
)

// ProjectMembers exposes methods to manage ProjectMembers table in database.
//
// architecture: Database
type ProjectMembers interface {
	// GetByMemberID is a method for querying project members from the database by memberID.
	GetByMemberID(ctx context.Context, memberID uuid.UUID) ([]ProjectMember, error)
	// GetPagedByProjectID is a method for querying project members from the database by projectID and cursor
	GetPagedByProjectID(ctx context.Context, projectID uuid.UUID, cursor ProjectMembersCursor) (*ProjectMembersPage, error)
	// Insert is a method for inserting project member into the database.
	Insert(ctx context.Context, memberID, projectID uuid.UUID) (*ProjectMember, error)
	// Delete is a method for deleting project member by memberID and projectID from the database.
	Delete(ctx context.Context, memberID, projectID uuid.UUID) error
}

// ProjectMember is a database object that describes ProjectMember entity.
type ProjectMember struct {
	// FK on Users table.
	MemberID uuid.UUID
	// FK on Projects table.
	ProjectID uuid.UUID

	CreatedAt time.Time
}

// ProjectMembersCursor holds info for project members cursor pagination.
type ProjectMembersCursor struct {
	Search         string
	Limit          uint
	Page           uint
	Order          ProjectMemberOrder
	OrderDirection OrderDirection
}

// ProjectMembersPage represent project members page result.
type ProjectMembersPage struct {
	ProjectMembers []ProjectMember

	Search         string
	Limit          uint
	Order          ProjectMemberOrder
	OrderDirection OrderDirection
	Offset         uint64

	PageCount   uint
	CurrentPage uint
	TotalCount  uint64
}

// ProjectMemberOrder is used for querying project members in specified order.
type ProjectMemberOrder int8

const (
	// Name indicates that we should order by full name.
	Name ProjectMemberOrder = 1
	// Email indicates that we should order by email.
	Email ProjectMemberOrder = 2
	// Created indicates that we should order by created date.
	Created ProjectMemberOrder = 3
)
