// Copyright (C) 2020 Storj Labs, Inc.
// See LICENSE for copying information.

package admin_test

import (
	"encoding/json"
	"fmt"
	"io/ioutil"
	"net/http"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	"storj.io/common/macaroon"
	"storj.io/common/testcontext"
	"storj.io/common/uuid"
	"storj.io/storj/private/testplanet"
	"storj.io/storj/satellite"
	"storj.io/storj/satellite/console"
)

func TestApiKeyAdd(t *testing.T) {
	testplanet.Run(t, testplanet.Config{
		SatelliteCount:   1,
		StorageNodeCount: 0,
		UplinkCount:      1,
		Reconfigure: testplanet.Reconfigure{
			Satellite: func(_ *zap.Logger, _ int, config *satellite.Config) {
				config.Admin.Address = "127.0.0.1:0"
			},
		},
	}, func(t *testing.T, ctx *testcontext.Context, planet *testplanet.Planet) {
		address := planet.Satellites[0].Admin.Admin.Listener.Addr()
		projectID := planet.Uplinks[0].Projects[0].ID

		keys, err := planet.Satellites[0].DB.Console().APIKeys().GetPagedByProjectID(ctx, projectID, console.APIKeyCursor{Page: 1, Limit: 10})
		require.NoError(t, err)
		require.Len(t, keys.APIKeys, 1)

		body := strings.NewReader(`{"name":"Default"}`)
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, fmt.Sprintf("http://"+address.String()+"/api/projects/%s/apikeys", projectID.String()), body)
		require.NoError(t, err)
		req.Header.Set("Authorization", planet.Satellites[0].Config.Console.AuthToken)

		response, err := http.DefaultClient.Do(req)
		require.NoError(t, err)
		require.Equal(t, http.StatusOK, response.StatusCode)
		require.Equal(t, "application/json", response.Header.Get("Content-Type"))
		responseBody, err := ioutil.ReadAll(response.Body)
		require.NoError(t, err)
		require.NoError(t, response.Body.Close())

		var output struct {
			APIKey string `json:"apikey"`
		}

		err = json.Unmarshal(responseBody, &output)
		require.NoError(t, err)

		apikey, err := macaroon.ParseAPIKey(output.APIKey)
		require.NoError(t, err)
		require.NotNil(t, apikey)

		keys, err = planet.Satellites[0].DB.Console().APIKeys().GetPagedByProjectID(ctx, projectID, console.APIKeyCursor{Page: 1, Limit: 10})
		require.NoError(t, err)
		require.Len(t, keys.APIKeys, 2)

		key, err := planet.Satellites[0].DB.Console().APIKeys().GetByHead(ctx, apikey.Head())
		require.NoError(t, err)
		require.Equal(t, "Default", key.Name)
	})
}

func TestApiKeyDelete(t *testing.T) {
	testplanet.Run(t, testplanet.Config{
		SatelliteCount:   1,
		StorageNodeCount: 0,
		UplinkCount:      1,
		Reconfigure: testplanet.Reconfigure{
			Satellite: func(_ *zap.Logger, _ int, config *satellite.Config) {
				config.Admin.Address = "127.0.0.1:0"
			},
		},
	}, func(t *testing.T, ctx *testcontext.Context, planet *testplanet.Planet) {
		address := planet.Satellites[0].Admin.Admin.Listener.Addr()
		projectID := planet.Uplinks[0].Projects[0].ID

		keys, err := planet.Satellites[0].DB.Console().APIKeys().GetPagedByProjectID(ctx, projectID, console.APIKeyCursor{Page: 1, Limit: 10})
		require.NoError(t, err)
		require.Len(t, keys.APIKeys, 1)

		apikey := planet.Uplinks[0].APIKey[planet.Satellites[0].ID()].Serialize()

		link := fmt.Sprintf("http://"+address.String()+"/api/apikeys/%s", apikey)
		body := assertReq(ctx, t, link, http.MethodDelete, "", http.StatusOK, "", planet.Satellites[0].Config.Console.AuthToken)
		require.Len(t, body, 0)

		keys, err = planet.Satellites[0].DB.Console().APIKeys().GetPagedByProjectID(ctx, projectID, console.APIKeyCursor{Page: 1, Limit: 10})
		require.NoError(t, err)
		require.Len(t, keys.APIKeys, 0)

		// Delete a deleted key returns Not Found.
		body = assertReq(ctx, t, link, http.MethodDelete, "", http.StatusNotFound, "", planet.Satellites[0].Config.Console.AuthToken)
		require.Contains(t, string(body), "does not exist")
	})
}

func TestApiKeyDelete_ByName(t *testing.T) {
	testplanet.Run(t, testplanet.Config{
		SatelliteCount:   1,
		StorageNodeCount: 0,
		UplinkCount:      1,
		Reconfigure: testplanet.Reconfigure{
			Satellite: func(_ *zap.Logger, _ int, config *satellite.Config) {
				config.Admin.Address = "127.0.0.1:0"
			},
		},
	}, func(t *testing.T, ctx *testcontext.Context, planet *testplanet.Planet) {
		address := planet.Satellites[0].Admin.Admin.Listener.Addr()
		projectID := planet.Uplinks[0].Projects[0].ID

		keys, err := planet.Satellites[0].DB.Console().APIKeys().GetPagedByProjectID(ctx, projectID, console.APIKeyCursor{Page: 1, Limit: 10})
		require.NoError(t, err)
		require.Len(t, keys.APIKeys, 1)

		link := fmt.Sprintf("http://"+address.String()+"/api/projects/%s/apikeys/%s", projectID.String(), keys.APIKeys[0].Name)
		body := assertReq(ctx, t, link, http.MethodDelete, "", http.StatusOK, "", planet.Satellites[0].Config.Console.AuthToken)
		require.Len(t, body, 0)

		keys, err = planet.Satellites[0].DB.Console().APIKeys().GetPagedByProjectID(ctx, projectID, console.APIKeyCursor{Page: 1, Limit: 10})
		require.NoError(t, err)
		require.Len(t, keys.APIKeys, 0)

		// Delete a deleted key returns Not Found.
		body = assertReq(ctx, t, link, http.MethodDelete, "", http.StatusNotFound, "", planet.Satellites[0].Config.Console.AuthToken)
		require.Contains(t, string(body), "does not exist")
	})
}

func TestApiKeysList(t *testing.T) {
	testplanet.Run(t, testplanet.Config{
		SatelliteCount:   1,
		StorageNodeCount: 0,
		UplinkCount:      1,
		Reconfigure: testplanet.Reconfigure{
			Satellite: func(_ *zap.Logger, _ int, config *satellite.Config) {
				config.Admin.Address = "127.0.0.1:0"
			},
		},
	}, func(t *testing.T, ctx *testcontext.Context, planet *testplanet.Planet) {
		var (
			sat       = planet.Satellites[0]
			authToken = planet.Satellites[0].Config.Console.AuthToken
			address   = sat.Admin.Admin.Listener.Addr()
		)

		project, err := sat.DB.Console().Projects().Get(ctx, planet.Uplinks[0].Projects[0].ID)
		require.NoError(t, err)

		link := "http://" + address.String() + "/api/projects/" + project.ID.String() + "/apikeys"

		{ // Delete initial API Keys to run this test

			page, err := sat.DB.Console().APIKeys().GetPagedByProjectID(
				ctx, project.ID, console.APIKeyCursor{
					Limit: 50, Page: 1, Order: console.KeyName, OrderDirection: console.Ascending,
				},
			)
			require.NoError(t, err)

			// Ensure that we are getting all the initial keys with one single page.
			require.Len(t, page.APIKeys, int(page.TotalCount))

			for _, ak := range page.APIKeys {
				require.NoError(t, sat.DB.Console().APIKeys().Delete(ctx, ak.ID))
			}
		}

		// Check get initial list of API keys.
		assertGet(ctx, t, link, "[]", authToken)

		{ // Create 2 new API Key.
			body := assertReq(ctx, t, link, http.MethodPost, `{"name": "first"}`, http.StatusOK, "", authToken)
			apiKey := struct {
				Apikey string `json:"apikey"`
			}{}
			require.NoError(t, json.Unmarshal(body, &apiKey))
			require.NotEmpty(t, apiKey.Apikey)

			body = assertReq(ctx, t, link, http.MethodPost, `{"name": "second"}`, http.StatusOK, "", authToken)
			require.NoError(t, json.Unmarshal(body, &apiKey))
			require.NotEmpty(t, apiKey.Apikey)

			// TODO: figure out how to create an API Key associated to a partner.
			// sat.DB.Console().APIKeys.Update only allows to update the API Key name
		}

		// Check get list of API keys.
		body := assertReq(ctx, t, link, http.MethodGet, "", http.StatusOK, "", authToken)

		var apiKeys []struct {
			ID        string `json:"id"`
			ProjectID string `json:"projectId"`
			Name      string `json:"name"`
			PartnerID string `json:"partnerID"`
			CreatedAt string `json:"createdAt"`
		}
		require.NoError(t, json.Unmarshal(body, &apiKeys))
		require.Len(t, apiKeys, 2)
		{ // Assert API keys info.
			a := apiKeys[0]
			assert.NotEmpty(t, a.ID, "API key ID")
			assert.Equal(t, "first", a.Name, "API key name")
			assert.Equal(t, project.ID.String(), a.ProjectID, "API key project ID")
			assert.Equal(t, uuid.UUID{}.String(), a.PartnerID, "API key partner ID")
			assert.NotEmpty(t, a.CreatedAt, "API key created at")

			a = apiKeys[1]
			assert.NotEmpty(t, a.ID, "API key ID")
			assert.Equal(t, "second", a.Name, "API key name")
			assert.Equal(t, project.ID.String(), a.ProjectID, "API key project ID")
			assert.Equal(t, uuid.UUID{}.String(), a.PartnerID, "API key partner ID")
			assert.NotEmpty(t, a.CreatedAt, "API key created at")
		}

		{ // Delete one API key to check that the endpoint just returns one.
			id, err := uuid.FromString(apiKeys[1].ID)
			require.NoError(t, err)
			require.NoError(t, sat.DB.Console().APIKeys().Delete(ctx, id))
		}

		body = assertReq(ctx, t, link, http.MethodGet, "", http.StatusOK, "", authToken)
		require.NoError(t, json.Unmarshal(body, &apiKeys))
		require.Len(t, apiKeys, 1)
		{ // Assert API keys info.
			a := apiKeys[0]
			assert.NotEmpty(t, a.ID, "API key ID")
			assert.Equal(t, "first", a.Name, "API key name")
			assert.Equal(t, project.ID.String(), a.ProjectID, "API key project ID")
			assert.Equal(t, uuid.UUID{}.String(), a.PartnerID, "API key partner ID")
			assert.NotEmpty(t, a.CreatedAt, "API key created at")
		}

		{ // Delete the one API key that last to check that the endpoint just returns none.
			id, err := uuid.FromString(apiKeys[0].ID)
			require.NoError(t, err)
			require.NoError(t, sat.DB.Console().APIKeys().Delete(ctx, id))
		}

		// Check get initial list of API keys.
		assertGet(ctx, t, link, "[]", authToken)
	})
}
