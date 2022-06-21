// Copyright (C) 2022 Storj Labs, Inc.
// See LICENSE for copying information.

package storjscan

import (
	"context"
	"time"

	"github.com/zeebo/errs"
	"go.uber.org/zap"

	"storj.io/common/sync2"
	"storj.io/storj/satellite/payments/monetary"
)

// ChoreErr is storjscan chore err class.
var ChoreErr = errs.Class("storjscan chore")

// Chore periodically queries for new payments from storjscan.
//
// architecture: Chore
type Chore struct {
	log              *zap.Logger
	client           *Client
	paymentsDB       PaymentsDB
	TransactionCycle *sync2.Cycle
	confirmations    int
}

// NewChore creates new chore.
func NewChore(log *zap.Logger, client *Client, paymentsDB PaymentsDB, confirmations int, interval time.Duration) *Chore {
	return &Chore{
		log:              log,
		client:           client,
		paymentsDB:       paymentsDB,
		TransactionCycle: sync2.NewCycle(interval),
		confirmations:    confirmations,
	}
}

// Run runs storjscan payment loop.
func (chore *Chore) Run(ctx context.Context) (err error) {
	defer mon.Task()(&ctx)(&err)

	return chore.TransactionCycle.Run(ctx, func(ctx context.Context) error {
		var from int64

		blockNumber, err := chore.paymentsDB.LastBlock(ctx, PaymentStatusConfirmed)
		switch {
		case err == nil:
			from = blockNumber + 1
		case errs.Is(err, ErrNoPayments):
			from = 0
		default:
			chore.log.Error("error retrieving last payment", zap.Error(ChoreErr.Wrap(err)))
			return nil
		}

		payments, err := chore.client.Payments(ctx, from)
		if err != nil {
			chore.log.Error("error retrieving payments", zap.Error(ChoreErr.Wrap(err)))
			return nil
		}
		if len(payments.Payments) == 0 {
			return nil
		}

		var cachedPayments []CachedPayment
		for _, payment := range payments.Payments {
			var status PaymentStatus
			if payments.LatestBlock.Number-payment.BlockNumber >= int64(chore.confirmations) {
				status = PaymentStatusConfirmed
			} else {
				status = PaymentStatusPending
			}

			cachedPayments = append(cachedPayments, CachedPayment{
				From:        payment.From,
				To:          payment.To,
				TokenValue:  monetary.AmountFromBaseUnits(payment.TokenValue.Int64(), monetary.StorjToken),
				Status:      status,
				BlockHash:   payment.BlockHash,
				BlockNumber: payment.BlockNumber,
				Transaction: payment.Transaction,
				LogIndex:    payment.LogIndex,
				Timestamp:   payment.Timestamp,
			})
		}

		err = chore.paymentsDB.DeletePending(ctx)
		if err != nil {
			chore.log.Error("error removing pending payments from the DB", zap.Error(ChoreErr.Wrap(err)))
			return nil
		}

		err = chore.paymentsDB.InsertBatch(ctx, cachedPayments)
		if err != nil {
			chore.log.Error("error storing payments to db", zap.Error(ChoreErr.Wrap(err)))
			return nil
		}

		return nil
	})
}

// Close closes all underlying resources.
func (chore *Chore) Close() (err error) {
	defer mon.Task()(nil)(&err)
	chore.TransactionCycle.Close()
	return nil
}
