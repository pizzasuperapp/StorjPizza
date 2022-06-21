// Copyright (C) 2022 Storj Labs, Inc.
// See LICENSE for copying information.

package storjscan_test

import (
	"math/big"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/zeebo/errs"
	"go.uber.org/zap/zaptest"

	"storj.io/common/testcontext"
	"storj.io/storj/satellite"
	"storj.io/storj/satellite/payments/monetary"
	"storj.io/storj/satellite/payments/storjscan"
	"storj.io/storj/satellite/payments/storjscan/blockchaintest"
	"storj.io/storj/satellite/payments/storjscan/storjscantest"
	"storj.io/storj/satellite/satellitedb/satellitedbtest"
)

func TestChore(t *testing.T) {
	satellitedbtest.Run(t, func(ctx *testcontext.Context, t *testing.T, db satellite.DB) {
		logger := zaptest.NewLogger(t)
		now := time.Now().Round(time.Second)

		const confirmations = 12

		var payments []storjscan.Payment
		var cachedPayments []storjscan.CachedPayment

		latestBlock := storjscan.Header{
			Hash:      blockchaintest.NewHash(),
			Number:    0,
			Timestamp: now,
		}

		addPayments := func(count int) {
			l := len(payments)
			for i := l; i < l+count; i++ {
				payment := storjscan.Payment{
					From:        blockchaintest.NewAddress(),
					To:          blockchaintest.NewAddress(),
					TokenValue:  new(big.Int).SetInt64(int64(i)),
					BlockHash:   blockchaintest.NewHash(),
					BlockNumber: int64(i),
					Transaction: blockchaintest.NewHash(),
					LogIndex:    i,
					Timestamp:   now.Add(time.Duration(i) * time.Second),
				}
				payments = append(payments, payment)

				cachedPayments = append(cachedPayments, storjscan.CachedPayment{
					From:        payment.From,
					To:          payment.To,
					TokenValue:  monetary.AmountFromBaseUnits(payment.TokenValue.Int64(), monetary.StorjToken),
					Status:      storjscan.PaymentStatusPending,
					BlockHash:   payment.BlockHash,
					BlockNumber: payment.BlockNumber,
					Transaction: payment.Transaction,
					LogIndex:    payment.LogIndex,
					Timestamp:   payment.Timestamp,
				})
			}

			latestBlock = storjscan.Header{
				Hash:      payments[len(payments)-1].BlockHash,
				Number:    payments[len(payments)-1].BlockNumber,
				Timestamp: payments[len(payments)-1].Timestamp,
			}
			for i := 0; i < len(cachedPayments); i++ {
				if latestBlock.Number-cachedPayments[i].BlockNumber >= confirmations {
					cachedPayments[i].Status = storjscan.PaymentStatusConfirmed
				} else {
					cachedPayments[i].Status = storjscan.PaymentStatusPending
				}
			}
		}

		var reqCounter int

		const (
			identifier = "eu"
			secret     = "secret"
		)
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			var err error
			reqCounter++

			if err = storjscantest.CheckAuth(r, identifier, secret); err != nil {
				storjscantest.ServeJSONError(t, w, http.StatusUnauthorized, err)
				return
			}

			var from int64
			if s := r.URL.Query().Get("from"); s != "" {
				from, err = strconv.ParseInt(s, 10, 64)
				if err != nil {
					storjscantest.ServeJSONError(t, w, http.StatusBadRequest, errs.New("from parameter is missing"))
					return
				}
			}

			storjscantest.ServePayments(t, w, from, latestBlock, payments)
		}))
		defer server.Close()

		paymentsDB := db.StorjscanPayments()

		client := storjscan.NewClient(server.URL, "eu", "secret")
		chore := storjscan.NewChore(logger, client, paymentsDB, confirmations, time.Second)
		ctx.Go(func() error {
			return chore.Run(ctx)
		})
		defer ctx.Check(chore.Close)

		chore.TransactionCycle.Pause()
		chore.TransactionCycle.TriggerWait()
		cachedReqCounter := reqCounter

		addPayments(100)
		chore.TransactionCycle.TriggerWait()

		last, err := paymentsDB.LastBlock(ctx, storjscan.PaymentStatusPending)
		require.NoError(t, err)
		require.EqualValues(t, 99, last)
		actual, err := paymentsDB.List(ctx)
		require.NoError(t, err)
		require.Equal(t, cachedPayments, actual)

		addPayments(100)
		chore.TransactionCycle.TriggerWait()

		last, err = paymentsDB.LastBlock(ctx, storjscan.PaymentStatusPending)
		require.NoError(t, err)
		require.EqualValues(t, 199, last)
		actual, err = paymentsDB.List(ctx)
		require.NoError(t, err)
		require.Equal(t, cachedPayments, actual)

		require.Equal(t, reqCounter, cachedReqCounter+2)
	})
}
