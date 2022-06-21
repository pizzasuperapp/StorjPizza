// Copyright (C) 2019 Storj Labs, Inc.
// See LICENSE for copying information

package piecestore_test

import (
	"bytes"
	"io"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/zeebo/errs"
	"go.uber.org/zap"

	"storj.io/common/errs2"
	"storj.io/common/memory"
	"storj.io/common/pb"
	"storj.io/common/pkcrypto"
	"storj.io/common/rpc/rpcstatus"
	"storj.io/common/signing"
	"storj.io/common/storj"
	"storj.io/common/testcontext"
	"storj.io/common/testrand"
	"storj.io/storj/private/testblobs"
	"storj.io/storj/private/testplanet"
	"storj.io/storj/storagenode"
	"storj.io/storj/storagenode/bandwidth"
	"storj.io/uplink/private/piecestore"
)

func TestUploadAndPartialDownload(t *testing.T) {
	testplanet.Run(t, testplanet.Config{
		SatelliteCount: 1, StorageNodeCount: 6, UplinkCount: 1,
	}, func(t *testing.T, ctx *testcontext.Context, planet *testplanet.Planet) {
		expectedData := testrand.Bytes(100 * memory.KiB)

		err := planet.Uplinks[0].Upload(ctx, planet.Satellites[0], "testbucket", "test/path", expectedData)
		assert.NoError(t, err)

		var totalDownload int64
		for _, tt := range []struct {
			offset, size int64
		}{
			{0, 1510},
			{1513, 1584},
			{13581, 4783},
		} {
			func() {
				if piecestore.DefaultConfig.InitialStep < tt.size {
					t.Fatal("test expects initial step to be larger than size to download")
				}
				totalDownload += piecestore.DefaultConfig.InitialStep

				download, cleanup, err := planet.Uplinks[0].DownloadStreamRange(ctx, planet.Satellites[0], "testbucket", "test/path", tt.offset, -1)
				require.NoError(t, err)
				defer ctx.Check(cleanup)

				data := make([]byte, tt.size)
				n, err := io.ReadFull(download, data)
				require.NoError(t, err)
				assert.Equal(t, int(tt.size), n)

				assert.Equal(t, expectedData[tt.offset:tt.offset+tt.size], data)

				require.NoError(t, download.Close())
			}()
		}

		var totalBandwidthUsage bandwidth.Usage
		for _, storagenode := range planet.StorageNodes {
			usage, err := storagenode.DB.Bandwidth().Summary(ctx, time.Now().Add(-10*time.Hour), time.Now().Add(10*time.Hour))
			require.NoError(t, err)
			totalBandwidthUsage.Add(usage)
		}

		err = planet.Uplinks[0].DeleteObject(ctx, planet.Satellites[0], "testbucket", "test/path")
		require.NoError(t, err)
		_, err = planet.Uplinks[0].Download(ctx, planet.Satellites[0], "testbucket", "test/path")
		require.Error(t, err)

		// check rough limits for the upload and download
		totalUpload := int64(len(expectedData))
		t.Log(totalUpload, totalBandwidthUsage.Put, int64(len(planet.StorageNodes))*totalUpload)
		assert.True(t, totalUpload < totalBandwidthUsage.Put && totalBandwidthUsage.Put < int64(len(planet.StorageNodes))*totalUpload)
		t.Log(totalDownload, totalBandwidthUsage.Get, int64(len(planet.StorageNodes))*totalDownload)
		assert.True(t, totalBandwidthUsage.Get < int64(len(planet.StorageNodes))*totalDownload)
	})
}

func TestUpload(t *testing.T) {
	testplanet.Run(t, testplanet.Config{
		SatelliteCount: 1, StorageNodeCount: 1, UplinkCount: 1,
	}, func(t *testing.T, ctx *testcontext.Context, planet *testplanet.Planet) {
		client, err := planet.Uplinks[0].DialPiecestore(ctx, planet.StorageNodes[0])
		require.NoError(t, err)
		defer ctx.Check(client.Close)

		for _, tt := range []struct {
			pieceID       storj.PieceID
			contentLength memory.Size
			action        pb.PieceAction
			err           string
		}{
			{ // should successfully store data
				pieceID:       storj.PieceID{1},
				contentLength: 50 * memory.KiB,
				action:        pb.PieceAction_PUT,
				err:           "",
			},
			{ // should err with piece ID not specified
				pieceID:       storj.PieceID{},
				contentLength: 1 * memory.KiB,
				action:        pb.PieceAction_PUT,
				err:           "missing piece id",
			},
			{ // should err because invalid action
				pieceID:       storj.PieceID{1},
				contentLength: 1 * memory.KiB,
				action:        pb.PieceAction_GET,
				err:           "expected put or put repair action got GET",
			},
		} {
			data := testrand.Bytes(tt.contentLength)
			serialNumber := testrand.SerialNumber()

			orderLimit, piecePrivateKey := GenerateOrderLimit(
				t,
				planet.Satellites[0].ID(),
				planet.StorageNodes[0].ID(),
				tt.pieceID,
				tt.action,
				serialNumber,
				24*time.Hour,
				24*time.Hour,
				int64(len(data)),
			)
			signer := signing.SignerFromFullIdentity(planet.Satellites[0].Identity)
			orderLimit, err = signing.SignOrderLimit(ctx, signer, orderLimit)
			require.NoError(t, err)

			pieceHash, err := client.UploadReader(ctx, orderLimit, piecePrivateKey, bytes.NewReader(data))
			if tt.err != "" {
				require.Error(t, err)
				require.Contains(t, err.Error(), tt.err)
			} else {
				require.NoError(t, err)

				expectedHash := pkcrypto.SHA256Hash(data)
				assert.Equal(t, expectedHash, pieceHash.Hash)

				signee := signing.SignerFromFullIdentity(planet.StorageNodes[0].Identity)
				require.NoError(t, signing.VerifyPieceHashSignature(ctx, signee, pieceHash))
			}
		}
	})
}

// TestSlowUpload tries to mock a SlowLoris attack.
func TestSlowUpload(t *testing.T) {
	testplanet.Run(t, testplanet.Config{
		SatelliteCount: 1, StorageNodeCount: 1, UplinkCount: 1,
		Reconfigure: testplanet.Reconfigure{

			StorageNode: func(index int, config *storagenode.Config) {
				// Set MinUploadSpeed to extremely high to indicates that
				// client upload rate is slow (relative to node's standards)
				config.Storage2.MinUploadSpeed = 10000000 * memory.MB

				// Storage Node waits only few microsecond before starting the measurement
				// of upload rate to flag unsually slow connection
				config.Storage2.MinUploadSpeedGraceDuration = 500 * time.Microsecond

				config.Storage2.MinUploadSpeedCongestionThreshold = 0.8
			},
		},
	}, func(t *testing.T, ctx *testcontext.Context, planet *testplanet.Planet) {
		client, err := planet.Uplinks[0].DialPiecestore(ctx, planet.StorageNodes[0])
		require.NoError(t, err)
		defer ctx.Check(client.Close)

		for _, tt := range []struct {
			pieceID       storj.PieceID
			contentLength memory.Size
			action        pb.PieceAction
			err           string
		}{
			{ // connection should be aborted
				pieceID: storj.PieceID{1},
				// As the server node only starts flagging unusually slow connection
				// after 500 micro seconds, the file should be big enough to ensure the connection is still open.
				contentLength: 50 * memory.MB,
				action:        pb.PieceAction_PUT,
				err:           "speed too low",
			},
		} {
			data := testrand.Bytes(tt.contentLength)

			serialNumber := testrand.SerialNumber()

			orderLimit, piecePrivateKey := GenerateOrderLimit(
				t,
				planet.Satellites[0].ID(),
				planet.StorageNodes[0].ID(),
				tt.pieceID,
				tt.action,
				serialNumber,
				24*time.Hour,
				24*time.Hour,
				int64(len(data)),
			)
			signer := signing.SignerFromFullIdentity(planet.Satellites[0].Identity)
			orderLimit, err = signing.SignOrderLimit(ctx, signer, orderLimit)
			require.NoError(t, err)

			pieceHash, err := client.UploadReader(ctx, orderLimit, piecePrivateKey, bytes.NewReader(data))

			if tt.err != "" {
				require.Error(t, err)
				require.Contains(t, err.Error(), tt.err)
			} else {
				require.NoError(t, err)

				expectedHash := pkcrypto.SHA256Hash(data)
				assert.Equal(t, expectedHash, pieceHash.Hash)

				signee := signing.SignerFromFullIdentity(planet.StorageNodes[0].Identity)
				require.NoError(t, signing.VerifyPieceHashSignature(ctx, signee, pieceHash))
			}
		}
	})
}
func TestUploadOverAvailable(t *testing.T) {
	testplanet.Run(t, testplanet.Config{
		SatelliteCount: 1, StorageNodeCount: 1, UplinkCount: 1,
		Reconfigure: testplanet.Reconfigure{
			StorageNodeDB: func(index int, db storagenode.DB, log *zap.Logger) (storagenode.DB, error) {
				return testblobs.NewLimitedSpaceDB(log.Named("overload"), db, 3000000), nil
			},
			StorageNode: func(index int, config *storagenode.Config) {
				config.Storage2.Monitor.MinimumDiskSpace = 3 * memory.MB
			},
		},
	}, func(t *testing.T, ctx *testcontext.Context, planet *testplanet.Planet) {
		client, err := planet.Uplinks[0].DialPiecestore(ctx, planet.StorageNodes[0])
		require.NoError(t, err)
		defer ctx.Check(client.Close)

		var tt struct {
			pieceID       storj.PieceID
			contentLength memory.Size
			action        pb.PieceAction
			err           string
		}

		tt.pieceID = storj.PieceID{1}
		tt.contentLength = 5 * memory.MB
		tt.action = pb.PieceAction_PUT
		tt.err = "not enough available disk space, have: 3000000, need: 5000000"

		data := testrand.Bytes(tt.contentLength)
		serialNumber := testrand.SerialNumber()

		orderLimit, piecePrivateKey := GenerateOrderLimit(
			t,
			planet.Satellites[0].ID(),
			planet.StorageNodes[0].ID(),
			tt.pieceID,
			tt.action,
			serialNumber,
			24*time.Hour,
			24*time.Hour,
			int64(len(data)),
		)
		signer := signing.SignerFromFullIdentity(planet.Satellites[0].Identity)
		orderLimit, err = signing.SignOrderLimit(ctx, signer, orderLimit)
		require.NoError(t, err)

		pieceHash, err := client.UploadReader(ctx, orderLimit, piecePrivateKey, bytes.NewReader(data))
		if tt.err != "" {
			require.Error(t, err)
			require.Contains(t, err.Error(), tt.err)
		} else {
			require.NoError(t, err)

			expectedHash := pkcrypto.SHA256Hash(data)
			assert.Equal(t, expectedHash, pieceHash.Hash)

			signee := signing.SignerFromFullIdentity(planet.StorageNodes[0].Identity)
			require.NoError(t, signing.VerifyPieceHashSignature(ctx, signee, pieceHash))
		}
	})
}

func TestDownload(t *testing.T) {
	testplanet.Run(t, testplanet.Config{
		SatelliteCount: 1, StorageNodeCount: 1, UplinkCount: 1,
	}, func(t *testing.T, ctx *testcontext.Context, planet *testplanet.Planet) {
		pieceID := storj.PieceID{1}
		expectedData, _, _ := uploadPiece(t, ctx, pieceID, planet.StorageNodes[0], planet.Uplinks[0], planet.Satellites[0])
		client, err := planet.Uplinks[0].DialPiecestore(ctx, planet.StorageNodes[0])
		require.NoError(t, err)

		for _, tt := range []struct {
			pieceID storj.PieceID
			action  pb.PieceAction
			errs    []string
		}{
			{ // should successfully download data
				pieceID: pieceID,
				action:  pb.PieceAction_GET,
			},
			{ // should err with piece ID not specified
				pieceID: storj.PieceID{},
				action:  pb.PieceAction_GET,
				errs:    []string{"missing piece id"},
			},
			{ // should err with piece ID not specified
				pieceID: storj.PieceID{2},
				action:  pb.PieceAction_GET,
				errs:    []string{"file does not exist", "The system cannot find the path specified"},
			},
			{ // should err with invalid action
				pieceID: pieceID,
				action:  pb.PieceAction_PUT,
				errs:    []string{"expected get or get repair or audit action got PUT"},
			},
		} {
			tt := tt
			serialNumber := testrand.SerialNumber()

			orderLimit, piecePrivateKey := GenerateOrderLimit(
				t,
				planet.Satellites[0].ID(),
				planet.StorageNodes[0].ID(),
				tt.pieceID,
				tt.action,
				serialNumber,
				24*time.Hour,
				24*time.Hour,
				int64(len(expectedData)),
			)
			signer := signing.SignerFromFullIdentity(planet.Satellites[0].Identity)
			orderLimit, err = signing.SignOrderLimit(ctx, signer, orderLimit)
			require.NoError(t, err)

			downloader, err := client.Download(ctx, orderLimit, piecePrivateKey, 0, int64(len(expectedData)))
			require.NoError(t, err)

			buffer := make([]byte, len(expectedData))
			n, readErr := downloader.Read(buffer)

			if len(tt.errs) > 0 {
			} else {
				require.NoError(t, readErr)
				require.Equal(t, expectedData, buffer[:n])
			}

			closeErr := downloader.Close()
			err = errs.Combine(readErr, closeErr)

			switch len(tt.errs) {
			case 0:
				require.NoError(t, err)
			case 1:
				require.Error(t, err)
				require.Contains(t, err.Error(), tt.errs[0])
			case 2:
				require.Error(t, err)
				require.Conditionf(t, func() bool {
					return strings.Contains(err.Error(), tt.errs[0]) ||
						strings.Contains(err.Error(), tt.errs[1])
				}, "expected error to contain %q or %q, but it does not: %v", tt.errs[0], tt.errs[1], err)
			default:
				require.FailNow(t, "unexpected number of error cases")
			}

			// these should only be not-nil if action = pb.PieceAction_GET_REPAIR
			hash, originalLimit := downloader.GetHashAndLimit()
			require.Nil(t, hash)
			require.Nil(t, originalLimit)
		}
	})
}

func TestDownloadGetRepair(t *testing.T) {
	testplanet.Run(t, testplanet.Config{
		SatelliteCount: 1, StorageNodeCount: 1, UplinkCount: 1,
	}, func(t *testing.T, ctx *testcontext.Context, planet *testplanet.Planet) {

		pieceID := storj.PieceID{1}
		expectedData, ulOrderLimit, originHash := uploadPiece(
			t, ctx, pieceID, planet.StorageNodes[0], planet.Uplinks[0], planet.Satellites[0],
		)
		client, err := planet.Uplinks[0].DialPiecestore(ctx, planet.StorageNodes[0])
		require.NoError(t, err)

		serialNumber := testrand.SerialNumber()

		dlOrderLimit, piecePrivateKey := GenerateOrderLimit(
			t,
			planet.Satellites[0].ID(),
			planet.StorageNodes[0].ID(),
			storj.PieceID{1},
			pb.PieceAction_GET_REPAIR,
			serialNumber,
			24*time.Hour,
			24*time.Hour,
			int64(len(expectedData)),
		)
		signer := signing.SignerFromFullIdentity(planet.Satellites[0].Identity)
		dlOrderLimit, err = signing.SignOrderLimit(ctx, signer, dlOrderLimit)
		require.NoError(t, err)

		downloader, err := client.Download(ctx, dlOrderLimit, piecePrivateKey, 0, int64(len(expectedData)))
		require.NoError(t, err)

		buffer := make([]byte, len(expectedData))
		n, err := downloader.Read(buffer)

		require.NoError(t, err)
		require.Equal(t, expectedData, buffer[:n])

		err = downloader.Close()
		require.NoError(t, err)

		hash, originLimit := downloader.GetHashAndLimit()
		require.NotNil(t, hash)
		require.Equal(t, originHash.Hash, hash.Hash)
		require.Equal(t, originHash.PieceId, hash.PieceId)

		require.NotNil(t, originLimit)
		require.Equal(t, originLimit.Action, ulOrderLimit.Action)
		require.Equal(t, originLimit.Limit, ulOrderLimit.Limit)
		require.Equal(t, originLimit.PieceId, ulOrderLimit.PieceId)
		require.Equal(t, originLimit.SatelliteId, ulOrderLimit.SatelliteId)
		require.Equal(t, originLimit.SerialNumber, ulOrderLimit.SerialNumber)
		require.Equal(t, originLimit.SatelliteSignature, ulOrderLimit.SatelliteSignature)
		require.Equal(t, originLimit.UplinkPublicKey, ulOrderLimit.UplinkPublicKey)
	})
}

func TestDelete(t *testing.T) {
	testplanet.Run(t, testplanet.Config{
		SatelliteCount: 1, StorageNodeCount: 1, UplinkCount: 1,
	}, func(t *testing.T, ctx *testcontext.Context, planet *testplanet.Planet) {
		pieceID := storj.PieceID{1}
		uploadPiece(t, ctx, pieceID, planet.StorageNodes[0], planet.Uplinks[0], planet.Satellites[0])

		nodeurl := planet.StorageNodes[0].NodeURL()
		conn, err := planet.Uplinks[0].Dialer.DialNodeURL(ctx, nodeurl)
		require.NoError(t, err)
		defer ctx.Check(conn.Close)

		client := pb.NewDRPCPiecestoreClient(conn)

		for _, tt := range []struct {
			pieceID storj.PieceID
			action  pb.PieceAction
			err     string
		}{
			{ // should successfully delete data
				pieceID: pieceID,
				action:  pb.PieceAction_DELETE,
				err:     "",
			},
			{ // should err with piece ID not found
				pieceID: storj.PieceID{99},
				action:  pb.PieceAction_DELETE,
				err:     "", // TODO should this return error
			},
			{ // should err with piece ID not specified
				pieceID: storj.PieceID{},
				action:  pb.PieceAction_DELETE,
				err:     "missing piece id",
			},
			{ // should err due to incorrect action
				pieceID: pieceID,
				action:  pb.PieceAction_GET,
				err:     "expected delete action got GET",
			},
		} {
			serialNumber := testrand.SerialNumber()

			orderLimit, _ := GenerateOrderLimit(
				t,
				planet.Satellites[0].ID(),
				planet.StorageNodes[0].ID(),
				tt.pieceID,
				tt.action,
				serialNumber,
				24*time.Hour,
				24*time.Hour,
				100,
			)
			signer := signing.SignerFromFullIdentity(planet.Satellites[0].Identity)
			orderLimit, err = signing.SignOrderLimit(ctx, signer, orderLimit)
			require.NoError(t, err)

			_, err := client.Delete(ctx, &pb.PieceDeleteRequest{
				Limit: orderLimit,
			})
			if tt.err != "" {
				require.Error(t, err)
				require.Contains(t, err.Error(), tt.err)
			} else {
				require.NoError(t, err)
			}
		}
	})
}

func TestDeletePieces(t *testing.T) {
	testplanet.Run(t, testplanet.Config{
		SatelliteCount: 1, StorageNodeCount: 1, UplinkCount: 1,
	}, func(t *testing.T, ctx *testcontext.Context, planet *testplanet.Planet) {
		satellite := planet.Satellites[0]
		storagenode := planet.StorageNodes[0]

		nodeurl := storagenode.NodeURL()
		conn, err := planet.Satellites[0].Dialer.DialNodeURL(ctx, nodeurl)
		require.NoError(t, err)
		defer ctx.Check(conn.Close)

		client := pb.NewDRPCPiecestoreClient(conn)

		t.Run("ok", func(t *testing.T) {
			pieceIDs := []storj.PieceID{testrand.PieceID(), testrand.PieceID(), testrand.PieceID(), testrand.PieceID()}
			dataArray := make([][]byte, len(pieceIDs))
			for i, pieceID := range pieceIDs {
				dataArray[i], _, _ = uploadPiece(t, ctx, pieceID, storagenode, planet.Uplinks[0], satellite)
			}

			_, err := client.DeletePieces(ctx.Context, &pb.DeletePiecesRequest{
				PieceIds: pieceIDs,
			})
			require.NoError(t, err)

			planet.WaitForStorageNodeDeleters(ctx)

			for i, pieceID := range pieceIDs {
				_, err = downloadPiece(t, ctx, pieceID, int64(len(dataArray[i])), storagenode, planet.Uplinks[0], satellite)
				require.Error(t, err)
			}
			require.Condition(t, func() bool {
				return strings.Contains(err.Error(), "file does not exist") ||
					strings.Contains(err.Error(), "The system cannot find the path specified")
			}, "unexpected error message")
		})

		t.Run("ok: one piece to delete is missing", func(t *testing.T) {
			missingPieceID := testrand.PieceID()
			pieceIDs := []storj.PieceID{testrand.PieceID(), testrand.PieceID(), testrand.PieceID(), testrand.PieceID()}
			dataArray := make([][]byte, len(pieceIDs))
			for i, pieceID := range pieceIDs {
				dataArray[i], _, _ = uploadPiece(t, ctx, pieceID, storagenode, planet.Uplinks[0], satellite)
			}

			_, err := client.DeletePieces(ctx.Context, &pb.DeletePiecesRequest{
				PieceIds: append(pieceIDs, missingPieceID),
			})
			require.NoError(t, err)

			planet.WaitForStorageNodeDeleters(ctx)

			for i, pieceID := range pieceIDs {
				_, err = downloadPiece(t, ctx, pieceID, int64(len(dataArray[i])), storagenode, planet.Uplinks[0], satellite)
				require.Error(t, err)
			}
			require.Condition(t, func() bool {
				return strings.Contains(err.Error(), "file does not exist") ||
					strings.Contains(err.Error(), "The system cannot find the path specified")
			}, "unexpected error message")
		})

		t.Run("ok: no piece deleted", func(t *testing.T) {
			pieceID := testrand.PieceID()
			data, _, _ := uploadPiece(t, ctx, pieceID, storagenode, planet.Uplinks[0], satellite)

			_, err := client.DeletePieces(ctx.Context, &pb.DeletePiecesRequest{})
			require.NoError(t, err)

			planet.WaitForStorageNodeDeleters(ctx)

			downloaded, err := downloadPiece(t, ctx, pieceID, int64(len(data)), storagenode, planet.Uplinks[0], satellite)
			require.NoError(t, err)
			require.Equal(t, data, downloaded)
		})

		t.Run("error: permission denied", func(t *testing.T) {
			conn, err := planet.Uplinks[0].Dialer.DialNodeURL(ctx, nodeurl)
			require.NoError(t, err)
			defer ctx.Check(conn.Close)
			client := pb.NewDRPCPiecestoreClient(conn)

			pieceID := testrand.PieceID()
			data, _, _ := uploadPiece(t, ctx, pieceID, storagenode, planet.Uplinks[0], satellite)

			_, err = client.DeletePieces(ctx.Context, &pb.DeletePiecesRequest{
				PieceIds: []storj.PieceID{pieceID},
			})
			require.Error(t, err)
			require.Equal(t, rpcstatus.PermissionDenied, rpcstatus.Code(err))

			planet.WaitForStorageNodeDeleters(ctx)

			downloaded, err := downloadPiece(t, ctx, pieceID, int64(len(data)), storagenode, planet.Uplinks[0], satellite)
			require.NoError(t, err)
			require.Equal(t, data, downloaded)
		})
	})
}

func TestTooManyRequests(t *testing.T) {
	const uplinkCount = 6
	const maxConcurrent = 3
	const expectedFailures = uplinkCount - maxConcurrent

	testplanet.Run(t, testplanet.Config{
		SatelliteCount: 1, StorageNodeCount: 1, UplinkCount: uplinkCount,
		Reconfigure: testplanet.Reconfigure{
			StorageNode: func(index int, config *storagenode.Config) {
				config.Storage2.MaxConcurrentRequests = maxConcurrent
			},
		},
	}, func(t *testing.T, ctx *testcontext.Context, planet *testplanet.Planet) {
		failedCount := int64(expectedFailures)

		defer ctx.Wait()

		for i, uplink := range planet.Uplinks {
			i, uplink := i, uplink
			ctx.Go(func() (err error) {
				storageNode := planet.StorageNodes[0]
				config := piecestore.DefaultConfig

				client, err := piecestore.Dial(ctx, uplink.Dialer, storageNode.NodeURL(), config)
				if err != nil {
					return err
				}
				defer func() {
					if cerr := client.Close(); cerr != nil {
						uplink.Log.Error("close failed", zap.Error(cerr))
						err = errs.Combine(err, cerr)
					}
				}()

				pieceID := storj.PieceID{byte(i + 1)}
				serialNumber := testrand.SerialNumber()

				orderLimit, piecePrivateKey := GenerateOrderLimit(
					t,
					planet.Satellites[0].ID(),
					planet.StorageNodes[0].ID(),
					pieceID,
					pb.PieceAction_PUT,
					serialNumber,
					24*time.Hour,
					24*time.Hour,
					int64(10000),
				)

				satelliteSigner := signing.SignerFromFullIdentity(planet.Satellites[0].Identity)
				orderLimit, err = signing.SignOrderLimit(ctx, satelliteSigner, orderLimit)
				if err != nil {
					return errs.New("signing failed: %w", err)
				}

				_, err = client.UploadReader(ctx, orderLimit, piecePrivateKey, bytes.NewReader(make([]byte, orderLimit.Limit)))
				if err != nil {
					if errs2.IsRPC(err, rpcstatus.Unavailable) {
						if atomic.AddInt64(&failedCount, -1) < 0 {
							return errs.New("too many uploads failed: %w", err)
						}
						return nil
					}
					uplink.Log.Error("upload failed", zap.Stringer("Piece ID", pieceID), zap.Error(err))
					return err
				}

				return nil
			})
		}
	})
}

func GenerateOrderLimit(t *testing.T, satellite storj.NodeID, storageNode storj.NodeID, pieceID storj.PieceID, action pb.PieceAction, serialNumber storj.SerialNumber, pieceExpiration, orderExpiration time.Duration, limit int64) (*pb.OrderLimit, storj.PiecePrivateKey) {
	piecePublicKey, piecePrivateKey, err := storj.NewPieceKey()
	require.NoError(t, err)

	now := time.Now()
	return &pb.OrderLimit{
		SatelliteId:     satellite,
		UplinkPublicKey: piecePublicKey,
		StorageNodeId:   storageNode,
		PieceId:         pieceID,
		Action:          action,
		SerialNumber:    serialNumber,
		OrderCreation:   time.Now(),
		OrderExpiration: now.Add(orderExpiration),
		PieceExpiration: now.Add(pieceExpiration),
		Limit:           limit,
	}, piecePrivateKey
}

// uploadPiece uploads piece to storageNode.
func uploadPiece(
	t *testing.T, ctx *testcontext.Context, piece storj.PieceID, storageNode *testplanet.StorageNode,
	uplink *testplanet.Uplink, satellite *testplanet.Satellite,
) (uploadedData []byte, _ *pb.OrderLimit, _ *pb.PieceHash) {
	t.Helper()

	client, err := uplink.DialPiecestore(ctx, storageNode)
	require.NoError(t, err)
	defer ctx.Check(client.Close)

	serialNumber := testrand.SerialNumber()
	uploadedData = testrand.Bytes(10 * memory.KiB)

	orderLimit, piecePrivateKey := GenerateOrderLimit(
		t,
		satellite.ID(),
		storageNode.ID(),
		piece,
		pb.PieceAction_PUT,
		serialNumber,
		24*time.Hour,
		24*time.Hour,
		int64(len(uploadedData)),
	)
	signer := signing.SignerFromFullIdentity(satellite.Identity)
	orderLimit, err = signing.SignOrderLimit(ctx, signer, orderLimit)
	require.NoError(t, err)

	hash, err := client.UploadReader(ctx, orderLimit, piecePrivateKey, bytes.NewReader(uploadedData))
	require.NoError(t, err)

	return uploadedData, orderLimit, hash
}

// downloadPiece downlodads piece from storageNode.
func downloadPiece(
	t *testing.T, ctx *testcontext.Context, piece storj.PieceID, limit int64,
	storageNode *testplanet.StorageNode, uplink *testplanet.Uplink, satellite *testplanet.Satellite,
) (pieceData []byte, err error) {
	t.Helper()

	serialNumber := testrand.SerialNumber()
	orderLimit, piecePrivateKey := GenerateOrderLimit(
		t,
		satellite.ID(),
		storageNode.ID(),
		piece,
		pb.PieceAction_GET,
		serialNumber,
		24*time.Hour,
		24*time.Hour,
		limit,
	)
	signer := signing.SignerFromFullIdentity(satellite.Identity)
	orderLimit, err = signing.SignOrderLimit(ctx.Context, signer, orderLimit)
	require.NoError(t, err)

	client, err := uplink.DialPiecestore(ctx, storageNode)
	require.NoError(t, err)

	downloader, err := client.Download(ctx.Context, orderLimit, piecePrivateKey, 0, limit)
	require.NoError(t, err)
	defer func() {
		if err != nil {
			// Ignore err in Close if an error happened in Download because it's also
			// returned by Close.
			_ = downloader.Close()
			return
		}

		err = downloader.Close()
	}()

	buffer := make([]byte, limit)
	n, err := downloader.Read(buffer)
	return buffer[:n], err
}
