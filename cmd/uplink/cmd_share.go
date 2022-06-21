// Copyright (C) 2021 Storj Labs, Inc.
// See LICENSE for copying information.

package main

import (
	"context"
	"fmt"
	"io/ioutil"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/zeebo/clingy"
	"github.com/zeebo/errs"

	"storj.io/storj/cmd/uplink/ulext"
	"storj.io/uplink"
	"storj.io/uplink/edge"
)

type cmdShare struct {
	ex ulext.External
	ap accessPermissions

	access      string
	exportTo    string
	baseURL     string
	register    bool
	url         bool
	dns         string
	authService string
	caCert      string
	public      bool
}

func newCmdShare(ex ulext.External) *cmdShare {
	return &cmdShare{ex: ex}
}

func (c *cmdShare) Setup(params clingy.Parameters) {
	c.access = params.Flag("access", "Access name or value to share", "").(string)
	params.Break()

	c.exportTo = params.Flag("export-to", "Path to export the shared access to", "").(string)
	c.baseURL = params.Flag("base-url", "The base url for link sharing", "https://link.storjshare.io").(string)
	c.register = params.Flag("register", "If true, creates and registers access grant", false,
		clingy.Transform(strconv.ParseBool), clingy.Boolean,
	).(bool)
	c.url = params.Flag("url", "If true, returns a url for the shared path. implies --register and --public", false,
		clingy.Transform(strconv.ParseBool), clingy.Boolean,
	).(bool)
	c.dns = params.Flag("dns", "Specify your custom hostname. if set, returns dns settings for web hosting. implies --register and --public", "").(string)
	c.authService = params.Flag("auth-service", "URL for shared auth service", "https://auth.storjshare.io").(string)
	c.public = params.Flag("public", "If true, the access will be public. --dns and --url override this", false,
		clingy.Transform(strconv.ParseBool), clingy.Boolean,
	).(bool)
	params.Break()

	c.ap.Setup(params, false)
}

func (c *cmdShare) Execute(ctx clingy.Context) error {
	if len(c.ap.prefixes) == 0 {
		return errs.New("You must specify at least one prefix to share. Use the access restrict command to restrict with no prefixes.")
	}

	access, err := c.ex.OpenAccess(c.access)
	if err != nil {
		return err
	}

	access, err = c.ap.Apply(access)
	if err != nil {
		return err
	}

	c.public = c.public || c.url || c.dns != ""

	if c.public {
		c.register = true

		if c.ap.notAfter.String() == "" {
			fmt.Fprintf(ctx, "It's not recommended to create a shared Access without an expiration date.")
			fmt.Fprintf(ctx, "If you wish to do so anyway, please run this command with --not-after=none.")
			return nil
		}

		if c.ap.notAfter.String() == "none" {
			c.ap.notAfter = time.Time{}
		}
	}

	newAccessData, err := access.Serialize()
	if err != nil {
		return err
	}

	fmt.Fprintf(ctx, "Sharing access to satellite %s\n", access.SatelliteAddress())
	fmt.Fprintf(ctx, "=========== ACCESS RESTRICTIONS ==========================================================\n")
	fmt.Fprintf(ctx, "Download  : %s\n", formatPermission(c.ap.AllowDownload()))
	fmt.Fprintf(ctx, "Upload    : %s\n", formatPermission(c.ap.AllowUpload()))
	fmt.Fprintf(ctx, "Lists     : %s\n", formatPermission(c.ap.AllowList()))
	fmt.Fprintf(ctx, "Deletes   : %s\n", formatPermission(c.ap.AllowDelete()))
	fmt.Fprintf(ctx, "NotBefore : %s\n", formatTimeRestriction(c.ap.notBefore))
	fmt.Fprintf(ctx, "NotAfter  : %s\n", formatTimeRestriction(c.ap.notAfter))
	fmt.Fprintf(ctx, "Paths     : %s\n", formatPaths(c.ap.prefixes))
	fmt.Fprintf(ctx, "=========== SERIALIZED ACCESS WITH THE ABOVE RESTRICTIONS TO SHARE WITH OTHERS ===========\n")
	fmt.Fprintf(ctx, "Access    : %s\n", newAccessData)

	if c.register {
		credentials, err := RegisterAccess(ctx, access, c.authService, c.public, c.caCert)
		if err != nil {
			return err
		}
		err = DisplayGatewayCredentials(ctx, *credentials, "", "")
		if err != nil {
			return err
		}
		_, err = fmt.Fprintln(ctx, "Public Access: ", c.public)
		if err != nil {
			return err
		}

		if c.url {
			if c.ap.AllowUpload() || c.ap.AllowDelete() {
				return errs.New("will only generate linksharing URL with readonly restrictions")
			}

			err = createURL(ctx, credentials.AccessKeyID, c.ap.prefixes, c.baseURL)
			if err != nil {
				return err
			}
		}

		if c.dns != "" {
			if c.ap.AllowUpload() || c.ap.AllowDelete() {
				return errs.New("will only generate DNS entries with readonly restrictions")
			}

			err = createDNS(ctx, credentials.AccessKeyID, c.ap.prefixes, c.baseURL, c.dns)
			if err != nil {
				return err
			}
		}
	}

	if c.exportTo != "" {
		// convert to an absolute path, mostly for output purposes.
		exportTo, err := filepath.Abs(c.exportTo)
		if err != nil {
			return err
		}
		if err := ioutil.WriteFile(exportTo, []byte(newAccessData+"\n"), 0600); err != nil {
			return err
		}
		fmt.Fprintln(ctx, "Exported to:", exportTo)
	}

	return nil
}

func formatPermission(allowed bool) string {
	if allowed {
		return "Allowed"
	}
	return "Disallowed"
}

func formatTimeRestriction(t time.Time) string {
	if t.IsZero() {
		return "No restriction"
	}
	return formatTime(true, t)
}

func formatPaths(sharePrefixes []uplink.SharePrefix) string {
	if len(sharePrefixes) == 0 {
		return "WARNING! The entire project is shared!"
	}

	var paths []string
	for _, prefix := range sharePrefixes {
		path := "sj://" + prefix.Bucket
		if len(prefix.Prefix) == 0 {
			path += "/ (entire bucket)"
		} else {
			path += "/" + prefix.Prefix
		}

		paths = append(paths, path)
	}

	return strings.Join(paths, "\n            ")
}

// RegisterAccess registers an access grant with a Gateway Authorization Service.
func RegisterAccess(ctx context.Context, access *uplink.Access, authService string, public bool, certificateFile string) (credentials *edge.Credentials, err error) {
	if authService == "" {
		return nil, errs.New("no auth service address provided")
	}

	// preserve compatibility with previous https service
	authService = strings.TrimPrefix(authService, "https://")
	authService = strings.TrimSuffix(authService, "/")
	if !strings.Contains(authService, ":") {
		authService += ":7777"
	}

	var certificatePEM []byte
	if certificateFile != "" {
		certificatePEM, err = os.ReadFile(certificateFile)
		if err != nil {
			return nil, errs.New("can't read certificate file: %w", err)
		}
	}

	edgeConfig := edge.Config{
		AuthServiceAddress: authService,
		CertificatePEM:     certificatePEM,
	}
	return edgeConfig.RegisterAccess(ctx, access, &edge.RegisterAccessOptions{Public: public})
}

// Creates linksharing url for allowed path prefixes.
func createURL(ctx clingy.Context, accessKeyID string, prefixes []uplink.SharePrefix, baseURL string) (err error) {
	if len(prefixes) == 0 {
		return errs.New("need at least a bucket to create a working linkshare URL")
	}

	bucket := prefixes[0].Bucket
	key := prefixes[0].Prefix

	url, err := edge.JoinShareURL(baseURL, accessKeyID, bucket, key, nil)
	if err != nil {
		return err
	}

	fmt.Fprintf(ctx, "=========== BROWSER URL ==================================================================\n")
	if key != "" && key[len(key)-1:] != "/" {
		fmt.Fprintf(ctx, "REMINDER  : Object key must end in '/' when trying to share a prefix\n")
	}
	fmt.Fprintf(ctx, "URL       : %s\n", url)
	return nil
}

// Creates dns record info for allowed path prefixes.
func createDNS(ctx clingy.Context, accessKey string, prefixes []uplink.SharePrefix, baseURL, dns string) (err error) {
	if len(prefixes) == 0 {
		return errs.New("need at least a bucket to create DNS records")
	}

	bucket := prefixes[0].Bucket
	key := prefixes[0].Prefix

	CNAME, err := url.Parse(baseURL)
	if err != nil {
		return err
	}

	var printStorjRoot string
	if key == "" {
		printStorjRoot = fmt.Sprintf("txt-%s\tIN\tTXT  \tstorj-root:%s", dns, bucket)
	} else {
		printStorjRoot = fmt.Sprintf("txt-%s\tIN\tTXT  \tstorj-root:%s/%s", dns, bucket, key)
	}

	fmt.Fprintf(ctx, "=========== DNS INFO =====================================================================\n")
	fmt.Fprintf(ctx, "Remember to update the $ORIGIN with your domain name. You may also change the $TTL.\n")
	fmt.Fprintf(ctx, "$ORIGIN example.com.\n")
	fmt.Fprintf(ctx, "$TTL    3600\n")
	fmt.Fprintf(ctx, "%s    \tIN\tCNAME\t%s.\n", dns, CNAME.Host)
	fmt.Fprintln(ctx, printStorjRoot)
	fmt.Fprintf(ctx, "txt-%s\tIN\tTXT  \tstorj-access:%s\n", dns, accessKey)

	return nil
}

// DisplayGatewayCredentials formats and writes credentials to stdout.
func DisplayGatewayCredentials(ctx clingy.Context, credentials edge.Credentials, format string, awsProfile string) (err error) {
	switch format {
	case "env": // export / set compatible format
		// note that AWS_ENDPOINT configuration is not natively utilized by the AWS CLI
		_, err = fmt.Fprintf(ctx, "AWS_ACCESS_KEY_ID=%s\n"+
			"AWS_SECRET_ACCESS_KEY=%s\n"+
			"AWS_ENDPOINT=%s\n",
			credentials.AccessKeyID, credentials.SecretKey, credentials.Endpoint)
		if err != nil {
			return err
		}
	case "aws": // aws configuration commands
		profile := ""
		if awsProfile != "" {
			profile = " --profile " + awsProfile
			_, err = fmt.Fprintf(ctx, "aws configure %s\n", profile)
			if err != nil {
				return err
			}
		}
		// note that the endpoint_url configuration is not natively utilized by the AWS CLI
		_, err = fmt.Fprintf(ctx, "aws configure %s set aws_access_key_id %s\n"+
			"aws configure %s set aws_secret_access_key %s\n"+
			"aws configure %s set s3.endpoint_url %s\n",
			profile, credentials.AccessKeyID, profile, credentials.SecretKey, profile, credentials.Endpoint)
		if err != nil {
			return err
		}
	default: // plain text
		_, err = fmt.Fprintf(ctx, "========== CREDENTIALS ===================================================================\n"+
			"Access Key ID: %s\n"+
			"Secret Key   : %s\n"+
			"Endpoint     : %s\n",
			credentials.AccessKeyID, credentials.SecretKey, credentials.Endpoint)
		if err != nil {
			return err
		}
	}
	return nil
}
