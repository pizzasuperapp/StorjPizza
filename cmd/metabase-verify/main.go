// Copyright (C) 2021 Storj Labs, Inc.
// See LICENSE for copying information.

package main

import (
	"time"

	"github.com/spf13/cobra"
	"github.com/zeebo/errs"
	"go.uber.org/zap"

	"storj.io/private/process"
	"storj.io/storj/cmd/metabase-verify/verify"
	"storj.io/storj/satellite/metabase"
)

// Error is the default error class for the package.
var Error = errs.Class("metabase-verify")

func main() {
	log := zap.L()

	root := &cobra.Command{
		Use: "metainfo-loop-verify",
	}
	IncludeProfiling(root)

	root.AddCommand(VerifyCommand(log))

	process.Exec(root)
}

// VerifyCommand creates command for running verifications.
func VerifyCommand(log *zap.Logger) *cobra.Command {
	var metabaseDB string
	var ignoreVersionMismatch bool
	var verifyConfig verify.Config

	cmd := &cobra.Command{
		Use:   "run",
		Short: "run metabase verification",
	}

	flag := cmd.Flags()

	flag.StringVar(&metabaseDB, "metabasedb", "", "connection URL for MetabaseDB")
	_ = cmd.MarkFlagRequired("metabasedb")

	flag.BoolVar(&ignoreVersionMismatch, "ignore-version-mismatch", false, "ignore version mismatch")

	flag.DurationVar(&verifyConfig.Loop.CoalesceDuration, "loop.coalesce-duration", 5*time.Second, "how long to wait for new observers before starting iteration")
	flag.Float64Var(&verifyConfig.Loop.RateLimit, "loop.rate-limit", 0, "rate limit (default is 0 which is unlimited segments per second)")
	flag.IntVar(&verifyConfig.Loop.ListLimit, "loop.list-limit", 2500, "how many items to query in a batch")

	flag.Int64Var(&verifyConfig.ProgressPrintFrequency, "progress-frequency", 1000000, "how often should we print progress (every object)")

	cmd.RunE = func(cmd *cobra.Command, args []string) error {
		ctx, cancel := process.Ctx(cmd)
		defer cancel()

		mdb, err := metabase.Open(ctx, log.Named("mdb"), metabaseDB, metabase.Config{ApplicationName: "metabase-verify"})
		if err != nil {
			return Error.Wrap(err)
		}
		defer func() { _ = mdb.Close() }()

		versionErr := mdb.CheckVersion(ctx)
		if versionErr != nil {
			log.Error("versions skewed", zap.Error(versionErr))
			if !ignoreVersionMismatch {
				return Error.Wrap(versionErr)
			}
		}

		verify := verify.New(log, mdb, verifyConfig)
		if err := verify.RunOnce(ctx); err != nil {
			return Error.Wrap(err)
		}
		return nil
	}

	return cmd
}
