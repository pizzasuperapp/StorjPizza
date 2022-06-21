// Copyright (C) 2019 Storj Labs, Inc.
// See LICENSE for copying information.

package rewards

import (
	"context"
	"sort"
	"strings"

	"github.com/zeebo/errs"
)

// PartnersStaticDB implements partner lookup based on a static definition.
//
// architecture: Database Implementation
type PartnersStaticDB struct {
	list        *PartnerList
	byName      map[string]PartnerInfo
	byID        map[string]PartnerInfo
	byUserAgent map[string]PartnerInfo
}

var _ PartnersDB = (*PartnersStaticDB)(nil)

// NewPartnersStaticDB creates a new PartnersStaticDB.
func NewPartnersStaticDB(list *PartnerList) (*PartnersStaticDB, error) {
	db := &PartnersStaticDB{
		list:        list,
		byName:      map[string]PartnerInfo{},
		byID:        map[string]PartnerInfo{},
		byUserAgent: map[string]PartnerInfo{},
	}

	sort.Slice(list.Partners, func(i, k int) bool {
		return list.Partners[i].Name < list.Partners[k].Name
	})

	var errg errs.Group
	for _, p := range list.Partners {
		name := strings.ToLower(p.Name)
		if _, exists := db.byName[name]; exists {
			errg.Add(ErrPartners.New("name %q already exists", p.Name))
		} else {
			db.byName[name] = p
		}

		if _, exists := db.byID[p.ID]; exists {
			errg.Add(ErrPartners.New("id %q already exists", p.ID))
		} else {
			db.byID[p.ID] = p
		}

		useragent := CanonicalUserAgentProduct(p.UserAgent())
		if _, exists := db.byUserAgent[useragent]; exists {
			errg.Add(ErrPartners.New("user agent %q already exists", useragent))
		} else {
			db.byUserAgent[useragent] = p
		}
	}

	return db, errg.Err()
}

// All returns all partners.
func (db *PartnersStaticDB) All(ctx context.Context) ([]PartnerInfo, error) {
	return append([]PartnerInfo{}, db.list.Partners...), nil
}

// ByName returns partner definitions for a given name. Name is case insensitive.
func (db *PartnersStaticDB) ByName(ctx context.Context, name string) (PartnerInfo, error) {
	partner, ok := db.byName[strings.ToLower(name)]
	if !ok {
		return PartnerInfo{}, ErrPartnerNotExist.New("%q", name)
	}
	return partner, nil
}

// ByID returns partner definition corresponding to an id.
func (db *PartnersStaticDB) ByID(ctx context.Context, id string) (PartnerInfo, error) {
	partner, ok := db.byID[id]
	if !ok {
		return PartnerInfo{}, ErrPartnerNotExist.New("%q", id)
	}
	return partner, nil
}

// ByUserAgent returns partner definition corresponding to an user agent product string.
func (db *PartnersStaticDB) ByUserAgent(ctx context.Context, agent string) (PartnerInfo, error) {
	partner, ok := db.byUserAgent[CanonicalUserAgentProduct(agent)]
	if !ok {
		return PartnerInfo{}, ErrPartnerNotExist.New("%q", agent)
	}
	return partner, nil
}
