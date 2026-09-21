package db

import (
	"context"
	"fmt"
	"net/http"
	"path"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	awss3 "github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/benbjohnson/litestream"
	"github.com/benbjohnson/litestream/s3"
)

// S3Config configures S3-backed (or S3-compatible: R2, MinIO, ...) replication
// and write-lease coordination for every tenant, using one bucket with
// per-tenant key prefixes: "<Prefix>/tenants/<logical>" holds the replicated
// WAL, "<Prefix>/leases/<logical>" holds that tenant's write lease.
type S3Config struct {
	Bucket          string
	Prefix          string // default "rekam"
	Region          string // default "us-east-1"; ignored by most S3-compatible stores
	Endpoint        string // set for R2/MinIO/other non-AWS S3-compatible stores
	AccessKeyID     string
	SecretAccessKey string

	// Owner identifies this node in a lease it holds — set it to this node's
	// own reachable base URL (e.g. "http://10.0.1.5:5000") so a losing
	// AcquireLease/RenewLease directly yields a usable forwarding target via
	// ErrNotPrimary.Owner. Empty falls back to s3.Leaser's own hostname:pid
	// default, which identifies but doesn't address the current holder.
	Owner string
}

// NewS3Replication builds a Replication backed by S3 (or an S3-compatible
// store) for both WAL replication and write-lease coordination. The lease
// uses s3.Leaser's conditional-PUT CAS on a small per-tenant object — the same
// mechanism Cursor's Continuity design uses for Git — so no separate
// coordinator process (Consul, etcd, ...) is required. See
// docs/plan-distributed-tenants.md.
func NewS3Replication(ctx context.Context, cfg S3Config, ropts ReplicationOptions) (*Replication, error) {
	if cfg.Bucket == "" {
		return nil, fmt.Errorf("s3 replication: Bucket is required")
	}
	prefix := cfg.Prefix
	if prefix == "" {
		prefix = "rekam"
	}
	region := cfg.Region
	if region == "" {
		region = "us-east-1"
	}

	awsClient, err := newAWSS3Client(ctx, region, cfg.Endpoint, cfg.AccessKeyID, cfg.SecretAccessKey)
	if err != nil {
		return nil, fmt.Errorf("s3 replication: build client: %w", err)
	}

	ropts.ReplicaClient = func(logical string) (litestream.ReplicaClient, error) {
		c := s3.NewReplicaClient()
		c.Bucket = cfg.Bucket
		c.Path = path.Join(prefix, "tenants", logical)
		c.Region = cfg.Region
		c.Endpoint = cfg.Endpoint
		c.AccessKeyID = cfg.AccessKeyID
		c.SecretAccessKey = cfg.SecretAccessKey
		return c, nil
	}
	ropts.Leaser = func(logical string) (litestream.Leaser, error) {
		l := s3.NewLeaser()
		l.Bucket = cfg.Bucket
		l.Path = path.Join(prefix, "leases", logical)
		if cfg.Owner != "" {
			l.Owner = cfg.Owner
		}
		l.SetClient(awsClient)
		return l, nil
	}

	return NewReplication(ropts)
}

// newAWSS3Client builds the AWS SDK v2 client s3.Leaser needs directly (unlike
// s3.ReplicaClient, which builds its own internally from plain fields).
func newAWSS3Client(ctx context.Context, region, endpoint, accessKeyID, secretAccessKey string) (*awss3.Client, error) {
	var optFns []func(*awsconfig.LoadOptions) error
	if region != "" {
		optFns = append(optFns, awsconfig.WithRegion(region))
	}
	if accessKeyID != "" {
		optFns = append(optFns, awsconfig.WithCredentialsProvider(
			credentials.NewStaticCredentialsProvider(accessKeyID, secretAccessKey, "")))
	}
	optFns = append(optFns, awsconfig.WithHTTPClient(&http.Client{Timeout: 30 * time.Second}))

	cfg, err := awsconfig.LoadDefaultConfig(ctx, optFns...)
	if err != nil {
		return nil, err
	}
	return awss3.NewFromConfig(cfg, func(o *awss3.Options) {
		if endpoint != "" {
			o.BaseEndpoint = aws.String(endpoint)
			o.UsePathStyle = true // required by most non-AWS S3-compatible stores
		}
	}), nil
}
