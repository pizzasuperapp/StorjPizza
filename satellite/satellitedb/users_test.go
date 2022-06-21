// Copyright (C) 2019 Storj Labs, Inc.
// See LICENSE for copying information.

package satellitedb_test

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"storj.io/common/testcontext"
	"storj.io/common/testrand"
	"storj.io/storj/satellite"
	"storj.io/storj/satellite/console"
	"storj.io/storj/satellite/satellitedb/satellitedbtest"
)

func TestGetUnverifiedNeedingReminderCutoff(t *testing.T) {
	satellitedbtest.Run(t, func(ctx *testcontext.Context, t *testing.T, db satellite.DB) {
		users := db.Console().Users()

		id := testrand.UUID()
		_, err := users.Insert(ctx, &console.User{
			ID:           id,
			FullName:     "test",
			Email:        "userone@mail.test",
			PasswordHash: []byte("testpassword"),
		})
		require.NoError(t, err)

		u, err := users.Get(ctx, id)
		require.NoError(t, err)
		require.Equal(t, console.UserStatus(0), u.Status)

		now := time.Now()
		reminders := now.Add(time.Hour)

		// to get a reminder, created_at needs be after cutoff.
		// since we don't have control over created_at, make cutoff in the future to test that
		// user doesn't get a reminder.
		cutoff := now.Add(time.Hour)

		needingReminder, err := users.GetUnverifiedNeedingReminder(ctx, reminders, reminders, cutoff)
		require.NoError(t, err)
		require.Len(t, needingReminder, 0)

		// change cutoff so user created_at is after it.
		// user should get a reminder.
		cutoff = now.Add(-time.Hour)

		needingReminder, err = users.GetUnverifiedNeedingReminder(ctx, now, now, cutoff)
		require.NoError(t, err)
		require.Len(t, needingReminder, 1)
	})
}

func TestUpdateUser(t *testing.T) {
	satellitedbtest.Run(t, func(ctx *testcontext.Context, t *testing.T, db satellite.DB) {
		users := db.Console().Users()
		id := testrand.UUID()
		u, err := users.Insert(ctx, &console.User{
			ID:           id,
			FullName:     "testFullName",
			ShortName:    "testShortName",
			Email:        "test@storj.test",
			PasswordHash: []byte("testPasswordHash"),
		})
		require.NoError(t, err)

		newInfo := console.User{
			FullName:                 "updatedFullName",
			ShortName:                "updatedShortName",
			PasswordHash:             []byte("updatedPasswordHash"),
			ProjectLimit:             1,
			ProjectBandwidthLimit:    1,
			ProjectStorageLimit:      1,
			ProjectSegmentLimit:      1,
			PaidTier:                 true,
			MFAEnabled:               true,
			MFASecretKey:             "secretKey",
			MFARecoveryCodes:         []string{"code1", "code2"},
			LastVerificationReminder: time.Now().Truncate(time.Second),
			FailedLoginCount:         1,
			LoginLockoutExpiration:   time.Now().Truncate(time.Second),
		}

		require.NotEqual(t, u.FullName, newInfo.FullName)
		require.NotEqual(t, u.ShortName, newInfo.ShortName)
		require.NotEqual(t, u.PasswordHash, newInfo.PasswordHash)
		require.NotEqual(t, u.ProjectLimit, newInfo.ProjectLimit)
		require.NotEqual(t, u.ProjectBandwidthLimit, newInfo.ProjectBandwidthLimit)
		require.NotEqual(t, u.ProjectStorageLimit, newInfo.ProjectStorageLimit)
		require.NotEqual(t, u.ProjectSegmentLimit, newInfo.ProjectSegmentLimit)
		require.NotEqual(t, u.PaidTier, newInfo.PaidTier)
		require.NotEqual(t, u.MFAEnabled, newInfo.MFAEnabled)
		require.NotEqual(t, u.MFASecretKey, newInfo.MFASecretKey)
		require.NotEqual(t, u.MFARecoveryCodes, newInfo.MFARecoveryCodes)
		require.NotEqual(t, u.LastVerificationReminder, newInfo.LastVerificationReminder)
		require.NotEqual(t, u.FailedLoginCount, newInfo.FailedLoginCount)
		require.NotEqual(t, u.LoginLockoutExpiration, newInfo.LoginLockoutExpiration)

		// update just fullname
		updateReq := console.UpdateUserRequest{
			FullName: &newInfo.FullName,
		}

		err = users.Update(ctx, id, updateReq)
		require.NoError(t, err)

		updatedUser, err := users.Get(ctx, id)
		require.NoError(t, err)

		u.FullName = newInfo.FullName
		require.Equal(t, u, updatedUser)

		// update just shortname
		shortNamePtr := &newInfo.ShortName
		updateReq = console.UpdateUserRequest{
			ShortName: &shortNamePtr,
		}

		err = users.Update(ctx, id, updateReq)
		require.NoError(t, err)

		updatedUser, err = users.Get(ctx, id)
		require.NoError(t, err)

		u.ShortName = newInfo.ShortName
		require.Equal(t, u, updatedUser)

		// update just password hash
		updateReq = console.UpdateUserRequest{
			PasswordHash: newInfo.PasswordHash,
		}

		err = users.Update(ctx, id, updateReq)
		require.NoError(t, err)

		updatedUser, err = users.Get(ctx, id)
		require.NoError(t, err)

		u.PasswordHash = newInfo.PasswordHash
		require.Equal(t, u, updatedUser)

		// update just project limit
		updateReq = console.UpdateUserRequest{
			ProjectLimit: &newInfo.ProjectLimit,
		}

		err = users.Update(ctx, id, updateReq)
		require.NoError(t, err)

		updatedUser, err = users.Get(ctx, id)
		require.NoError(t, err)

		u.ProjectLimit = newInfo.ProjectLimit
		require.Equal(t, u, updatedUser)

		// update just project bw limit
		updateReq = console.UpdateUserRequest{
			ProjectBandwidthLimit: &newInfo.ProjectBandwidthLimit,
		}

		err = users.Update(ctx, id, updateReq)
		require.NoError(t, err)

		updatedUser, err = users.Get(ctx, id)
		require.NoError(t, err)

		u.ProjectBandwidthLimit = newInfo.ProjectBandwidthLimit
		require.Equal(t, u, updatedUser)

		// update just project storage limit
		updateReq = console.UpdateUserRequest{
			ProjectStorageLimit: &newInfo.ProjectStorageLimit,
		}

		err = users.Update(ctx, id, updateReq)
		require.NoError(t, err)

		updatedUser, err = users.Get(ctx, id)
		require.NoError(t, err)

		u.ProjectStorageLimit = newInfo.ProjectStorageLimit
		require.Equal(t, u, updatedUser)

		// update just project segment limit
		updateReq = console.UpdateUserRequest{
			ProjectSegmentLimit: &newInfo.ProjectSegmentLimit,
		}

		err = users.Update(ctx, id, updateReq)
		require.NoError(t, err)

		updatedUser, err = users.Get(ctx, id)
		require.NoError(t, err)

		u.ProjectSegmentLimit = newInfo.ProjectSegmentLimit
		require.Equal(t, u, updatedUser)

		// update just paid tier
		updateReq = console.UpdateUserRequest{
			PaidTier: &newInfo.PaidTier,
		}

		err = users.Update(ctx, id, updateReq)
		require.NoError(t, err)

		updatedUser, err = users.Get(ctx, id)
		require.NoError(t, err)

		u.PaidTier = newInfo.PaidTier
		require.Equal(t, u, updatedUser)

		// update just mfa enabled
		updateReq = console.UpdateUserRequest{
			MFAEnabled: &newInfo.MFAEnabled,
		}

		err = users.Update(ctx, id, updateReq)
		require.NoError(t, err)

		updatedUser, err = users.Get(ctx, id)
		require.NoError(t, err)

		u.MFAEnabled = newInfo.MFAEnabled
		require.Equal(t, u, updatedUser)

		// update just mfa secret key
		secretKeyPtr := &newInfo.MFASecretKey
		updateReq = console.UpdateUserRequest{
			MFASecretKey: &secretKeyPtr,
		}

		err = users.Update(ctx, id, updateReq)
		require.NoError(t, err)

		updatedUser, err = users.Get(ctx, id)
		require.NoError(t, err)

		u.MFASecretKey = newInfo.MFASecretKey
		require.Equal(t, u, updatedUser)

		// update just mfa recovery codes
		updateReq = console.UpdateUserRequest{
			MFARecoveryCodes: &newInfo.MFARecoveryCodes,
		}

		err = users.Update(ctx, id, updateReq)
		require.NoError(t, err)

		updatedUser, err = users.Get(ctx, id)
		require.NoError(t, err)

		u.MFARecoveryCodes = newInfo.MFARecoveryCodes
		require.Equal(t, u, updatedUser)

		// update just last verification reminder
		lastReminderPtr := &newInfo.LastVerificationReminder
		updateReq = console.UpdateUserRequest{
			LastVerificationReminder: &lastReminderPtr,
		}

		err = users.Update(ctx, id, updateReq)
		require.NoError(t, err)

		updatedUser, err = users.Get(ctx, id)
		require.NoError(t, err)

		u.LastVerificationReminder = newInfo.LastVerificationReminder
		require.Equal(t, u, updatedUser)

		// update just failed login count
		updateReq = console.UpdateUserRequest{
			FailedLoginCount: &newInfo.FailedLoginCount,
		}

		err = users.Update(ctx, id, updateReq)
		require.NoError(t, err)

		updatedUser, err = users.Get(ctx, id)
		require.NoError(t, err)

		u.FailedLoginCount = newInfo.FailedLoginCount
		require.Equal(t, u, updatedUser)

		// update just login lockout expiration
		loginLockoutExpPtr := &newInfo.LoginLockoutExpiration
		updateReq = console.UpdateUserRequest{
			LoginLockoutExpiration: &loginLockoutExpPtr,
		}

		err = users.Update(ctx, id, updateReq)
		require.NoError(t, err)

		updatedUser, err = users.Get(ctx, id)
		require.NoError(t, err)

		u.LoginLockoutExpiration = newInfo.LoginLockoutExpiration
		require.Equal(t, u, updatedUser)
	})
}
