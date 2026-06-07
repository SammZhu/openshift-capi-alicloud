package client

import (
	"bytes"
	"fmt"
	"time"

	"github.com/aliyun/aliyun-oss-go-sdk/oss"
)

// IgnitionStoreParams describes an OSS object used to offload an oversized
// Ignition (user-data) payload that exceeds the ECS RunInstances UserData limit.
type IgnitionStoreParams struct {
	// Bucket is the OSS bucket that holds the offloaded Ignition objects.
	Bucket string
	// Endpoint overrides the OSS endpoint. Empty derives the region's internal
	// endpoint, reachable from within the VPC without internet egress.
	Endpoint string
	// RegionID is used to derive the default endpoint when Endpoint is empty.
	RegionID string
	// Key is the object key.
	Key string
	// Data is the object body (PutIgnitionObject only).
	Data []byte
	// Expiry is how long the presigned GET URL stays valid (PutIgnitionObject
	// only). Zero defaults to one hour.
	Expiry time.Duration
}

// ossEndpointFor returns the configured endpoint, or the region's internal OSS
// endpoint. The internal endpoint (oss-<region>-internal.aliyuncs.com) resolves
// over VPC DNS and is reachable without NAT/internet — both the in-cluster
// controller (PUT) and the booting RHCOS node (GET) sit in the VPC, so this
// keeps Ignition fetch on the private network, matching the air-gapped model.
func ossEndpointFor(endpoint, region string) string {
	if endpoint != "" {
		return endpoint
	}
	return fmt.Sprintf("https://oss-%s-internal.aliyuncs.com", region)
}

// newOSSClient builds an OSS client from the controller's AccessKey credentials.
// OSS offload requires AccessKey credentials in the environment; a bare ECS
// RAM-role credential is not wired here.
func (c *alibabacloudClient) newOSSClient(endpoint, region string) (*oss.Client, error) {
	ak := firstNonEmpty("ALIBABA_CLOUD_ACCESS_KEY_ID", "ALIBABACLOUD_ACCESS_KEY_ID")
	sk := firstNonEmpty("ALIBABA_CLOUD_ACCESS_KEY_SECRET", "ALIBABACLOUD_ACCESS_KEY_SECRET")
	if ak == "" || sk == "" {
		return nil, fmt.Errorf("OSS ignition offload requires ALIBABA_CLOUD_ACCESS_KEY_{ID,SECRET} in the controller environment")
	}
	return oss.New(ossEndpointFor(endpoint, region), ak, sk)
}

// PutIgnitionObject uploads the Ignition payload to OSS and returns a presigned
// GET URL the booting node can fetch without credentials. The upload overwrites
// any existing object at the key, so retries are idempotent.
func (c *alibabacloudClient) PutIgnitionObject(p IgnitionStoreParams) (string, error) {
	cli, err := c.newOSSClient(p.Endpoint, p.RegionID)
	if err != nil {
		return "", err
	}
	bucket, err := cli.Bucket(p.Bucket)
	if err != nil {
		return "", fmt.Errorf("OSS bucket %s: %w", p.Bucket, err)
	}
	if err := bucket.PutObject(p.Key, bytes.NewReader(p.Data)); err != nil {
		return "", fmt.Errorf("OSS put %s/%s: %w", p.Bucket, p.Key, err)
	}
	expiry := p.Expiry
	if expiry <= 0 {
		expiry = time.Hour
	}
	url, err := bucket.SignURL(p.Key, oss.HTTPGet, int64(expiry.Seconds()))
	if err != nil {
		return "", fmt.Errorf("OSS sign %s/%s: %w", p.Bucket, p.Key, err)
	}
	return url, nil
}

// DeleteIgnitionObject removes a previously offloaded Ignition object. It is
// best-effort cleanup on machine deletion; an OSS bucket lifecycle rule on the
// key prefix is the authoritative backstop against leaks.
func (c *alibabacloudClient) DeleteIgnitionObject(p IgnitionStoreParams) error {
	cli, err := c.newOSSClient(p.Endpoint, p.RegionID)
	if err != nil {
		return err
	}
	bucket, err := cli.Bucket(p.Bucket)
	if err != nil {
		return fmt.Errorf("OSS bucket %s: %w", p.Bucket, err)
	}
	if err := bucket.DeleteObject(p.Key); err != nil {
		return fmt.Errorf("OSS delete %s/%s: %w", p.Bucket, p.Key, err)
	}
	return nil
}
