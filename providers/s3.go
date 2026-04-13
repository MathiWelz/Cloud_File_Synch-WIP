package providers

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"cloudsync/config"
	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/s3"
)

// ─── s3Provider ────────────────────────────────────────────────────

type s3Provider struct {
	cfg    config.ProviderConfig
	client *s3.Client
	bucket string
	prefix string // key prefix within the bucket (without trailing slash)
}

func newS3Provider(ctx context.Context, pc config.ProviderConfig) (*s3Provider, error) {
	region := pc.Credentials["region"]
	if region == "" {
		region = "us-east-1"
	}

	// remote_folder format: "bucket" or "bucket/prefix/sub"
	parts := strings.SplitN(pc.RemoteFolder, "/", 2)
	bucket := parts[0]
	prefix := ""
	if len(parts) > 1 {
		prefix = parts[1]
	}

	// Build AWS config options
	var opts []func(*awsconfig.LoadOptions) error
	opts = append(opts, awsconfig.WithRegion(region))

	accessKeyID := pc.Credentials["access_key_id"]
	secretKey := pc.Credentials["secret_access_key"]
	sessionToken := pc.Credentials["session_token"] // optional — for STS / assumed roles

	if accessKeyID != "" && secretKey != "" {
		opts = append(opts, awsconfig.WithCredentialsProvider(
			credentials.NewStaticCredentialsProvider(accessKeyID, secretKey, sessionToken),
		))
	}
	// If no static creds provided, the SDK will use its standard credential
	// chain: env vars → ~/.aws/credentials → EC2 instance role → ECS task role.

	awsCfg, err := awsconfig.LoadDefaultConfig(ctx, opts...)
	if err != nil {
		return nil, fmt.Errorf("s3: load AWS config: %w", err)
	}

	return &s3Provider{
		cfg:    pc,
		client: s3.NewFromConfig(awsCfg),
		bucket: bucket,
		prefix: prefix,
	}, nil
}

func (p *s3Provider) Name() string          { return p.cfg.Name }
func (p *s3Provider) Type() string          { return "s3" }
func (p *s3Provider) RemoteFolder() string  { return p.cfg.RemoteFolder }
func (p *s3Provider) LocalDest() string     { return p.cfg.LocalDest }
func (p *s3Provider) SyncDirection() string { return p.cfg.SyncDirection }

// ─── ListFiles ─────────────────────────────────────────────────────

func (p *s3Provider) ListFiles(ctx context.Context) ([]FileInfo, error) {
	searchPrefix := p.prefix
	if searchPrefix != "" && !strings.HasSuffix(searchPrefix, "/") {
		searchPrefix += "/"
	}

	var files []FileInfo
	var contToken *string

	for {
		resp, err := p.client.ListObjectsV2(ctx, &s3.ListObjectsV2Input{
			Bucket:            aws.String(p.bucket),
			Prefix:            aws.String(searchPrefix),
			ContinuationToken: contToken,
		})
		if err != nil {
			return nil, fmt.Errorf("s3: list objects: %w", err)
		}

		for _, obj := range resp.Contents {
			key := aws.ToString(obj.Key)
			relPath := strings.TrimPrefix(key, searchPrefix)
			if relPath == "" {
				continue
			}

			fi := FileInfo{
				Path:    relPath,
				Size:    aws.ToInt64(obj.Size),
				ModTime: aws.ToTime(obj.LastModified),
			}
			// S3 ETags are MD5 for non-multipart uploads, a composite hash otherwise.
			if obj.ETag != nil {
				fi.Checksum = strings.Trim(*obj.ETag, `"`)
			}
			files = append(files, fi)
		}

		if !aws.ToBool(resp.IsTruncated) {
			break
		}
		contToken = resp.NextContinuationToken
	}
	return files, nil
}

// ─── Download ──────────────────────────────────────────────────────

func (p *s3Provider) Download(ctx context.Context, remotePath, localPath string) error {
	key := p.objectKey(remotePath)

	resp, err := p.client.GetObject(ctx, &s3.GetObjectInput{
		Bucket: aws.String(p.bucket),
		Key:    aws.String(key),
	})
	if err != nil {
		return fmt.Errorf("s3: get object %q: %w", key, err)
	}
	defer resp.Body.Close()

	if err := os.MkdirAll(filepath.Dir(localPath), 0o750); err != nil {
		return err
	}
	f, err := os.Create(localPath)
	if err != nil {
		return err
	}
	defer f.Close()

	_, err = io.Copy(f, resp.Body)
	return err
}

// ─── Upload ────────────────────────────────────────────────────────

func (p *s3Provider) Upload(ctx context.Context, localPath, remotePath string) error {
	key := p.objectKey(remotePath)

	f, err := os.Open(localPath)
	if err != nil {
		return err
	}
	defer f.Close()

	_, err = p.client.PutObject(ctx, &s3.PutObjectInput{
		Bucket: aws.String(p.bucket),
		Key:    aws.String(key),
		Body:   f,
	})
	if err != nil {
		return fmt.Errorf("s3: put object %q: %w", key, err)
	}
	return nil
}

// ─── Helpers ───────────────────────────────────────────────────────

func (p *s3Provider) objectKey(relPath string) string {
	if p.prefix == "" {
		return filepath.ToSlash(relPath)
	}
	return p.prefix + "/" + filepath.ToSlash(relPath)
}
