package providers

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"cloudsync/config"
	"golang.org/x/oauth2"
	"golang.org/x/oauth2/google"
	"google.golang.org/api/drive/v3"
	"google.golang.org/api/option"
)

// ─── gdriveProvider ────────────────────────────────────────────────

type gdriveProvider struct {
	cfg       config.ProviderConfig
	svc       *drive.Service
	rootID    string // Google Drive ID of the remote sync root folder
}

func newGDriveProvider(ctx context.Context, pc config.ProviderConfig) (*gdriveProvider, error) {
	clientID := pc.Credentials["client_id"]
	clientSecret := pc.Credentials["client_secret"]
	tokenFile := pc.Credentials["token_file"]

	if clientID == "" || clientSecret == "" {
		return nil, fmt.Errorf("gdrive: credentials must include client_id and client_secret")
	}
	if tokenFile == "" {
		home, _ := os.UserHomeDir()
		tokenFile = filepath.Join(home, ".cloudsync", "gdrive_token.json")
	}

	oauthCfg := &oauth2.Config{
		ClientID:     clientID,
		ClientSecret: clientSecret,
		Scopes:       []string{drive.DriveScope},
		Endpoint:     google.Endpoint,
		// "oob" is the out-of-band redirect for desktop/CLI apps
		RedirectURL: "urn:ietf:wg:oauth:2.0:oob",
	}

	// Load cached token or run interactive OAuth2 flow
	tok, err := loadGDriveToken(tokenFile)
	if err != nil {
		tok, err = runGDriveOAuthFlow(ctx, oauthCfg, tokenFile)
		if err != nil {
			return nil, fmt.Errorf("gdrive: authentication failed: %w", err)
		}
	}

	httpClient := oauthCfg.Client(ctx, tok)
	svc, err := drive.NewService(ctx, option.WithHTTPClient(httpClient))
	if err != nil {
		return nil, fmt.Errorf("gdrive: create service: %w", err)
	}

	p := &gdriveProvider{cfg: pc, svc: svc}

	p.rootID, err = p.resolveFolderPath(ctx, pc.RemoteFolder)
	if err != nil {
		return nil, fmt.Errorf("gdrive: cannot find remote folder %q: %w", pc.RemoteFolder, err)
	}

	return p, nil
}

func (g *gdriveProvider) Name() string          { return g.cfg.Name }
func (g *gdriveProvider) Type() string          { return "gdrive" }
func (g *gdriveProvider) RemoteFolder() string  { return g.cfg.RemoteFolder }
func (g *gdriveProvider) LocalDest() string     { return g.cfg.LocalDest }
func (g *gdriveProvider) SyncDirection() string { return g.cfg.SyncDirection }

// ─── ListFiles ─────────────────────────────────────────────────────

func (g *gdriveProvider) ListFiles(ctx context.Context) ([]FileInfo, error) {
	var files []FileInfo
	err := g.listRecursive(ctx, g.rootID, "", &files)
	return files, err
}

func (g *gdriveProvider) listRecursive(ctx context.Context, folderID, prefix string, out *[]FileInfo) error {
	pageToken := ""
	for {
		q := fmt.Sprintf("'%s' in parents and trashed = false", folderID)
		call := g.svc.Files.List().
			Q(q).
			Fields("nextPageToken, files(id, name, mimeType, size, modifiedTime, md5Checksum)").
			PageSize(1000).
			Context(ctx)
		if pageToken != "" {
			call = call.PageToken(pageToken)
		}

		r, err := call.Do()
		if err != nil {
			return fmt.Errorf("list files in %s: %w", folderID, err)
		}

		for _, f := range r.Files {
			relPath := joinPath(prefix, f.Name)
			isFolder := f.MimeType == "application/vnd.google-apps.folder"

			if isFolder {
				if err := g.listRecursive(ctx, f.Id, relPath, out); err != nil {
					return err
				}
				continue
			}

			// Skip Google Docs native formats (they require export, not direct download)
			if strings.HasPrefix(f.MimeType, "application/vnd.google-apps.") {
				continue
			}

			var modTime time.Time
			if f.ModifiedTime != "" {
				modTime, _ = time.Parse(time.RFC3339, f.ModifiedTime)
			}
			*out = append(*out, FileInfo{
				Path:     relPath,
				Size:     f.Size,
				ModTime:  modTime,
				Checksum: f.Md5Checksum,
			})
		}

		if r.NextPageToken == "" {
			break
		}
		pageToken = r.NextPageToken
	}
	return nil
}

// ─── Download ──────────────────────────────────────────────────────

func (g *gdriveProvider) Download(ctx context.Context, remotePath, localPath string) error {
	fileID, err := g.resolveFilePath(ctx, remotePath)
	if err != nil {
		return fmt.Errorf("resolve %q: %w", remotePath, err)
	}

	resp, err := g.svc.Files.Get(fileID).Context(ctx).Download()
	if err != nil {
		return fmt.Errorf("download %q: %w", remotePath, err)
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

func (g *gdriveProvider) Upload(ctx context.Context, localPath, remotePath string) error {
	f, err := os.Open(localPath)
	if err != nil {
		return err
	}
	defer f.Close()

	fi, err := f.Stat()
	if err != nil {
		return err
	}

	parts := strings.Split(filepath.ToSlash(remotePath), "/")
	name := parts[len(parts)-1]

	// Ensure parent folders exist
	parentID := g.rootID
	for _, part := range parts[:len(parts)-1] {
		parentID, err = g.ensureFolder(ctx, parentID, part)
		if err != nil {
			return fmt.Errorf("ensure folder %q: %w", part, err)
		}
	}

	meta := &drive.File{
		Name:         name,
		Parents:      []string{parentID},
		ModifiedTime: fi.ModTime().UTC().Format(time.RFC3339),
	}

	// Update if exists, create if not
	existingID, _ := g.findFile(ctx, parentID, name)
	if existingID != "" {
		_, err = g.svc.Files.Update(existingID, meta).Media(f).Context(ctx).Do()
	} else {
		_, err = g.svc.Files.Create(meta).Media(f).Context(ctx).Do()
	}
	return err
}

// ─── Helpers ───────────────────────────────────────────────────────

// resolveFolderPath walks a slash-separated path starting from "root"
// and returns the Drive folder ID of the final component.
func (g *gdriveProvider) resolveFolderPath(ctx context.Context, path string) (string, error) {
	if path == "" || path == "root" {
		return "root", nil
	}
	parentID := "root"
	for _, part := range strings.Split(path, "/") {
		id, err := g.findFolder(ctx, parentID, part)
		if err != nil {
			return "", fmt.Errorf("folder %q not found under %s", part, parentID)
		}
		parentID = id
	}
	return parentID, nil
}

// resolveFilePath locates a file by slash-separated relative path under rootID.
func (g *gdriveProvider) resolveFilePath(ctx context.Context, relPath string) (string, error) {
	parts := strings.Split(filepath.ToSlash(relPath), "/")
	parentID := g.rootID
	var err error
	for _, part := range parts[:len(parts)-1] {
		parentID, err = g.findFolder(ctx, parentID, part)
		if err != nil {
			return "", err
		}
	}
	return g.findFile(ctx, parentID, parts[len(parts)-1])
}

func (g *gdriveProvider) findFolder(ctx context.Context, parentID, name string) (string, error) {
	escaped := strings.ReplaceAll(name, "'", "\\'")
	q := fmt.Sprintf("'%s' in parents and name='%s' and mimeType='application/vnd.google-apps.folder' and trashed=false", parentID, escaped)
	r, err := g.svc.Files.List().Q(q).Fields("files(id)").Context(ctx).Do()
	if err != nil {
		return "", err
	}
	if len(r.Files) == 0 {
		return "", fmt.Errorf("folder %q not found", name)
	}
	return r.Files[0].Id, nil
}

func (g *gdriveProvider) findFile(ctx context.Context, parentID, name string) (string, error) {
	escaped := strings.ReplaceAll(name, "'", "\\'")
	q := fmt.Sprintf("'%s' in parents and name='%s' and trashed=false", parentID, escaped)
	r, err := g.svc.Files.List().Q(q).Fields("files(id)").Context(ctx).Do()
	if err != nil {
		return "", err
	}
	if len(r.Files) == 0 {
		return "", fmt.Errorf("file %q not found", name)
	}
	return r.Files[0].Id, nil
}

func (g *gdriveProvider) ensureFolder(ctx context.Context, parentID, name string) (string, error) {
	if id, err := g.findFolder(ctx, parentID, name); err == nil {
		return id, nil
	}
	f := &drive.File{
		Name:     name,
		MimeType: "application/vnd.google-apps.folder",
		Parents:  []string{parentID},
	}
	created, err := g.svc.Files.Create(f).Context(ctx).Do()
	if err != nil {
		return "", fmt.Errorf("create folder %q: %w", name, err)
	}
	return created.Id, nil
}

func joinPath(prefix, name string) string {
	if prefix == "" {
		return name
	}
	return prefix + "/" + name
}

// ─── OAuth2 token persistence ──────────────────────────────────────

func loadGDriveToken(path string) (*oauth2.Token, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var tok oauth2.Token
	if err := json.Unmarshal(data, &tok); err != nil {
		return nil, err
	}
	return &tok, nil
}

func saveGDriveToken(path string, tok *oauth2.Token) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	data, err := json.MarshalIndent(tok, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o600) // owner-only permissions
}

// runGDriveOAuthFlow performs the "installed app" OAuth2 flow:
// prints the auth URL, waits for the user to paste the code.
func runGDriveOAuthFlow(ctx context.Context, cfg *oauth2.Config, tokenFile string) (*oauth2.Token, error) {
	authURL := cfg.AuthCodeURL("state-token", oauth2.AccessTypeOffline)
	fmt.Println("\n🔑 Google Drive authorization required.")
	fmt.Println("   Open the following URL in your browser:")
	fmt.Println()
	fmt.Println("   " + authURL)
	fmt.Println()
	fmt.Print("   Paste the authorization code here: ")

	var code string
	fmt.Scanln(&code)
	code = strings.TrimSpace(code)

	tok, err := cfg.Exchange(ctx, code)
	if err != nil {
		return nil, fmt.Errorf("exchange code: %w", err)
	}

	if err := saveGDriveToken(tokenFile, tok); err != nil {
		fmt.Printf("⚠️  Token could not be saved to %s: %v\n", tokenFile, err)
	} else {
		fmt.Printf("✅ Token saved to %s\n", tokenFile)
	}
	return tok, nil
}
