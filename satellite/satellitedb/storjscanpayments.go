// Copyright (C) 2022 Storj Labs, Inc.
// See LICENSE for copying information.

package satellitedb

import (
	"context"
	"time"

	"storj.io/private/dbutil/pgutil"
	"storj.io/storj/private/blockchain"
	"storj.io/storj/satellite/payments/monetary"
	"storj.io/storj/satellite/payments/storjscan"
	"storj.io/storj/satellite/satellitedb/dbx"
)

var _ storjscan.PaymentsDB = (*storjscanPayments)(nil)

// storjscanPayments implements storjscan.DB.
type storjscanPayments struct {
	db *satelliteDB
}

// InsertBatch inserts list of payments in a single transaction.
func (storjscanPayments *storjscanPayments) InsertBatch(ctx context.Context, payments []storjscan.CachedPayment) (err error) {
	defer mon.Task()(&ctx)(&err)

	cmnd := `INSERT INTO storjscan_payments(
				block_hash,
				block_number,
				transaction,
				log_index,
				from_address,
				to_address,
				token_value,
				usd_value,
				status,
				timestamp,
				created_at
			) SELECT
				UNNEST($1::BYTEA[]),
				UNNEST($2::INT8[]),
				UNNEST($3::BYTEA[]),
				UNNEST($4::INT4[]),
				UNNEST($5::BYTEA[]),
				UNNEST($6::BYTEA[]),
				UNNEST($7::INT8[]),
				$8,
				UNNEST($9::TEXT[]),
				UNNEST($10::TIMESTAMPTZ[]),
				$11
			`
	var (
		blockHashes   = make([][]byte, 0, len(payments))
		blockNumbers  = make([]int64, 0, len(payments))
		transactions  = make([][]byte, 0, len(payments))
		logIndexes    = make([]int32, 0, len(payments))
		fromAddresses = make([][]byte, 0, len(payments))
		toAddresses   = make([][]byte, 0, len(payments))
		tokenValues   = make([]int64, 0, len(payments))
		statuses      = make([]string, 0, len(payments))
		timestamps    = make([]time.Time, 0, len(payments))

		createdAt = time.Now()
	)
	for i := range payments {
		payment := payments[i]
		blockHashes = append(blockHashes, payment.BlockHash[:])
		blockNumbers = append(blockNumbers, payment.BlockNumber)
		transactions = append(transactions, payment.Transaction[:])
		logIndexes = append(logIndexes, int32(payment.LogIndex))
		fromAddresses = append(fromAddresses, payment.From[:])
		toAddresses = append(toAddresses, payment.To[:])
		tokenValues = append(tokenValues, payment.TokenValue.BaseUnits())
		statuses = append(statuses, string(payment.Status))
		timestamps = append(timestamps, payment.Timestamp)
	}

	_, err = storjscanPayments.db.ExecContext(ctx, cmnd,
		pgutil.ByteaArray(blockHashes),
		pgutil.Int8Array(blockNumbers),
		pgutil.ByteaArray(transactions),
		pgutil.Int4Array(logIndexes),
		pgutil.ByteaArray(fromAddresses),
		pgutil.ByteaArray(toAddresses),
		pgutil.Int8Array(tokenValues),
		0,
		pgutil.TextArray(statuses),
		pgutil.TimestampTZArray(timestamps),
		createdAt)
	return err
}

// List returns list of storjscan payments order by block number and log index desc.
func (storjscanPayments *storjscanPayments) List(ctx context.Context) (_ []storjscan.CachedPayment, err error) {
	defer mon.Task()(&ctx)(&err)

	dbxPmnts, err := storjscanPayments.db.All_StorjscanPayment_OrderBy_Asc_BlockNumber_Asc_LogIndex(ctx)
	if err != nil {
		return nil, Error.Wrap(err)
	}

	var payments []storjscan.CachedPayment
	for _, dbxPmnt := range dbxPmnts {
		payments = append(payments, fromDBXPayment(dbxPmnt))
	}

	return payments, nil
}

// ListWallet returns list of storjscan payments order by block number and log index desc.
func (storjscanPayments *storjscanPayments) ListWallet(ctx context.Context, wallet blockchain.Address, limit int, offset int64) ([]storjscan.CachedPayment, error) {
	dbxPmnts, err := storjscanPayments.db.Limited_StorjscanPayment_By_ToAddress_OrderBy_Asc_BlockNumber_Asc_LogIndex(ctx,
		dbx.StorjscanPayment_ToAddress(wallet[:]),
		limit, offset)
	if err != nil {
		return nil, Error.Wrap(err)
	}

	var payments []storjscan.CachedPayment
	for _, dbxPmnt := range dbxPmnts {
		payments = append(payments, fromDBXPayment(dbxPmnt))
	}

	return payments, nil
}

// LastBlock returns the highest block known to DB.
func (storjscanPayments *storjscanPayments) LastBlock(ctx context.Context, status storjscan.PaymentStatus) (_ int64, err error) {
	defer mon.Task()(&ctx)(&err)

	blockNumber, err := storjscanPayments.db.First_StorjscanPayment_BlockNumber_By_Status_OrderBy_Desc_BlockNumber_Desc_LogIndex(
		ctx, dbx.StorjscanPayment_Status(string(status)))
	if err != nil {
		return 0, Error.Wrap(err)
	}
	if blockNumber == nil {
		return 0, Error.Wrap(storjscan.ErrNoPayments)
	}

	return blockNumber.BlockNumber, nil
}

// DeletePending removes all pending transactions from the DB.
func (storjscanPayments storjscanPayments) DeletePending(ctx context.Context) error {
	_, err := storjscanPayments.db.Delete_StorjscanPayment_By_Status(ctx,
		dbx.StorjscanPayment_Status(storjscan.PaymentStatusPending))
	return err
}

// fromDBXPayment converts dbx storjscan payment type to storjscan.CachedPayment.
func fromDBXPayment(dbxPmnt *dbx.StorjscanPayment) storjscan.CachedPayment {
	payment := storjscan.CachedPayment{
		TokenValue:  monetary.AmountFromBaseUnits(dbxPmnt.TokenValue, monetary.StorjToken),
		Status:      storjscan.PaymentStatus(dbxPmnt.Status),
		BlockNumber: dbxPmnt.BlockNumber,
		LogIndex:    dbxPmnt.LogIndex,
		Timestamp:   dbxPmnt.Timestamp,
	}
	copy(payment.From[:], dbxPmnt.FromAddress)
	copy(payment.To[:], dbxPmnt.ToAddress)
	copy(payment.BlockHash[:], dbxPmnt.BlockHash)
	copy(payment.Transaction[:], dbxPmnt.Transaction)
	return payment
}
