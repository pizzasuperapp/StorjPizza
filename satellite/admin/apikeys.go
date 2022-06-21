// Copyright (C) 2020 Storj Labs, Inc.
// See LICENSE for copying information.

package admin

import (
	"database/sql"
	"encoding/json"
	"errors"
	"io/ioutil"
	"net/http"

	"github.com/gorilla/mux"

	"storj.io/common/macaroon"
	"storj.io/common/uuid"
	"storj.io/storj/satellite/console"
)

func (server *Server) addAPIKey(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	vars := mux.Vars(r)
	projectUUIDString, ok := vars["project"]
	if !ok {
		sendJSONError(w, "project-uuid missing",
			"", http.StatusBadRequest)
		return
	}

	projectUUID, err := uuid.FromString(projectUUIDString)
	if err != nil {
		sendJSONError(w, "invalid project-uuid",
			err.Error(), http.StatusBadRequest)
		return
	}

	body, err := ioutil.ReadAll(r.Body)
	if err != nil {
		sendJSONError(w, "failed to read body",
			err.Error(), http.StatusInternalServerError)
		return
	}

	var input struct {
		PartnerID uuid.UUID `json:"partnerId"`
		Name      string    `json:"name"`
	}

	err = json.Unmarshal(body, &input)
	if err != nil {
		sendJSONError(w, "failed to unmarshal request",
			err.Error(), http.StatusBadRequest)
		return
	}

	if input.Name == "" {
		sendJSONError(w, "Name is not set",
			"", http.StatusBadRequest)
		return
	}

	_, err = server.db.Console().APIKeys().GetByNameAndProjectID(ctx, input.Name, projectUUID)
	if err == nil {
		sendJSONError(w, "api-key with given name already exists",
			"", http.StatusConflict)
		return
	}

	secret, err := macaroon.NewSecret()
	if err != nil {
		sendJSONError(w, "could not create macaroon secret",
			err.Error(), http.StatusInternalServerError)
		return
	}

	key, err := macaroon.NewAPIKey(secret)
	if err != nil {
		sendJSONError(w, "could not create api-key",
			err.Error(), http.StatusInternalServerError)
		return
	}

	apikey := console.APIKeyInfo{
		Name:      input.Name,
		ProjectID: projectUUID,
		Secret:    secret,
		PartnerID: input.PartnerID,
	}

	_, err = server.db.Console().APIKeys().Create(ctx, key.Head(), apikey)
	if err != nil {
		sendJSONError(w, "unable to add api-key to database",
			err.Error(), http.StatusInternalServerError)
		return
	}

	var output struct {
		APIKey string `json:"apikey"`
	}

	output.APIKey = key.Serialize()
	data, err := json.Marshal(output)
	if err != nil {
		sendJSONError(w, "json encoding failed",
			err.Error(), http.StatusInternalServerError)
		return
	}

	sendJSONData(w, http.StatusOK, data)
}

func (server *Server) deleteAPIKey(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	vars := mux.Vars(r)
	apikeyString, ok := vars["apikey"]
	if !ok {
		sendJSONError(w, "apikey missing",
			"", http.StatusBadRequest)
		return
	}

	apikey, err := macaroon.ParseAPIKey(apikeyString)
	if err != nil {
		sendJSONError(w, "invalid apikey format",
			err.Error(), http.StatusBadRequest)
		return
	}

	info, err := server.db.Console().APIKeys().GetByHead(ctx, apikey.Head())
	if errors.Is(err, sql.ErrNoRows) {
		sendJSONError(w, "API key does not exist",
			"", http.StatusNotFound)
		return
	}
	if err != nil {
		sendJSONError(w, "could not get apikey id",
			err.Error(), http.StatusInternalServerError)
		return
	}

	err = server.db.Console().APIKeys().Delete(ctx, info.ID)
	if err != nil {
		sendJSONError(w, "unable to delete apikey",
			err.Error(), http.StatusInternalServerError)
		return
	}
}

func (server *Server) deleteAPIKeyByName(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	vars := mux.Vars(r)
	projectUUIDString, ok := vars["project"]
	if !ok {
		sendJSONError(w, "project-uuid missing",
			"", http.StatusBadRequest)
		return
	}

	projectUUID, err := uuid.FromString(projectUUIDString)
	if err != nil {
		sendJSONError(w, "invalid project-uuid",
			err.Error(), http.StatusBadRequest)
		return
	}

	apikeyName, ok := vars["name"]
	if !ok {
		sendJSONError(w, "apikey name missing",
			"", http.StatusBadRequest)
		return
	}

	info, err := server.db.Console().APIKeys().GetByNameAndProjectID(ctx, apikeyName, projectUUID)
	if errors.Is(err, sql.ErrNoRows) {
		sendJSONError(w, "API key with specified name does not exist",
			"", http.StatusNotFound)
		return
	}
	if err != nil {
		sendJSONError(w, "could not get apikey id",
			err.Error(), http.StatusInternalServerError)
		return
	}

	err = server.db.Console().APIKeys().Delete(ctx, info.ID)
	if err != nil {
		sendJSONError(w, "unable to delete apikey",
			err.Error(), http.StatusInternalServerError)
		return
	}
}

func (server *Server) listAPIKeys(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	vars := mux.Vars(r)
	projectUUIDString, ok := vars["project"]
	if !ok {
		sendJSONError(w, "project-uuid missing",
			"", http.StatusBadRequest)
		return
	}

	projectUUID, err := uuid.FromString(projectUUIDString)
	if err != nil {
		sendJSONError(w, "invalid project-uuid",
			err.Error(), http.StatusBadRequest)
		return
	}

	const apiKeysPerPage = 50 // This is the current maximum API Keys per page.
	var apiKeys []console.APIKeyInfo
	for i := uint(1); true; i++ {
		page, err := server.db.Console().APIKeys().GetPagedByProjectID(
			ctx, projectUUID, console.APIKeyCursor{
				Limit:          apiKeysPerPage,
				Page:           i,
				Order:          console.KeyName,
				OrderDirection: console.Ascending,
			},
		)
		if err != nil {
			sendJSONError(w, "failed retrieving a cursor page of API Keys list",
				err.Error(), http.StatusInternalServerError,
			)
			return
		}

		apiKeys = append(apiKeys, page.APIKeys...)
		if len(page.APIKeys) < apiKeysPerPage {
			break
		}
	}

	var data []byte
	if len(apiKeys) == 0 {
		data = []byte("[]")
	} else {
		data, err = json.Marshal(apiKeys)
		if err != nil {
			sendJSONError(w, "json encoding failed",
				err.Error(), http.StatusInternalServerError)
			return
		}
	}

	sendJSONData(w, http.StatusOK, data)
}
