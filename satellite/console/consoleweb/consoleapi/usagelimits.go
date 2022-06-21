// Copyright (C) 2021 Storj Labs, Inc.
// See LICENSE for copying information.

package consoleapi

import (
	"encoding/json"
	"net/http"
	"strconv"
	"time"

	"github.com/gorilla/mux"
	"github.com/zeebo/errs"
	"go.uber.org/zap"

	"storj.io/common/uuid"
	"storj.io/storj/satellite/accounting"
	"storj.io/storj/satellite/console"
)

var (
	// ErrUsageLimitsAPI - console usage and limits api error type.
	ErrUsageLimitsAPI = errs.Class("console usage and limits")
)

// UsageLimits is an api controller that exposes all usage and limits related functionality.
type UsageLimits struct {
	log     *zap.Logger
	service *console.Service
}

// NewUsageLimits is a constructor for api usage and limits controller.
func NewUsageLimits(log *zap.Logger, service *console.Service) *UsageLimits {
	return &UsageLimits{
		log:     log,
		service: service,
	}
}

// ProjectUsageLimits returns usage and limits by project ID.
func (ul *UsageLimits) ProjectUsageLimits(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	var err error
	defer mon.Task()(&ctx)(&err)

	w.Header().Set("Content-Type", "application/json")

	var ok bool
	var idParam string

	if idParam, ok = mux.Vars(r)["id"]; !ok {
		ul.serveJSONError(w, http.StatusBadRequest, errs.New("missing project id route param"))
		return
	}

	projectID, err := uuid.FromString(idParam)
	if err != nil {
		ul.serveJSONError(w, http.StatusBadRequest, errs.New("invalid project id: %v", err))
		return
	}

	usageLimits, err := ul.service.GetProjectUsageLimits(ctx, projectID)
	if err != nil {
		switch {
		case console.ErrUnauthorized.Has(err):
			ul.serveJSONError(w, http.StatusUnauthorized, err)
			return
		case accounting.ErrInvalidArgument.Has(err):
			ul.serveJSONError(w, http.StatusBadRequest, err)
			return
		default:
			ul.serveJSONError(w, http.StatusInternalServerError, err)
			return
		}
	}

	err = json.NewEncoder(w).Encode(usageLimits)
	if err != nil {
		ul.log.Error("error encoding project usage limits", zap.Error(ErrUsageLimitsAPI.Wrap(err)))
	}
}

// TotalUsageLimits returns total usage and limits for all the projects that user owns.
func (ul *UsageLimits) TotalUsageLimits(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	var err error
	defer mon.Task()(&ctx)(&err)

	usageLimits, err := ul.service.GetTotalUsageLimits(ctx)
	if err != nil {
		if console.ErrUnauthorized.Has(err) {
			ul.serveJSONError(w, http.StatusUnauthorized, err)
			return
		}

		ul.serveJSONError(w, http.StatusInternalServerError, err)
		return
	}

	err = json.NewEncoder(w).Encode(usageLimits)
	if err != nil {
		ul.log.Error("error encoding project usage limits", zap.Error(ErrUsageLimitsAPI.Wrap(err)))
	}
}

// DailyUsage returns daily usage by project ID.
func (ul *UsageLimits) DailyUsage(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	var err error
	defer mon.Task()(&ctx)(&err)

	var ok bool
	var idParam string

	if idParam, ok = mux.Vars(r)["id"]; !ok {
		ul.serveJSONError(w, http.StatusBadRequest, errs.New("missing project id route param"))
		return
	}
	projectID, err := uuid.FromString(idParam)
	if err != nil {
		ul.serveJSONError(w, http.StatusBadRequest, errs.New("invalid project id: %v", err))
		return
	}

	sinceStamp, err := strconv.ParseInt(r.URL.Query().Get("from"), 10, 64)
	if err != nil {
		ul.serveJSONError(w, http.StatusBadRequest, err)
		return
	}
	beforeStamp, err := strconv.ParseInt(r.URL.Query().Get("to"), 10, 64)
	if err != nil {
		ul.serveJSONError(w, http.StatusBadRequest, err)
		return
	}

	since := time.Unix(sinceStamp, 0)
	before := time.Unix(beforeStamp, 0)

	dailyUsage, err := ul.service.GetDailyProjectUsage(ctx, projectID, since, before)
	if err != nil {
		if console.ErrUnauthorized.Has(err) {
			ul.serveJSONError(w, http.StatusUnauthorized, err)
			return
		}

		ul.serveJSONError(w, http.StatusInternalServerError, err)
		return
	}

	err = json.NewEncoder(w).Encode(dailyUsage)
	if err != nil {
		ul.log.Error("error encoding daily project usage", zap.Error(ErrUsageLimitsAPI.Wrap(err)))
	}
}

// serveJSONError writes JSON error to response output stream.
func (ul *UsageLimits) serveJSONError(w http.ResponseWriter, status int, err error) {
	serveJSONError(ul.log, w, status, err)
}
