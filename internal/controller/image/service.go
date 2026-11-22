package image

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	"github.com/agomez/pulsar/internal/api"
	"github.com/agomez/pulsar/internal/store/etcd"
	"github.com/agomez/pulsar/pkg/id"
)

const keyPrefix = "/pulsar/images/"

type Image struct {
	ID        string    `json:"id"`
	Name      string    `json:"name"`
	Status    string    `json:"status"` // queued | active | error
	Format    string    `json:"format"` // qcow2 | raw | oci
	SizeBytes int64     `json:"size_bytes,omitempty"`
	URL       string    `json:"url,omitempty"` // set for URL-registered images; for uploads this is the controller download URL
	CreatedAt time.Time `json:"created_at"`
	// ProjectID owns the image; empty for platform images.
	ProjectID string `json:"project_id,omitempty"`
	// Visibility is "public" (every project) or "private" (owning project
	// only). Images created before ownership existed have neither field and
	// are treated as public so nothing in use disappears.
	Visibility string `json:"visibility"`
}

const (
	VisibilityPublic  = "public"
	VisibilityPrivate = "private"
)

// IsPublic reports whether every project may see and boot the image.
func (img *Image) IsPublic() bool {
	return img.Visibility == VisibilityPublic || (img.Visibility == "" && img.ProjectID == "")
}

// VisibleTo reports whether a project may see and boot the image.
func (img *Image) VisibleTo(projectID string) bool {
	return img.IsPublic() || (projectID != "" && img.ProjectID == projectID)
}

// UsableBy is the single rule for seeing, booting and building volumes from
// an image: platform admins may use any image; everyone else, public images
// and their active project's private ones.
func (img *Image) UsableBy(ctx context.Context, projectID string) bool {
	return api.IsPlatformAdmin(ctx) || img.VisibleTo(projectID)
}

// normalize fills in the effective visibility of images stored before
// ownership existed, so clients never see "visibility": "".
func (img *Image) normalize() *Image {
	if img.Visibility == "" {
		if img.IsPublic() {
			img.Visibility = VisibilityPublic
		} else {
			img.Visibility = VisibilityPrivate
		}
	}
	return img
}

type Service struct {
	store    *etcd.Client
	storeDir string
}

func NewService(store *etcd.Client, storeDir string) (*Service, error) {
	if err := os.MkdirAll(storeDir, 0o755); err != nil {
		return nil, fmt.Errorf("create image store dir: %w", err)
	}
	return &Service{store: store, storeDir: storeDir}, nil
}

func (s *Service) Create(ctx context.Context, img *Image) error {
	if img.ID == "" {
		img.ID = id.New()
	}
	img.CreatedAt = time.Now().UTC()
	data, _ := json.Marshal(img)
	return s.store.Put(ctx, keyPrefix+img.ID, string(data))
}

func (s *Service) Get(ctx context.Context, imageID string) (*Image, error) {
	val, err := s.store.Get(ctx, keyPrefix+imageID)
	if err != nil || val == "" {
		return nil, fmt.Errorf("image %s not found", imageID)
	}
	var img Image
	if err := json.Unmarshal([]byte(val), &img); err != nil {
		return nil, err
	}
	return img.normalize(), nil
}

func (s *Service) List(ctx context.Context) ([]*Image, error) {
	vals, err := s.store.GetPrefix(ctx, keyPrefix)
	if err != nil {
		return nil, err
	}
	out := make([]*Image, 0)
	for _, v := range vals {
		var img Image
		if err := json.Unmarshal([]byte(v), &img); err == nil {
			out = append(out, img.normalize())
		}
	}
	return out, nil
}

func (s *Service) Delete(ctx context.Context, imageID string) error {
	img, err := s.Get(ctx, imageID)
	if err != nil {
		return err
	}
	// Remove stored file if present
	if img.Format != "" {
		path := s.filePath(imageID, img.Format)
		if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
			return fmt.Errorf("remove image file: %w", err)
		}
	}
	return s.store.Delete(ctx, keyPrefix+imageID)
}

// StoreFile writes the reader into the image store and returns bytes written.
func (s *Service) StoreFile(imageID, format string, r io.Reader) (int64, error) {
	dest := s.filePath(imageID, format)
	tmp, err := os.CreateTemp(s.storeDir, ".upload-*")
	if err != nil {
		return 0, fmt.Errorf("create temp file: %w", err)
	}
	tmpName := tmp.Name()
	defer func() {
		tmp.Close()
		os.Remove(tmpName) // no-op if rename succeeded
	}()

	n, err := io.Copy(tmp, r)
	if err != nil {
		return 0, fmt.Errorf("write image: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return 0, fmt.Errorf("flush image: %w", err)
	}
	if err := os.Rename(tmpName, dest); err != nil {
		return 0, fmt.Errorf("install image: %w", err)
	}
	return n, nil
}

// OpenFile returns a ReadCloser for the stored image file.
func (s *Service) OpenFile(imageID, format string) (io.ReadCloser, int64, error) {
	path := s.filePath(imageID, format)
	f, err := os.Open(path)
	if err != nil {
		return nil, 0, fmt.Errorf("open image file: %w", err)
	}
	fi, err := f.Stat()
	if err != nil {
		f.Close()
		return nil, 0, err
	}
	return f, fi.Size(), nil
}

func (s *Service) filePath(imageID, format string) string {
	ext := format
	if ext == "" {
		ext = "img"
	}
	return filepath.Join(s.storeDir, imageID+"."+ext)
}

func (s *Service) Update(ctx context.Context, img *Image) error {
	data, _ := json.Marshal(img)
	return s.store.Put(ctx, keyPrefix+img.ID, string(data))
}
