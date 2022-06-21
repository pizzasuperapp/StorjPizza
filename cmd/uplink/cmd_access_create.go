// Copyright (C) 2021 Storj Labs, Inc.
// See LICENSE for copying information.

package main

import (
	"fmt"
	"io/ioutil"
	"strconv"
	"strings"

	"github.com/zeebo/clingy"
	"github.com/zeebo/errs"

	"storj.io/storj/cmd/uplink/ulext"
)

type cmdAccessCreate struct {
	ex ulext.External
	am accessMaker

	passphraseStdin bool
	satelliteAddr   string
	apiKey          string
	importAs        string
	exportTo        string
}

func newCmdAccessCreate(ex ulext.External) *cmdAccessCreate {
	return &cmdAccessCreate{ex: ex}
}

func (c *cmdAccessCreate) Setup(params clingy.Parameters) {
	c.passphraseStdin = params.Flag("passphrase-stdin", "If set, the passphrase is read from stdin, and all other values must be provided.", false,
		clingy.Transform(strconv.ParseBool),
		clingy.Boolean,
	).(bool)

	c.satelliteAddr = params.Flag("satellite-address", "Satellite address from satellite UI (prompted if unspecified)", "").(string)
	c.apiKey = params.Flag("api-key", "API key from satellite UI (prompted if unspecified)", "").(string)
	c.importAs = params.Flag("import-as", "Import the access as this name", "").(string)
	c.exportTo = params.Flag("export-to", "Export the access to this file path", "").(string)

	params.Break()
	c.am.Setup(params, c.ex)
}

func (c *cmdAccessCreate) Execute(ctx clingy.Context) (err error) {
	if c.satelliteAddr == "" {
		if c.passphraseStdin {
			return errs.New("Must specify the satellite address as a flag when passphrase-stdin is set.")
		}
		c.satelliteAddr, err = c.ex.PromptInput(ctx, "Satellite address:")
		if err != nil {
			return errs.Wrap(err)
		}
	}

	if c.apiKey == "" {
		if c.passphraseStdin {
			return errs.New("Must specify the api key as a flag when passphrase-stdin is set.")
		}
		c.apiKey, err = c.ex.PromptInput(ctx, "API key:")
		if err != nil {
			return errs.Wrap(err)
		}
	}

	var passphrase string
	if c.passphraseStdin {
		stdinData, err := ioutil.ReadAll(ctx.Stdin())
		if err != nil {
			return errs.Wrap(err)
		}
		passphrase = strings.TrimRight(string(stdinData), "\r\n")
	} else {
		passphrase, err = c.ex.PromptSecret(ctx, "Passphrase:")
		if err != nil {
			return errs.Wrap(err)
		}
	}
	if passphrase == "" {
		return errs.New("Encryption passphrase must be non-empty")
	}

	access, err := c.ex.RequestAccess(ctx, c.satelliteAddr, c.apiKey, passphrase)
	if err != nil {
		return errs.Wrap(err)
	}

	access, err = c.am.Execute(ctx, c.importAs, access)
	if err != nil {
		return errs.Wrap(err)
	}

	if c.exportTo != "" {
		return c.ex.ExportAccess(ctx, access, c.exportTo)
	}

	if c.importAs != "" {
		return nil
	}

	serialized, err := access.Serialize()
	if err != nil {
		return errs.Wrap(err)
	}

	fmt.Fprintln(ctx, serialized)

	return nil
}
