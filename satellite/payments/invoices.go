// Copyright (C) 2019 Storj Labs, Inc.
// See LICENSE for copying information.

package payments

import (
	"context"
	"time"

	"storj.io/common/uuid"
)

// Invoices exposes all needed functionality to manage account invoices.
//
// architecture: Service
type Invoices interface {
	// List returns a list of invoices for a given payment account.
	List(ctx context.Context, userID uuid.UUID) ([]Invoice, error)
	// ListWithDiscounts returns a list of invoices and coupon usages for a given payment account.
	ListWithDiscounts(ctx context.Context, userID uuid.UUID) ([]Invoice, []CouponUsage, error)
	// CheckPendingItems returns if pending invoice items for a given payment account exist.
	CheckPendingItems(ctx context.Context, userID uuid.UUID) (existingItems bool, err error)
}

// Invoice holds all public information about invoice.
type Invoice struct {
	ID          string    `json:"id"`
	Description string    `json:"description"`
	Amount      int64     `json:"amount"`
	Status      string    `json:"status"`
	Link        string    `json:"link"`
	Start       time.Time `json:"start"`
	End         time.Time `json:"end"`
}

// CouponUsage describes the usage of a coupon on an invoice.
type CouponUsage struct {
	Coupon      Coupon
	Amount      int64
	PeriodStart time.Time
	PeriodEnd   time.Time
}
