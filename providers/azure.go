package providers

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"cloudsync/config"
	"github.com/Azure/azure-sdk-for-go/sdk/storage/azblob"
)

// ─── azureProvider ─────────────────────────────────────────────────

type azureProvider struct {
	cfg       config.ProviderConfig
	client    *azblob.Client
	container string
	prefix    string // optional path prefix within the container
}

func newAzureProvider(ctx context.Context, pc config.ProviderConfig) (*azureProvider, error) {
	connStr := pc.Credentials["connection_string"]

	if connStr == "" {
		account := pc.Credentials["account_name"]
		key := pc.Credentials["account_key"]
		if account == "" || key == "" {
			return nil, fmt.Errorf("azure: provide connection_string OR (account_name + account_key)")
		}
		connStr = fmt.Sprintf(
			"DefaultEndpointsProtocol=https;AccountName=%s;AccountKey=%s;EndpointSuffix=core.windows.net",
			account, key,
		)
	}

	client, err := azblob.NewClientFromConnectionString(connStr, nil)
	if err != nil {
		return nil, fmt.Errorf("azure: create client: %w", err)
	}

	// remote_folder format: "container" or "container/prefix/sub"
	parts := strings.SplitN(pc.RemoteFolder, "/", 2)
	container := parts[0]
	prefix := ""
	if len(parts) > 1 {
		prefix = parts[1]
	}

	return &azureProvider{
		cfg:       pc,
		client:    client,
		container: container,
		prefix:    prefix,
	}, nil
}

func (a *azureProvider) Name() string          { return a.cfg.Name }
func (a *azureProvider) Type() string          { return "azure" }
func (a *azureProvider) RemoteFolder() string  { return a.cfg.RemoteFolder }
func (a *azureProvider) LocalDest() string     { return a.cfg.LocalDest }
func (a *azureProvider) SyncDirection() string { return a.cfg.SyncDirection }

// ─── ListFiles ─────────────────────────────────────────────────────

func (a *azureProvider) ListFiles(ctx context.Context) ([]FileInfo, error) {
	searchPrefix := a.prefix
	if searchPrefix != "" && !strings.HasSuffix(searchPrefix, "/") {
		searchPrefix += "/"
	}

	pager := a.client.NewListBlobsFlatPager(a.container, &azblob.ListBlobsFlatOptions{
		Prefix: strPtr(searchPrefix),
	})

	var files []FileInfo
	for pager.More() {
		page, err := pager.NextPage(ctx)
		if err != nil {
			return nil, fmt.Errorf("azure: list blobs: %w", err)
		}

		for _, item := range page.Segment.BlobItems {
			blobName := derefStr(item.Name)
			relPath := strings.TrimPrefix(blobName, searchPrefix)
			if relPath == "" {
				continue
			}

			fi := FileInfo{Path: relPath}
			if item.Properties.ContentLength != nil {
				fi.Size = *item.Properties.ContentLength
			}
			if item.Properties.LastModified != nil {
				fi.ModTime = *item.Properties.LastModified
			}
			if item.Properties.ContentMD5 != nil {
				fi.Checksum = fmt.Sprintf("%x", item.Properties.ContentMD5)
			}
			files = append(files, fi)
		}
	}
	return files, nil
}

// ─── Download ──────────────────────────────────────────────────────

func (a *azureProvider) Download(ctx context.Context, remotePath, localPath string) error {
	key := a.blobKey(remotePath)

	blobClient := a.client.ServiceClient().
		NewContainerClient(a.container).
		NewBlockBlobClient(key)

	resp, err := blobClient.DownloadStream(ctx, nil)
	if err != nil {
		return fmt.Errorf("azure: download %q: %w", key, err)
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

func (a *azureProvider) Upload(ctx context.Context, localPath, remotePath string) error {
	key := a.blobKey(remotePath)

	blobClient := a.client.ServiceClient().
		NewContainerClient(a.container).
		NewBlockBlobClient(key)

	f, err := os.Open(localPath)
	if err != nil {
		return err
	}
	defer f.Close()

	_, err = blobClient.UploadStream(ctx, f, nil)
	if err != nil {
		return fmt.Errorf("azure: upload %q: %w", key, err)
	}
	return nil
}

// ─── Helpers ───────────────────────────────────────────────────────

func (a *azureProvider) blobKey(relPath string) string {
	if a.prefix == "" {
		return relPath
	}
	prefix := a.prefix
	if !strings.HasSuffix(prefix, "/") {
		prefix += "/"
	}
	return prefix + relPath
}

func strPtr(s string) *string   { return &s }
func derefStr(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}
