// Copyright (C) 2021 Storj Labs, Inc.
// See LICENSE for copying information.

package consoleweb_test

import (
	"encoding/json"
	"io"
	"io/ioutil"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"storj.io/common/testcontext"
	"storj.io/storj/private/testplanet"
)

func TestAuth(t *testing.T) {
	testplanet.Run(t, testplanet.Config{
		SatelliteCount: 1, StorageNodeCount: 0, UplinkCount: 1,
	}, func(t *testing.T, ctx *testcontext.Context, planet *testplanet.Planet) {
		test := newTest(t, ctx, planet)
		user := test.defaultUser()

		{ // Register User
			_ = test.registerUser("user@mail.test", "#$Rnkl12i3nkljfds")
		}

		{ // Login_GetToken_Fail
			resp, body := test.request(
				http.MethodPost, "/auth/token",
				strings.NewReader(`{"email":"wrong@invalid.test","password":"wrong"}`))
			require.Nil(t, findCookie(resp, "_tokenKey"))
			require.Equal(t, http.StatusUnauthorized, resp.StatusCode)
			_ = body
			// TODO: require.Contains(t, body, "unauthorized")
		}

		{ // Login_GetToken_Pass
			test.login(user.email, user.password)
		}

		{ // Login_ChangePassword_IncorrectCurrentPassword
			resp, body := test.request(
				http.MethodPost, "/auth/account/change-password",
				test.toJSON(map[string]string{
					"email":       user.email,
					"password":    user.password + "1",
					"newPassword": user.password + "2",
				}))

			require.Equal(t, http.StatusUnauthorized, resp.StatusCode)
			_ = body
			//TODO: require.Contains(t, body, "password was incorrect")
		}

		{ // Login_ChangePassword
			resp, _ := test.request(
				http.MethodPost, "/auth/account/change-password`",
				test.toJSON(map[string]string{
					"email":       user.email,
					"password":    user.password,
					"newPassword": user.password,
				}))
			require.Equal(t, http.StatusOK, resp.StatusCode)
		}

		var oldCookies []*http.Cookie

		{ // Get_AccountInfo
			resp, body := test.request(http.MethodGet, "/auth/account", nil)
			require.Equal(test.t, http.StatusOK, resp.StatusCode)
			require.Contains(test.t, body, "fullName")
			oldCookies = resp.Cookies()

			var userIdentifier struct{ ID string }
			require.NoError(test.t, json.Unmarshal([]byte(body), &userIdentifier))
			require.NotEmpty(test.t, userIdentifier.ID)
		}

		{ // Logout
			resp, _ := test.request(http.MethodPost, "/auth/logout", nil)
			cookie := findCookie(resp, "_tokenKey")
			require.NotNil(t, cookie)
			require.Equal(t, "", cookie.Value)
			require.Equal(t, http.StatusOK, resp.StatusCode)
		}

		{ // Get_AccountInfo shouldn't succeed after logging out
			resp, body := test.request(http.MethodGet, "/auth/account", nil)
			// TODO: wrong error text
			// require.Contains(test.t, body, "unauthorized")
			require.Contains(test.t, body, "error")
			require.Equal(test.t, http.StatusUnauthorized, resp.StatusCode)
		}

		{ // Get_AccountInfo shouldn't succeed with reused session cookie
			satURL, err := url.Parse(test.url(""))
			require.NoError(t, err)
			test.client.Jar.SetCookies(satURL, oldCookies)

			resp, body := test.request(http.MethodGet, "/auth/account", nil)
			require.Contains(test.t, body, "error")
			require.Equal(test.t, http.StatusUnauthorized, resp.StatusCode)
		}

		{ // repeated login attempts should end in too many requests
			hitRateLimiter := false
			for i := 0; i < 30; i++ {
				resp, _ := test.request(
					http.MethodPost, "/auth/token",
					strings.NewReader(`{"email":"wrong@invalid.test","password":"wrong"}`))
				require.Nil(t, findCookie(resp, "_tokenKey"))
				if resp.StatusCode != http.StatusUnauthorized {
					require.Equal(t, http.StatusTooManyRequests, resp.StatusCode)
					hitRateLimiter = true
					break
				}
			}
			require.True(t, hitRateLimiter, "did not hit rate limiter")
		}
	})
}

func TestPayments(t *testing.T) {
	testplanet.Run(t, testplanet.Config{
		SatelliteCount: 1, StorageNodeCount: 0, UplinkCount: 1,
	}, func(t *testing.T, ctx *testcontext.Context, planet *testplanet.Planet) {
		test := newTest(t, ctx, planet)
		user := test.defaultUser()

		{ // Unauthorized
			for _, path := range []string{
				"/payments/cards",
				"/payments/account/balance",
				"/payments/billing-history",
				"/payments/account/charges?from=1619827200&to=1620844320",
			} {
				resp, body := test.request(http.MethodGet, path, nil)
				require.Contains(t, body, "unauthorized", path)
				require.Equal(t, http.StatusUnauthorized, resp.StatusCode, path)
			}
		}

		test.login(user.email, user.password)

		{ // Get_PaymentCards_EmptyReturn
			resp, body := test.request(http.MethodGet, "/payments/cards", nil)
			require.JSONEq(t, "[]", body)
			require.Equal(t, http.StatusOK, resp.StatusCode)
		}

		{ // Get_AccountBalance
			resp, body := test.request(http.MethodGet, "/payments/account/balance", nil)
			require.Contains(t, body, "freeCredits")
			require.Equal(t, http.StatusOK, resp.StatusCode)
		}

		{ // Get_BillingHistory
			resp, body := test.request(http.MethodGet, "/payments/billing-history", nil)
			require.JSONEq(t, "[]", body)
			require.Equal(t, http.StatusOK, resp.StatusCode)
		}

		{ // Get_AccountChargesByDateRange
			resp, body := test.request(http.MethodGet, "/payments/account/charges?from=1619827200&to=1620844320", nil)
			require.Contains(t, body, "egress")
			require.Equal(t, http.StatusOK, resp.StatusCode)
		}
	})
}

func TestBuckets(t *testing.T) {
	testplanet.Run(t, testplanet.Config{
		SatelliteCount: 1, StorageNodeCount: 0, UplinkCount: 1,
	}, func(t *testing.T, ctx *testcontext.Context, planet *testplanet.Planet) {
		test := newTest(t, ctx, planet)
		user := test.defaultUser()

		{ // Unauthorized
			for _, path := range []string{
				"/buckets/bucket-names?projectID=" + test.defaultProjectID(),
			} {
				resp, body := test.request(http.MethodGet, path, nil)
				require.Contains(t, body, "unauthorized", path)
				require.Equal(t, http.StatusUnauthorized, resp.StatusCode, path)
			}
		}

		test.login(user.email, user.password)

		{ // Get_BucketNamesByProjectId
			resp, body := test.request(http.MethodGet, "/buckets/bucket-names?projectID="+test.defaultProjectID(), nil)
			// TODO: this should be []
			require.JSONEq(t, "null", body)
			require.Equal(t, http.StatusOK, resp.StatusCode)
		}

		{ // get bucket usages
			resp, body := test.request(http.MethodPost, "/graphql",
				test.toJSON(map[string]interface{}{
					"variables": map[string]interface{}{
						"projectId": test.defaultProjectID(),
						"before":    "2021-05-12T18:32:30.533Z",
						"limit":     7,
						"search":    "",
						"page":      1,
					},
					"query": `
						query ($projectId: String!, $before: DateTime!, $limit: Int!, $search: String!, $page: Int!) {
							project(id: $projectId) {
								bucketUsages(before: $before, cursor: {limit: $limit, search: $search, page: $page}) {
									bucketUsages {
										bucketName
										storage
										egress
										objectCount
										segmentCount
										since
										before
										__typename
									}
									search
									limit
									offset
									pageCount
									currentPage
									totalCount
									__typename
								}
							__typename
							}
						}`}))
			require.Contains(t, body, "bucketUsagePage")
			require.Equal(t, http.StatusOK, resp.StatusCode)
		}
	})
}

func TestAPIKeys(t *testing.T) {
	testplanet.Run(t, testplanet.Config{
		SatelliteCount: 1, StorageNodeCount: 0, UplinkCount: 1,
	}, func(t *testing.T, ctx *testcontext.Context, planet *testplanet.Planet) {
		test := newTest(t, ctx, planet)
		user := test.defaultUser()
		test.login(user.email, user.password)

		{ // Post_GenerateApiKey
			resp, body := test.request(http.MethodPost, "/graphql",
				test.toJSON(map[string]interface{}{
					"variables": map[string]interface{}{
						"projectId": test.defaultProjectID(),
						"name":      user.email,
					},
					"query": `
						mutation ($projectId: String!, $name: String!) {
							createAPIKey(projectID: $projectId, name: $name) {
								key
								keyInfo {
									id
									name
									createdAt
									__typename
								}
								__typename
							}
						}`}))
			require.Contains(t, body, "createAPIKey")
			require.Equal(t, http.StatusOK, resp.StatusCode)
		}

		{ // Get_APIKeyInfoByProjectId
			resp, body := test.request(http.MethodPost, "/graphql",
				test.toJSON(map[string]interface{}{
					"variables": map[string]interface{}{
						"orderDirection": 1,
						"projectId":      test.defaultProjectID(),
						"limit":          6,
						"search":         ``,
						"page":           1,
						"order":          1,
					},
					"query": `
						query ($projectId: String!, $limit: Int!, $search: String!, $page: Int!, $order: Int!, $orderDirection: Int!) {
							project(id: $projectId) {
								apiKeys(cursor: {limit: $limit, search: $search, page: $page, order: $order, orderDirection: $orderDirection}) {
									apiKeys {
										id
										name
										createdAt
										__typename
									}
									search
									limit
									order
									pageCount
									currentPage
									totalCount
									__typename
								}
								__typename
							}
						}`}))
			require.Contains(t, body, "apiKeysPage")
			require.Equal(t, http.StatusOK, resp.StatusCode)
		}
	})
}

func TestProjects(t *testing.T) {
	testplanet.Run(t, testplanet.Config{
		SatelliteCount: 1, StorageNodeCount: 0, UplinkCount: 1,
	}, func(t *testing.T, ctx *testcontext.Context, planet *testplanet.Planet) {
		test := newTest(t, ctx, planet)
		user := test.defaultUser()
		user2 := test.registerUser("user@mail.test", "#$Rnkl12i3nkljfds")
		test.login(user.email, user.password)

		{ // Get_ProjectId
			resp, body := test.request(http.MethodPost, "/graphql",
				test.toJSON(map[string]interface{}{
					"query": `
						{
							myProjects {
								name
								id
								description
								createdAt
								ownerId
								__typename
							}
						}`}))
			require.Contains(t, body, test.defaultProjectID())
			require.Equal(t, http.StatusOK, resp.StatusCode)
		}

		{ // Get_ProjectInfo
			resp, body := test.request(http.MethodPost, "/graphql",
				test.toJSON(map[string]interface{}{
					"variables": map[string]interface{}{
						"projectId": test.defaultProjectID(),
						"before":    "2021-05-12T18:32:30.533Z",
						"limit":     7,
						"search":    "",
						"page":      1,
					},
					"query": `
						query ($projectId: String!, $before: DateTime!, $limit: Int!, $search: String!, $page: Int!) {
							project(id: $projectId) {
								bucketUsages(before: $before, cursor: {limit: $limit, search: $search, page: $page}) {
									bucketUsages {
										bucketName
										storage
										egress
										objectCount
										segmentCount
										since
										before
										__typename
									}
									search
									limit
									offset
									pageCount
									currentPage
									totalCount
									__typename
								}
							__typename
							}
						}`}))
			require.Contains(t, body, "bucketUsagePage")
			require.Equal(t, http.StatusOK, resp.StatusCode)
		}

		{ // Get_ProjectUsageLimitById
			resp, body := test.request(http.MethodGet, `/projects/`+test.defaultProjectID()+`/usage-limits`, nil)
			require.Equal(t, http.StatusOK, resp.StatusCode)
			require.Contains(t, body, "storageLimit")
		}

		{ // Get_OwnedProjects
			resp, body := test.request(http.MethodPost, "/graphql",
				test.toJSON(map[string]interface{}{
					"variables": map[string]interface{}{
						"limit": 7,
						"page":  1,
					},
					"query": `
						query ($limit: Int!, $page: Int!) {
							ownedProjects(cursor: {limit: $limit, page: $page}) {
								projects {
									id
									name
									ownerId
									description
									createdAt
									memberCount
									__typename
								}
								limit
								offset
								pageCount
								currentPage
								totalCount
								__typename
							}
						}`}))
			require.Contains(t, body, "projectsPage")
			require.Equal(t, http.StatusOK, resp.StatusCode)
		}

		{ // Get_ProjectMembersByProjectId
			resp, body := test.request(http.MethodPost, "/graphql",
				test.toJSON(map[string]interface{}{
					"variables": map[string]interface{}{
						`orderDirection`: 1,
						`projectId`:      test.defaultProjectID(),
						`limit`:          6,
						`search`:         ``,
						`page`:           1,
						`order`:          1,
					},
					"query": `
						query ($projectId: String!, $limit: Int!, $search: String!, $page: Int!, $order: Int!, $orderDirection: Int!) {
							project(id: $projectId) {
								members(cursor: {limit: $limit, search: $search, page: $page, order: $order, orderDirection: $orderDirection}) {
									projectMembers {
										user {
											id
											fullName
											shortName
											email
											__typename
										}
										joinedAt
										__typename
									}
									search
									limit
									order
									pageCount
									currentPage
									totalCount
									__typename
								}
								__typename
							}
						}`}))
			require.Contains(t, body, "projectMembersPage")
			require.Equal(t, http.StatusOK, resp.StatusCode)
		}

		{ // Post_AddUserToProject
			resp, body := test.request(http.MethodPost, "/graphql",
				test.toJSON(map[string]interface{}{
					"variables": map[string]interface{}{
						"projectId": test.defaultProjectID(),
						"emails":    []string{user2.email},
					},
					"query": `
						mutation ($projectId: String!, $emails: [String!]!) {
							addProjectMembers(projectID: $projectId, email: $emails) {
								id
								__typename
							}
						}`}))
			require.Contains(t, body, "addProjectMembers")
			require.Equal(t, http.StatusOK, resp.StatusCode)
		}

		{ // Post_RemoveUserFromProject
			resp, body := test.request(http.MethodPost, "/graphql",
				test.toJSON(map[string]interface{}{
					"variables": map[string]interface{}{
						"projectId": test.defaultProjectID(),
						"emails":    []string{user2.email},
					},
					"query": `
						mutation ($projectId: String!, $emails: [String!]!) {
							deleteProjectMembers(projectID: $projectId, email: $emails) {
								id
								__typename
							}
						}`}))
			require.Contains(t, body, "deleteProjectMembers")
			require.Equal(t, http.StatusOK, resp.StatusCode)
		}

		{ // Post_AddMultipleUsersToProjectWhere1UserIsInvalid
			resp, body := test.request(http.MethodPost, "/graphql",
				test.toJSON(map[string]interface{}{
					"variables": map[string]interface{}{
						"projectId": test.defaultProjectID(),
						"emails":    []string{user2.email, "invalid@mail.test"},
					},
					"query": `
						mutation ($projectId: String!, $emails: [String!]!) {
							addProjectMembers(projectID: $projectId, email: $emails) {
								id
								__typename
							}
						}`}))
			require.Contains(t, body, "There is no account on this Satellite for the user(s) you have entered")
			require.Equal(t, http.StatusOK, resp.StatusCode)
		}

		{ // Post_AddMultipleUsersToProjectWhereUserIsAlreadyAMember
			resp, body := test.request(http.MethodPost, "/graphql",
				test.toJSON(map[string]interface{}{
					"variables": map[string]interface{}{
						"projectId": test.defaultProjectID(),
						"emails":    []string{user2.email, user.email},
					},
					"query": `
						mutation ($projectId: String!, $emails: [String!]!) {
							addProjectMembers(projectID: $projectId, email: $emails) {
								id
								__typename
							}
						}`}))
			require.Contains(t, body, "error")
			// TODO: this should return a better error
			require.Equal(t, http.StatusInternalServerError, resp.StatusCode)
		}

		{ // Post_ProjectRenameInvalid
			resp, body := test.request(http.MethodPost, "/graphql",
				test.toJSON(map[string]interface{}{
					"variables": map[string]interface{}{
						`projectId`:   `e4a929a6-cc69-4920-ad06-c84f3c943928`,
						`name`:        `My Second Project`,
						`description`: `___`,
					},
					"query": `
						mutation ($projectId: String!, $name: String!, $description: String!) {
							updateProject(id: $projectId, projectFields: {name: $name, description: $description}, projectLimits: {storageLimit: "1000", bandwidthLimit: "1000"}) {
								name
								__typename
							}
						}`}))
			require.Contains(t, body, "error")
			// TODO: this should return a better error
			require.Equal(t, http.StatusInternalServerError, resp.StatusCode)
		}

		{ // Post_ProjectRename
			resp, body := test.request(http.MethodPost, "/graphql",
				test.toJSON(map[string]interface{}{
					"variables": map[string]interface{}{
						`projectId`:   test.defaultProjectID(),
						`name`:        `Test`,
						`description`: `Misc`,
					},
					"query": `
						mutation ($projectId: String!, $name: String!, $description: String!) {
							updateProject(id: $projectId, projectFields: {name: $name, description: $description}, projectLimits: {storageLimit: "1000", bandwidthLimit: "1000"}) {
								name
								__typename
							}
						}`}))
			require.Contains(t, body, "updateProject")
			require.Equal(t, http.StatusOK, resp.StatusCode)
		}
	})
}

func TestWrongUser(t *testing.T) {
	testplanet.Run(t, testplanet.Config{
		SatelliteCount: 1, StorageNodeCount: 0, UplinkCount: 1,
	}, func(t *testing.T, ctx *testcontext.Context, planet *testplanet.Planet) {
		test := newTest(t, ctx, planet)
		user := test.defaultUser()
		_ = user
		user2 := test.registerUser("user@mail.test", "#$Rnkl12i3nkljfds")
		test.login(user2.email, user2.password)

		{ // Get_ProjectInfo
			resp, body := test.request(http.MethodPost, "/graphql",
				test.toJSON(map[string]interface{}{
					"variables": map[string]interface{}{
						"projectId": test.defaultProjectID(),
						"before":    "2021-05-12T18:32:30.533Z",
						"limit":     7,
						"search":    "",
						"page":      1,
					},
					"query": `
						query ($projectId: String!, $before: DateTime!, $limit: Int!, $search: String!, $page: Int!) {
							project(id: $projectId) {
								bucketUsages(before: $before, cursor: {limit: $limit, search: $search, page: $page}) {
									bucketUsages {
										bucketName
										storage
										egress
										objectCount
										segmentCount
										since
										before
										__typename
									}
									search
									limit
									offset
									pageCount
									currentPage
									totalCount
									__typename
								}
							__typename
							}
						}`}))
			require.Contains(t, body, "not authorized")
			// TODO: wrong error code
			require.Equal(t, http.StatusInternalServerError, resp.StatusCode)
		}

		{ // Get_ProjectUsageLimitById
			resp, body := test.request(http.MethodGet, `/projects/`+test.defaultProjectID()+`/usage-limits`, nil)
			require.Contains(t, body, "not authorized")
			// TODO: wrong error code
			require.Equal(t, http.StatusInternalServerError, resp.StatusCode)
		}

		{ // Get_ProjectMembersByProjectId
			resp, body := test.request(http.MethodPost, "/graphql",
				test.toJSON(map[string]interface{}{
					"variables": map[string]interface{}{
						`orderDirection`: 1,
						`projectId`:      test.defaultProjectID(),
						`limit`:          6,
						`search`:         ``,
						`page`:           1,
						`order`:          1,
					},
					"query": `
						query ($projectId: String!, $limit: Int!, $search: String!, $page: Int!, $order: Int!, $orderDirection: Int!) {
							project(id: $projectId) {
								members(cursor: {limit: $limit, search: $search, page: $page, order: $order, orderDirection: $orderDirection}) {
									projectMembers {
										user {
											id
											fullName
											shortName
											email
											__typename
										}
										joinedAt
										__typename
									}
									search
									limit
									order
									pageCount
									currentPage
									totalCount
									__typename
								}
								__typename
							}
						}`}))
			require.Contains(t, body, "not authorized")
			// TODO: wrong error code
			require.Equal(t, http.StatusInternalServerError, resp.StatusCode)
		}

		{ // Post_AddUserToProject
			resp, body := test.request(http.MethodPost, "/graphql",
				test.toJSON(map[string]interface{}{
					"variables": map[string]interface{}{
						"projectId": test.defaultProjectID(),
						"emails":    []string{user2.email},
					},
					"query": `
						mutation ($projectId: String!, $emails: [String!]!) {
							addProjectMembers(projectID: $projectId, email: $emails) {
								id
								__typename
							}
						}`}))
			require.Contains(t, body, "not authorized")
			// TODO: wrong error code
			require.Equal(t, http.StatusInternalServerError, resp.StatusCode)
		}

		{ // Post_RemoveUserFromProject
			resp, body := test.request(http.MethodPost, "/graphql",
				test.toJSON(map[string]interface{}{
					"variables": map[string]interface{}{
						"projectId": test.defaultProjectID(),
						"emails":    []string{user2.email},
					},
					"query": `
						mutation ($projectId: String!, $emails: [String!]!) {
							deleteProjectMembers(projectID: $projectId, email: $emails) {
								id
								__typename
							}
						}`}))
			require.Contains(t, body, "not authorized")
			// TODO: wrong error code
			require.Equal(t, http.StatusInternalServerError, resp.StatusCode)
		}

		{ // Post_ProjectRename
			resp, body := test.request(http.MethodPost, "/graphql",
				test.toJSON(map[string]interface{}{
					"variables": map[string]interface{}{
						`projectId`:   test.defaultProjectID(),
						`name`:        `Test`,
						`description`: `Misc`,
					},
					"query": `
						mutation ($projectId: String!, $name: String!, $description: String!) {
							updateProject(id: $projectId, projectFields: {name: $name, description: $description}, projectLimits: {storageLimit: "1000", bandwidthLimit: "1000"}) {
								name
								__typename
							}
						}`}))
			require.Contains(t, body, "not authorized")
			// TODO: wrong error code
			require.Equal(t, http.StatusInternalServerError, resp.StatusCode)
		}

		{ // get bucket usages
			resp, body := test.request(http.MethodPost, "/graphql",
				test.toJSON(map[string]interface{}{
					"variables": map[string]interface{}{
						"projectId": test.defaultProjectID(),
						"before":    "2021-05-12T18:32:30.533Z",
						"limit":     7,
						"search":    "",
						"page":      1,
					},
					"query": `
						query ($projectId: String!, $before: DateTime!, $limit: Int!, $search: String!, $page: Int!) {
							project(id: $projectId) {
								bucketUsages(before: $before, cursor: {limit: $limit, search: $search, page: $page}) {
									bucketUsages {
										bucketName
										storage
										egress
										objectCount
										segmentCount
										since
										before
										__typename
									}
									search
									limit
									offset
									pageCount
									currentPage
									totalCount
									__typename
								}
							__typename
							}
						}`}))
			require.Contains(t, body, "not authorized")
			// TODO: wrong error code
			require.Equal(t, http.StatusInternalServerError, resp.StatusCode)
		}
	})
}

type test struct {
	t      *testing.T
	ctx    *testcontext.Context
	planet *testplanet.Planet
	client *http.Client
}

func newTest(t *testing.T, ctx *testcontext.Context, planet *testplanet.Planet) test {
	jar, err := cookiejar.New(nil)
	require.NoError(t, err)
	return test{t: t, ctx: ctx, planet: planet, client: &http.Client{Jar: jar}}
}

type registeredUser struct {
	id       string
	email    string
	password string
}

func (test *test) request(method string, path string, data io.Reader) (resp Response, body string) {
	req, err := http.NewRequestWithContext(test.ctx, method, test.url(path), data)
	require.NoError(test.t, err)
	req.Header = map[string][]string{
		"sec-ch-ua":        {`" Not A;Brand";v="99"`, `"Chromium";v="90"`, `"Google Chrome";v="90"`},
		"sec-ch-ua-mobile": {"?0"},
		"User-Agent":       {"Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/90.0.4430.93 Safari/537.36"},
		"Content-Type":     {"application/json"},
		"Accept":           {"*/*"},
	}
	return test.do(req)
}

// Response is a wrapper for http.Request to prevent false-positive with bodyclose check.
type Response struct{ *http.Response }

func (test *test) do(req *http.Request) (_ Response, body string) {
	resp, err := test.client.Do(req)
	require.NoError(test.t, err)

	data, err := ioutil.ReadAll(resp.Body)
	require.NoError(test.t, err)
	require.NoError(test.t, resp.Body.Close())

	return Response{resp}, string(data)
}

func (test *test) url(suffix string) string {
	return test.planet.Satellites[0].ConsoleURL() + "/api/v0" + suffix
}

func (test *test) toJSON(v interface{}) io.Reader {
	data, err := json.Marshal(v)
	require.NoError(test.t, err)
	return strings.NewReader(string(data))
}

func (test *test) defaultUser() registeredUser {
	user := test.planet.Uplinks[0].User[test.planet.Satellites[0].ID()]
	return registeredUser{
		email:    user.Email,
		password: user.Password,
	}
}

func (test *test) defaultProjectID() string { return test.planet.Uplinks[0].Projects[0].ID.String() }

func (test *test) login(email, password string) Response {
	resp, body := test.request(
		http.MethodPost, "/auth/token",
		test.toJSON(map[string]string{
			"email":    email,
			"password": password,
		}))
	cookie := findCookie(resp, "_tokenKey")
	require.NotNil(test.t, cookie)

	var rawToken string
	require.NoError(test.t, json.Unmarshal([]byte(body), &rawToken))
	require.Equal(test.t, http.StatusOK, resp.StatusCode)
	require.Equal(test.t, rawToken, cookie.Value)

	return resp
}

func (test *test) registerUser(email, password string) registeredUser {
	resp, body := test.request(
		http.MethodPost, "/auth/register",
		test.toJSON(map[string]interface{}{
			"secret":           "",
			"password":         password,
			"fullName":         "Chester Cheeto",
			"shortName":        "",
			"email":            email,
			"partner":          "",
			"partnerId":        "",
			"isProfessional":   false,
			"position":         "",
			"companyName":      "",
			"employeeCount":    "",
			"haveSalesContact": false,
		}))

	require.Equal(test.t, http.StatusOK, resp.StatusCode)
	require.NotEmpty(test.t, body)

	time.Sleep(time.Second) // TODO: hack-fix, register activates account asynchronously

	return registeredUser{
		id:       body,
		email:    email,
		password: password,
	}
}

func findCookie(response Response, name string) *http.Cookie {
	for _, c := range response.Cookies() {
		if c.Name == name {
			return c
		}
	}
	return nil
}
