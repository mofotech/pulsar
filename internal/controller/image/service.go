package image

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	"github.com/agomez/pulsar/internal/store/etcd"
	"github.com/agomez/pulsar/pkg/id"
)

const keyPrefix = "/pulsar/images/"

type Image struct {
	ID        string    `json:"id"`
	Name      string    `json:"name"`
	Status    string    `json:"status"`              // queued | active | error
	Format    string    `json:"format"`              // qcow2 | raw | oci
	SizeBytes int64     `json:"size_bytes,omitempty"`
	URL       string    `json:"url,omitempty"` // set for URL-registered images; for uploads this is the controller download URL
	CreatedAt time.Time `json:"created_at"`
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
	return &img, json.Unmarshal([]byte(val), &img)
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
			out = append(out, &img)
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
