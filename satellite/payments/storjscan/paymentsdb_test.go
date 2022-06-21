// Copyright (C) 2022 Storj Labs, Inc.
// See LICENSE for copying information.

package storjscan_test

import (
	"math/big"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/zeebo/errs"

	"storj.io/common/testcontext"
	"storj.io/common/testrand"
	"storj.io/storj/private/blockchain"
	"storj.io/storj/satellite"
	"storj.io/storj/satellite/payments/monetary"
	"storj.io/storj/satellite/payments/storjscan"
	"storj.io/storj/satellite/payments/storjscan/blockchaintest"
	"storj.io/storj/satellite/satellitedb/satellitedbtest"
)

func TestPaymentsDBInsertBatch(t *testing.T) {
	satellitedbtest.Run(t, func(ctx *testcontext.Context, t *testing.T, db satellite.DB) {
		paymentsDB := db.StorjscanPayments()
		now := time.Now().Truncate(time.Second)

		var payments []storjscan.CachedPayment
		for i := 0; i < 100; i++ {
			payments = append(payments, storjscan.CachedPayment{
				From:        blockchaintest.NewAddress(),
				To:          blockchaintest.NewAddress(),
				TokenValue:  monetary.AmountFromBaseUnits(1000, monetary.StorjToken),
				BlockHash:   blockchaintest.NewHash(),
				BlockNumber: int64(i),
				Transaction: blockchaintest.NewHash(),
				Status:      storjscan.PaymentStatusConfirmed,
				LogIndex:    i,
				Timestamp:   now.Add(time.Duration(i) * time.Second),
			})
		}

		err := paymentsDB.InsertBatch(ctx, payments)
		require.NoError(t, err)
	})
}

func TestPaymentsDBList(t *testing.T) {
	satellitedbtest.Run(t, func(ctx *testcontext.Context, t *testing.T, db satellite.DB) {
		paymentsDB := db.StorjscanPayments()
		now := time.Now().Truncate(time.Second)

		var blocks []blockHeader
		for i := 0; i < 5; i++ {
			blocks = append(blocks, blockHeader{
				Hash:      blockchaintest.NewHash(),
				Number:    int64(i),
				Timestamp: now.Add(time.Duration(i) * time.Second),
			})
		}
		wallet1 := blockchaintest.NewAddress()
		wallet2 := blockchaintest.NewAddress()
		tx1 := blockchaintest.NewHash()
		tx2 := blockchaintest.NewHash()

		expected := []storjscan.CachedPayment{
			blocks[0].NewPayment(paymentLog{
				From:        blockchaintest.NewAddress(),
				To:          wallet1,
				TokenValue:  new(big.Int).SetInt64(testrand.Int63n(1000)),
				Transaction: tx1,
				Index:       0,
			}, storjscan.PaymentStatusConfirmed),
			blocks[0].NewPayment(paymentLog{
				From:        blockchaintest.NewAddress(),
				To:          wallet1,
				TokenValue:  new(big.Int).SetInt64(testrand.Int63n(1000)),
				Transaction: tx1,
				Index:       1,
			}, storjscan.PaymentStatusConfirmed),
			blocks[0].NewPayment(paymentLog{
				From:        blockchaintest.NewAddress(),
				To:          wallet1,
				TokenValue:  new(big.Int).SetInt64(testrand.Int63n(1000)),
				Transaction: blockchaintest.NewHash(),
				Index:       2,
			}, storjscan.PaymentStatusConfirmed),
			blocks[1].NewPayment(paymentLog{
				From:        blockchaintest.NewAddress(),
				To:          wallet1,
				TokenValue:  new(big.Int).SetInt64(testrand.Int63n(1000)),
				Transaction: blockchaintest.NewHash(),
				Index:       0,
			}, storjscan.PaymentStatusConfirmed),
			blocks[1].NewPayment(paymentLog{
				From:        blockchaintest.NewAddress(),
				To:          wallet2,
				TokenValue:  new(big.Int).SetInt64(testrand.Int63n(1000)),
				Transaction: blockchaintest.NewHash(),
				Index:       1,
			}, storjscan.PaymentStatusConfirmed),
			blocks[2].NewPayment(paymentLog{
				From:        blockchaintest.NewAddress(),
				To:          wallet2,
				TokenValue:  new(big.Int).SetInt64(testrand.Int63n(1000)),
				Transaction: blockchaintest.NewHash(),
				Index:       0,
			}, storjscan.PaymentStatusConfirmed),
			blocks[3].NewPayment(paymentLog{
				From:        blockchaintest.NewAddress(),
				To:          wallet1,
				TokenValue:  new(big.Int).SetInt64(testrand.Int63n(1000)),
				Transaction: blockchaintest.NewHash(),
				Index:       0,
			}, storjscan.PaymentStatusConfirmed),
			blocks[4].NewPayment(paymentLog{
				From:        blockchaintest.NewAddress(),
				To:          wallet1,
				TokenValue:  new(big.Int).SetInt64(testrand.Int63n(1000)),
				Transaction: blockchaintest.NewHash(),
				Index:       0,
			}, storjscan.PaymentStatusPending),
			blocks[4].NewPayment(paymentLog{
				From:        blockchaintest.NewAddress(),
				To:          wallet2,
				TokenValue:  new(big.Int).SetInt64(testrand.Int63n(1000)),
				Transaction: tx2,
				Index:       1,
			}, storjscan.PaymentStatusPending),
			blocks[4].NewPayment(paymentLog{
				From:        blockchaintest.NewAddress(),
				To:          wallet2,
				TokenValue:  new(big.Int).SetInt64(testrand.Int63n(1000)),
				Transaction: tx2,
				Index:       2,
			}, storjscan.PaymentStatusPending),
		}

		err := paymentsDB.InsertBatch(ctx, expected)
		require.NoError(t, err)

		t.Run("List", func(t *testing.T) {
			actual, err := paymentsDB.List(ctx)
			require.NoError(t, err)
			require.Equal(t, expected, actual)
		})
		t.Run("ListWallet", func(t *testing.T) {
			var expectedW []storjscan.CachedPayment
			expectedW = append(expectedW,
				expected[0], expected[1], expected[2], expected[3], expected[6], expected[7])

			actual, err := paymentsDB.ListWallet(ctx, wallet1, 10, 0)
			require.NoError(t, err)
			require.Equal(t, expectedW, actual)
		})
	})
}

func TestPaymentsDBLastBlock(t *testing.T) {
	satellitedbtest.Run(t, func(ctx *testcontext.Context, t *testing.T, db satellite.DB) {
		paymentsDB := db.StorjscanPayments()
		now := time.Now().Truncate(time.Second)

		var payments []storjscan.CachedPayment
		for i := 0; i < 10; i++ {
			payments = append(payments, storjscan.CachedPayment{
				From:        blockchaintest.NewAddress(),
				To:          blockchaintest.NewAddress(),
				TokenValue:  monetary.AmountFromBaseUnits(1000, monetary.StorjToken),
				Status:      storjscan.PaymentStatusConfirmed,
				BlockHash:   blockchaintest.NewHash(),
				BlockNumber: int64(i),
				Transaction: blockchaintest.NewHash(),
				LogIndex:    100,
				Timestamp:   now.Add(time.Duration(i) * time.Second),
			})
		}
		payments = append(payments, storjscan.CachedPayment{
			From:        blockchaintest.NewAddress(),
			To:          blockchaintest.NewAddress(),
			TokenValue:  monetary.AmountFromBaseUnits(1000, monetary.StorjToken),
			Status:      storjscan.PaymentStatusPending,
			BlockHash:   blockchaintest.NewHash(),
			BlockNumber: int64(10),
			Transaction: blockchaintest.NewHash(),
			LogIndex:    100,
			Timestamp:   now.Add(time.Duration(10) * time.Second),
		})

		err := paymentsDB.InsertBatch(ctx, payments)
		require.NoError(t, err)

		t.Run("payment status confirmed", func(t *testing.T) {
			last, err := paymentsDB.LastBlock(ctx, storjscan.PaymentStatusConfirmed)
			require.NoError(t, err)
			require.EqualValues(t, 9, last)
		})
		t.Run("payment status pending", func(t *testing.T) {
			last, err := paymentsDB.LastBlock(ctx, storjscan.PaymentStatusPending)
			require.NoError(t, err)
			require.EqualValues(t, 10, last)
		})
	})
}

func TestPaymentsDBLastBlockNoPayments(t *testing.T) {
	satellitedbtest.Run(t, func(ctx *testcontext.Context, t *testing.T, db satellite.DB) {
		_, err := db.StorjscanPayments().LastBlock(ctx, storjscan.PaymentStatusConfirmed)
		require.True(t, errs.Is(err, storjscan.ErrNoPayments))
	})
}

func TestPaymentsDBDeletePending(t *testing.T) {
	satellitedbtest.Run(t, func(ctx *testcontext.Context, t *testing.T, db satellite.DB) {
		paymentsDB := db.StorjscanPayments()
		now := time.Now().Truncate(time.Second)

		var blocks []blockHeader
		for i := 0; i < 6; i++ {
			blocks = append(blocks, blockHeader{
				Hash:      blockchaintest.NewHash(),
				Number:    int64(i),
				Timestamp: now.Add(time.Duration(i) * time.Second),
			})
		}

		payments := []storjscan.CachedPayment{
			blocks[0].NewPayment(paymentLog{
				From:        blockchaintest.NewAddress(),
				To:          blockchaintest.NewAddress(),
				TokenValue:  new(big.Int).SetInt64(testrand.Int63n(1000)),
				Transaction: blockchaintest.NewHash(),
				Index:       0,
			}, storjscan.PaymentStatusConfirmed),
			blocks[1].NewPayment(paymentLog{
				From:        blockchaintest.NewAddress(),
				To:          blockchaintest.NewAddress(),
				TokenValue:  new(big.Int).SetInt64(testrand.Int63n(1000)),
				Transaction: blockchaintest.NewHash(),
				Index:       0,
			}, storjscan.PaymentStatusConfirmed),
			blocks[2].NewPayment(paymentLog{
				From:        blockchaintest.NewAddress(),
				To:          blockchaintest.NewAddress(),
				TokenValue:  new(big.Int).SetInt64(testrand.Int63n(1000)),
				Transaction: blockchaintest.NewHash(),
				Index:       0,
			}, storjscan.PaymentStatusConfirmed),
			blocks[3].NewPayment(paymentLog{
				From:        blockchaintest.NewAddress(),
				To:          blockchaintest.NewAddress(),
				TokenValue:  new(big.Int).SetInt64(testrand.Int63n(1000)),
				Transaction: blockchaintest.NewHash(),
				Index:       0,
			}, storjscan.PaymentStatusConfirmed),
			blocks[4].NewPayment(paymentLog{
				From:        blockchaintest.NewAddress(),
				To:          blockchaintest.NewAddress(),
				TokenValue:  new(big.Int).SetInt64(testrand.Int63n(1000)),
				Transaction: blockchaintest.NewHash(),
				Index:       0,
			}, storjscan.PaymentStatusPending),
			blocks[5].NewPayment(paymentLog{
				From:        blockchaintest.NewAddress(),
				To:          blockchaintest.NewAddress(),
				TokenValue:  new(big.Int).SetInt64(testrand.Int63n(1000)),
				Transaction: blockchaintest.NewHash(),
				Index:       0,
			}, storjscan.PaymentStatusPending),
		}
		require.NoError(t, paymentsDB.InsertBatch(ctx, payments))

		require.NoError(t, paymentsDB.DeletePending(ctx))
		actual, err := paymentsDB.List(ctx)
		require.NoError(t, err)
		require.Equal(t, 4, len(actual))
		require.Equal(t, payments[:4], actual)
	})
}

type paymentLog struct {
	From        blockchain.Address
	To          blockchain.Address
	TokenValue  *big.Int
	Transaction blockchain.Hash
	Index       int
}

type blockHeader struct {
	Hash      blockchain.Hash
	Number    int64
	Timestamp time.Time
}

func (block blockHeader) NewPayment(log paymentLog, status storjscan.PaymentStatus) storjscan.CachedPayment {
	return storjscan.CachedPayment{
		From:        log.From,
		To:          log.To,
		TokenValue:  monetary.AmountFromBaseUnits(log.TokenValue.Int64(), monetary.StorjToken),
		Status:      status,
		BlockHash:   block.Hash,
		BlockNumber: block.Number,
		Transaction: log.Transaction,
		LogIndex:    log.Index,
		Timestamp:   block.Timestamp,
	}
}
