// Copyright (C) 2019 Storj Labs, Inc.
// See LICENSE for copying information.

package main

import (
	"context"
	"encoding/base64"
	"encoding/csv"
	"flag"
	"fmt"
	"os"
	"strconv"

	"github.com/spf13/cobra"
	"github.com/zeebo/errs"

	"storj.io/common/identity"
	"storj.io/common/rpc"
	"storj.io/common/storj"
	"storj.io/private/process"
	_ "storj.io/storj/private/version" // This attaches version information during release builds.
	"storj.io/storj/satellite/internalpb"
	"storj.io/uplink/private/eestream"
)

var (
	// Addr is the address of peer from command flags.
	Addr = flag.String("address", "127.0.0.1:7778", "address of peer to inspect")

	// IdentityPath is the path to the identity the inspector should use for network communication.
	IdentityPath = flag.String("identity-path", "", "path to the identity certificate for use on the network")

	// CSVPath is the csv path where command output is written.
	CSVPath string

	// ErrInspectorDial throws when there are errors dialing the inspector server.
	ErrInspectorDial = errs.Class("dialing inspector server")

	// ErrRequest is for request errors after dialing.
	ErrRequest = errs.Class("processing request")

	// ErrIdentity is for errors during identity creation for this CLI.
	ErrIdentity = errs.Class("creating identity")

	// ErrArgs throws when there are errors with CLI args.
	ErrArgs = errs.Class("invalid CLI args")

	// Commander CLI.
	rootCmd = &cobra.Command{
		Use:   "inspector",
		Short: "CLI for interacting with Storj network",
	}
	statsCmd = &cobra.Command{
		Use:   "statdb",
		Short: "commands for statdb",
	}
	healthCmd = &cobra.Command{
		Use:   "health",
		Short: "commands for querying health of a stored data",
	}
	objectHealthCmd = &cobra.Command{
		Use:   "object <project-id> <bucket> <encrypted-path>",
		Short: "Get stats about an object's health",
		Args:  cobra.MinimumNArgs(3),
		RunE:  ObjectHealth,
	}
	segmentHealthCmd = &cobra.Command{
		Use:   "segment <project-id> <segment-index> <bucket> <encrypted-path>",
		Short: "Get stats about a segment's health",
		Args:  cobra.MinimumNArgs(4),
		RunE:  SegmentHealth,
	}
)

// Inspector gives access to overlay.
type Inspector struct {
	conn         *rpc.Conn
	identity     *identity.FullIdentity
	healthclient internalpb.DRPCHealthInspectorClient
}

// NewInspector creates a new inspector client for access to overlay.
func NewInspector(ctx context.Context, address, path string) (*Inspector, error) {
	id, err := identity.Config{
		CertPath: fmt.Sprintf("%s/identity.cert", path),
		KeyPath:  fmt.Sprintf("%s/identity.key", path),
	}.Load()
	if err != nil {
		return nil, ErrIdentity.Wrap(err)
	}

	conn, err := rpc.NewDefaultDialer(nil).DialAddressUnencrypted(ctx, address)
	if err != nil {
		return &Inspector{}, ErrInspectorDial.Wrap(err)
	}

	return &Inspector{
		conn:         conn,
		identity:     id,
		healthclient: internalpb.NewDRPCHealthInspectorClient(conn),
	}, nil
}

// Close closes the inspector.
func (i *Inspector) Close() error { return i.conn.Close() }

// ObjectHealth gets information about the health of an object on the network.
func ObjectHealth(cmd *cobra.Command, args []string) (err error) {
	ctx, _ := process.Ctx(cmd)
	i, err := NewInspector(ctx, *Addr, *IdentityPath)
	if err != nil {
		return ErrArgs.Wrap(err)
	}
	defer func() { err = errs.Combine(err, i.Close()) }()

	startAfterSegment := int64(0) // start from first segment
	endBeforeSegment := int64(0)  // No end, so we stop when we've hit limit or arrived at the last segment
	limit := int64(0)             // No limit, so we stop when we've arrived at the last segment

	switch len(args) {
	case 6:
		limit, err = strconv.ParseInt(args[5], 10, 64)
		if err != nil {
			return ErrRequest.Wrap(err)
		}
		fallthrough
	case 5:
		endBeforeSegment, err = strconv.ParseInt(args[4], 10, 64)
		if err != nil {
			return ErrRequest.Wrap(err)
		}
		fallthrough
	case 4:
		startAfterSegment, err = strconv.ParseInt(args[3], 10, 64)
		if err != nil {
			return ErrRequest.Wrap(err)
		}
		fallthrough
	default:
	}
	decodedPath, err := base64.URLEncoding.DecodeString(args[2])
	if err != nil {
		return err
	}
	req := &internalpb.ObjectHealthRequest{
		ProjectId:         []byte(args[0]),
		Bucket:            []byte(args[1]),
		EncryptedPath:     decodedPath,
		StartAfterSegment: startAfterSegment,
		EndBeforeSegment:  endBeforeSegment,
		Limit:             int32(limit),
	}

	resp, err := i.healthclient.ObjectHealth(ctx, req)
	if err != nil {
		return ErrRequest.Wrap(err)
	}

	f, err := csvOutput()
	if err != nil {
		return err
	}
	defer func() {
		err := f.Close()
		if err != nil {
			fmt.Printf("error closing file: %+v\n", err)
		}
	}()

	w := csv.NewWriter(f)
	defer w.Flush()

	redundancy, err := eestream.NewRedundancyStrategyFromProto(resp.GetRedundancy())
	if err != nil {
		return ErrRequest.Wrap(err)
	}

	if err := printRedundancyTable(w, redundancy); err != nil {
		return err
	}

	if err := printSegmentHealthAndNodeTables(w, redundancy, resp.GetSegments()); err != nil {
		return err
	}

	return nil
}

// SegmentHealth gets information about the health of a segment on the network.
func SegmentHealth(cmd *cobra.Command, args []string) (err error) {
	ctx, _ := process.Ctx(cmd)
	i, err := NewInspector(ctx, *Addr, *IdentityPath)
	if err != nil {
		return ErrArgs.Wrap(err)
	}
	defer func() { err = errs.Combine(err, i.Close()) }()

	segmentIndex, err := strconv.ParseInt(args[1], 10, 64)
	if err != nil {
		return ErrRequest.Wrap(err)
	}

	req := &internalpb.SegmentHealthRequest{
		ProjectId:     []byte(args[0]),
		SegmentIndex:  segmentIndex,
		Bucket:        []byte(args[2]),
		EncryptedPath: []byte(args[3]),
	}

	resp, err := i.healthclient.SegmentHealth(ctx, req)
	if err != nil {
		return ErrRequest.Wrap(err)
	}

	f, err := csvOutput()
	if err != nil {
		return err
	}
	defer func() {
		err := f.Close()
		if err != nil {
			fmt.Printf("error closing file: %+v\n", err)
		}
	}()

	w := csv.NewWriter(f)
	defer w.Flush()

	redundancy, err := eestream.NewRedundancyStrategyFromProto(resp.GetRedundancy())
	if err != nil {
		return ErrRequest.Wrap(err)
	}

	if err := printRedundancyTable(w, redundancy); err != nil {
		return err
	}

	if err := printSegmentHealthAndNodeTables(w, redundancy, []*internalpb.SegmentHealth{resp.GetHealth()}); err != nil {
		return err
	}

	return nil
}

func csvOutput() (*os.File, error) {
	if CSVPath == "stdout" {
		return os.Stdout, nil
	}

	return os.Create(CSVPath)
}

func printSegmentHealthAndNodeTables(w *csv.Writer, redundancy eestream.RedundancyStrategy, segments []*internalpb.SegmentHealth) error {
	segmentTableHeader := []string{
		"Segment Index", "Healthy Nodes", "Unhealthy Nodes", "Offline Nodes",
	}

	if err := w.Write(segmentTableHeader); err != nil {
		return fmt.Errorf("error writing record to csv: %w", err)
	}

	currentNodeIndex := 1                     // start at index 1 to leave first column empty
	nodeIndices := make(map[storj.NodeID]int) // to keep track of node positions for node table
	// Add each segment to the segmentTable
	for _, segment := range segments {
		healthyNodes := segment.HealthyIds               // healthy nodes with pieces currently online
		unhealthyNodes := segment.UnhealthyIds           // unhealthy nodes with pieces currently online
		offlineNodes := segment.OfflineIds               // offline nodes
		segmentIndexPath := string(segment.GetSegment()) // path formatted Segment Index

		row := []string{
			segmentIndexPath,
			strconv.FormatInt(int64(len(healthyNodes)), 10),
			strconv.FormatInt(int64(len(unhealthyNodes)), 10),
			strconv.FormatInt(int64(len(offlineNodes)), 10),
		}

		if err := w.Write(row); err != nil {
			return fmt.Errorf("error writing record to csv: %w", err)
		}

		allNodes := []storj.NodeID{}
		allNodes = append(allNodes, healthyNodes...)
		allNodes = append(allNodes, unhealthyNodes...)
		allNodes = append(allNodes, offlineNodes...)
		for _, id := range allNodes {
			if nodeIndices[id] == 0 {
				nodeIndices[id] = currentNodeIndex
				currentNodeIndex++
			}
		}
	}

	if err := w.Write([]string{}); err != nil {
		return fmt.Errorf("error writing record to csv: %w", err)
	}

	numNodes := len(nodeIndices)
	nodeTableHeader := make([]string, numNodes+1)
	for id, i := range nodeIndices {
		nodeTableHeader[i] = id.String()
	}
	if err := w.Write(nodeTableHeader); err != nil {
		return fmt.Errorf("error writing record to csv: %w", err)
	}

	// Add online/offline info to the node table
	for _, segment := range segments {
		row := make([]string, numNodes+1)
		for _, id := range segment.HealthyIds {
			i := nodeIndices[id]
			row[i] = "healthy"
		}
		for _, id := range segment.UnhealthyIds {
			i := nodeIndices[id]
			row[i] = "unhealthy"
		}
		for _, id := range segment.OfflineIds {
			i := nodeIndices[id]
			row[i] = "offline"
		}
		row[0] = string(segment.GetSegment())
		if err := w.Write(row); err != nil {
			return fmt.Errorf("error writing record to csv: %w", err)
		}
	}

	return nil
}

func printRedundancyTable(w *csv.Writer, redundancy eestream.RedundancyStrategy) error {
	total := redundancy.TotalCount()                  // total amount of pieces we generated (n)
	required := redundancy.RequiredCount()            // minimum required stripes for reconstruction (k)
	optimalThreshold := redundancy.OptimalThreshold() // amount of pieces we need to store to call it a success (o)
	repairThreshold := redundancy.RepairThreshold()   // amount of pieces we need to drop to before triggering repair (m)

	redundancyTable := [][]string{
		{"Total Pieces (n)", "Minimum Required (k)", "Optimal Threshold (o)", "Repair Threshold (m)"},
		{strconv.Itoa(total), strconv.Itoa(required), strconv.Itoa(optimalThreshold), strconv.Itoa(repairThreshold)},
		{},
	}

	for _, row := range redundancyTable {
		if err := w.Write(row); err != nil {
			return fmt.Errorf("error writing record to csv: %w", err)
		}
	}

	return nil
}

func init() {
	rootCmd.AddCommand(statsCmd)
	rootCmd.AddCommand(healthCmd)

	healthCmd.AddCommand(objectHealthCmd)
	healthCmd.AddCommand(segmentHealthCmd)

	objectHealthCmd.Flags().StringVar(&CSVPath, "csv-path", "stdout", "csv path where command output is written")

	flag.Parse()
}

func main() {
	process.Exec(rootCmd)
}
