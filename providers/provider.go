// Package providers defines the cloud storage abstraction layer.
// Each supported backend (Google Drive, Azure Blob, AWS S3) implements
// the Provider interface so the sync engine can treat them uniformly.
package providers

import (
	"context"
	"fmt"
	"time"

	"cloudsync/config"
)

// FileInfo describes a single file on either the cloud or the local filesystem.
type FileInfo struct {
	// Path is the file's path relative to the sync root (always forward slashes).
	Path    string
	Size    int64
	ModTime time.Time
	// Checksum is provider-specific (MD5 for Drive/Azure, ETag for S3, SHA-256 for local).
	// May be empty; sync falls back to size+modtime comparison when missing.
	Checksum string
	IsDir    bool
}

// Provider is the interface every cloud storage backend must satisfy.
type Provider interface {
	// Name returns the human-readable label from the config.
	Name() string
	// Type returns the provider type ("gdrive", "azure", "s3").
	Type() string
	// RemoteFolder returns the configured remote root path.
	RemoteFolder() string
	// LocalDest returns the local directory for this provider.
	LocalDest() string
	// SyncDirection returns "both", "cloud-to-local", or "local-to-cloud".
	SyncDirection() string

	// ListFiles enumerates all files under the configured remote folder,
	// recursing into sub-directories. Directories are not returned.
	ListFiles(ctx context.Context) ([]FileInfo, error)
	// Download fetches remotePath (relative to the remote root) and writes
	// it to localPath, creating any intermediate directories.
	Download(ctx context.Context, remotePath, localPath string) error
	// Upload sends localPath to remotePath (relative to the remote root),
	// creating any necessary remote "directories" / prefixes.
	Upload(ctx context.Context, localPath, remotePath string) error
}

// New constructs the appropriate Provider for the given config entry.
func New(ctx context.Context, pc config.ProviderConfig) (Provider, error) {
	switch pc.Type {
	case "gdrive":
		return newGDriveProvider(ctx, pc)
	case "azure":
		return newAzureProvider(ctx, pc)
	case "s3":
		return newS3Provider(ctx, pc)
	default:
		return nil, fmt.Errorf("unknown provider type %q — valid values: gdrive, azure, s3", pc.Type)
	}
}
