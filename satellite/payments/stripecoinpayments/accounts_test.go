// Copyright (C) 2021 Storj Labs, Inc.
// See LICENSE for copying information.

package stripecoinpayments_test

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"go.uber.org/zap/zaptest"

	"storj.io/common/testcontext"
	"storj.io/common/testrand"
	"storj.io/storj/private/testplanet"
	"storj.io/storj/private/testredis"
	"storj.io/storj/satellite/accounting"
	"storj.io/storj/satellite/accounting/live"
	"storj.io/storj/satellite/analytics"
	"storj.io/storj/satellite/console"
	"storj.io/storj/satellite/console/consoleauth"
	"storj.io/storj/satellite/console/restkeys"
	"storj.io/storj/satellite/payments"
	"storj.io/storj/satellite/payments/paymentsconfig"
	"storj.io/storj/satellite/payments/stripecoinpayments"
	"storj.io/storj/satellite/rewards"
)

func TestSignupCouponCodes(t *testing.T) {

	testplanet.Run(t, testplanet.Config{SatelliteCount: 1}, func(t *testing.T, ctx *testcontext.Context, planet *testplanet.Planet) {
		sat := planet.Satellites[0]
		db := sat.DB
		log := zaptest.NewLogger(t)

		partnersService := rewards.NewPartnersService(
			log.Named("partners"),
			rewards.DefaultPartnersDB,
		)

		analyticsService := analytics.NewService(log, analytics.Config{}, "test-satellite")

		redis, err := testredis.Mini(ctx)
		require.NoError(t, err)
		defer ctx.Check(redis.Close)

		cache, err := live.OpenCache(ctx, log.Named("cache"), live.Config{StorageBackend: "redis://" + redis.Addr() + "?db=0"})
		require.NoError(t, err)

		projectLimitCache := accounting.NewProjectLimitCache(db.ProjectAccounting(), 0, 0, 0, accounting.ProjectLimitConfig{CacheCapacity: 100})

		projectUsage := accounting.NewService(db.ProjectAccounting(), cache, projectLimitCache, *sat.API.Metainfo.Metabase, 5*time.Minute, -10*time.Second)

		pc := paymentsconfig.Config{
			StorageTBPrice: "10",
			EgressTBPrice:  "45",
			SegmentPrice:   "0.0000022",
		}

		paymentsService, err := stripecoinpayments.NewService(
			log.Named("payments.stripe:service"),
			stripecoinpayments.NewStripeMock(
				testrand.NodeID(),
				db.StripeCoinPayments().Customers(),
				db.Console().Users(),
			),
			pc.StripeCoinPayments,
			db.StripeCoinPayments(),
			db.Console().Projects(),
			db.ProjectAccounting(),
			pc.StorageTBPrice,
			pc.EgressTBPrice,
			pc.SegmentPrice,
			pc.BonusRate)
		require.NoError(t, err)

		service, err := console.NewService(
			log.Named("console"),
			db.Console(),
			restkeys.NewService(db.OIDC().OAuthTokens(), planet.Satellites[0].Config.RESTKeys),
			db.ProjectAccounting(),
			projectUsage,
			sat.API.Buckets.Service,
			partnersService,
			paymentsService.Accounts(),
			// TODO: do we need a payment deposit wallet here?
			nil,
			analyticsService,
			consoleauth.NewService(consoleauth.Config{
				TokenExpirationTime: 24 * time.Hour,
			}, &consoleauth.Hmac{Secret: []byte("my-suppa-secret-key")}),
			console.Config{PasswordCost: console.TestPasswordCost, DefaultProjectLimit: 5},
		)

		require.NoError(t, err)

		testCases := []struct {
			name               string
			email              string
			signupPromoCode    string
			expectedCouponType payments.CouponType
		}{
			{"good signup promo code", "test1@mail.test", "promo1", payments.SignupCoupon},
			{"bad signup promo code", "test2@mail.test", "badpromo", payments.NoCoupon},
		}

		for _, tt := range testCases {
			tt := tt

			t.Run(tt.name, func(t *testing.T) {
				createUser := console.CreateUser{
					FullName:        "Fullname",
					ShortName:       "Shortname",
					Email:           tt.email,
					Password:        "123a123",
					SignupPromoCode: tt.signupPromoCode,
				}

				regToken, err := service.CreateRegToken(ctx, 1)
				require.NoError(t, err)

				rootUser, err := service.CreateUser(ctx, createUser, regToken.Secret)
				require.NoError(t, err)

				couponType, err := paymentsService.Accounts().Setup(ctx, rootUser.ID, rootUser.Email, rootUser.SignupPromoCode)
				require.NoError(t, err)

				require.Equal(t, tt.expectedCouponType, couponType)
			})
		}
	})
}
