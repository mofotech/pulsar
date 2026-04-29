package keypair

import (
	"context"
	"crypto/md5" //nolint:gosec // MD5 used for SSH key fingerprint display per RFC 4716, not for security
	"encoding/base64"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/agomez/pulsar/internal/store/postgres"
	"github.com/agomez/pulsar/pkg/id"
	"golang.org/x/crypto/ssh"
)

// KeyPair is a stored SSH public key belonging to a user.
type KeyPair struct {
	ID          string    `json:"id"`
	UserID      string    `json:"user_id"`
	Name        string    `json:"name"`
	PublicKey   string    `json:"public_key"`
	Fingerprint string    `json:"fingerprint"`
	CreatedAt   time.Time `json:"created_at"`
}

// CreateRequest contains the fields for importing or generating a keypair.
type CreateRequest struct {
	// Name is a human-readable label for this keypair (unique per project).
	Name string `json:"name"`
	// PublicKey is the OpenSSH-format public key to import.
	// If empty, the server generates a new RSA-4096 keypair and returns the
	// private key once (it is not stored server-side).
	PublicKey string `json:"public_key,omitempty"`
}

// CreateResponse is returned when a keypair is created.
// PrivateKey is only populated when the server generated the keypair.
type CreateResponse struct {
	KeyPair    KeyPair `json:"keypair"`
	PrivateKey string  `json:"private_key,omitempty"`
}

// Service provides CRUD for SSH keypairs stored in Postgres.
type Service struct {
	db *postgres.DB
}

func NewService(db *postgres.DB) *Service {
	return &Service{db: db}
}

// Create imports (or generates) a keypair for a user.
func (s *Service) Create(ctx context.Context, userID string, req CreateRequest) (*CreateResponse, error) {
	if req.Name == "" {
		return nil, fmt.Errorf("keypair name is required")
	}

	var pubKeyStr, fingerprint, privateKeyPEM string

	if req.PublicKey != "" {
		// Import path: parse and validate the submitted public key.
		pubKey, comment, _, _, err := ssh.ParseAuthorizedKey([]byte(req.PublicKey))
		if err != nil {
			return nil, fmt.Errorf("invalid public key: %w", err)
		}
		_ = comment
		fingerprint = sshFingerprint(pubKey)
		pubKeyStr = strings.TrimSpace(req.PublicKey)
	} else {
		// Generate path: create a new RSA-4096 keypair.
		privKey, pubKey, err := generateRSAKeyPair()
		if err != nil {
			return nil, fmt.Errorf("generate keypair: %w", err)
		}
		fingerprint = sshFingerprint(pubKey)
		authorizedKey := ssh.MarshalAuthorizedKey(pubKey)
		pubKeyStr = strings.TrimSpace(string(authorizedKey))
		privateKeyPEM = privKey
	}

	kp := &KeyPair{
		ID:          id.New(),
		UserID:      userID,
		Name:        req.Name,
		PublicKey:   pubKeyStr,
		Fingerprint: fingerprint,
		CreatedAt:   time.Now().UTC(),
	}

	_, err := s.db.Pool().Exec(ctx,
		`INSERT INTO keypairs (id, user_id, name, public_key, fingerprint, created_at)
		 VALUES ($1, $2, $3, $4, $5, $6)`,
		kp.ID, kp.UserID, kp.Name, kp.PublicKey, kp.Fingerprint, kp.CreatedAt,
	)
	if err != nil {
		if strings.Contains(err.Error(), "unique") || strings.Contains(err.Error(), "duplicate") {
			return nil, fmt.Errorf("keypair named %q already exists for this user", req.Name)
		}
		return nil, fmt.Errorf("insert keypair: %w", err)
	}

	return &CreateResponse{KeyPair: *kp, PrivateKey: privateKeyPEM}, nil
}

// List returns all keypairs for a user, sorted by creation time.
func (s *Service) List(ctx context.Context, userID string) ([]*KeyPair, error) {
	rows, err := s.db.Pool().Query(ctx,
		`SELECT id, user_id, name, public_key, fingerprint, created_at
		   FROM keypairs
		  WHERE user_id = $1
		  ORDER BY created_at ASC`,
		userID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []*KeyPair
	for rows.Next() {
		kp := &KeyPair{}
		if err := rows.Scan(&kp.ID, &kp.UserID, &kp.Name, &kp.PublicKey, &kp.Fingerprint, &kp.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, kp)
	}
	return out, rows.Err()
}

// Get returns a single keypair by ID, verifying user ownership.
func (s *Service) Get(ctx context.Context, userID, keypairID string) (*KeyPair, error) {
	kp := &KeyPair{}
	err := s.db.Pool().QueryRow(ctx,
		`SELECT id, user_id, name, public_key, fingerprint, created_at
		   FROM keypairs
		  WHERE id = $1 AND user_id = $2`,
		keypairID, userID,
	).Scan(&kp.ID, &kp.UserID, &kp.Name, &kp.PublicKey, &kp.Fingerprint, &kp.CreatedAt)
	if err != nil {
		return nil, fmt.Errorf("keypair %s not found", keypairID)
	}
	return kp, nil
}

// GetByName returns a keypair by name for a user (used during instance launch).
func (s *Service) GetByName(ctx context.Context, userID, name string) (*KeyPair, error) {
	kp := &KeyPair{}
	err := s.db.Pool().QueryRow(ctx,
		`SELECT id, user_id, name, public_key, fingerprint, created_at
		   FROM keypairs
		  WHERE user_id = $1 AND name = $2`,
		userID, name,
	).Scan(&kp.ID, &kp.UserID, &kp.Name, &kp.PublicKey, &kp.Fingerprint, &kp.CreatedAt)
	if err != nil {
		return nil, fmt.Errorf("keypair %q not found", name)
	}
	return kp, nil
}

// Delete removes a keypair owned by the user. Returns nil if already absent (idempotent).
func (s *Service) Delete(ctx context.Context, userID, keypairID string) error {
	_, err := s.db.Pool().Exec(ctx,
		`DELETE FROM keypairs WHERE id = $1 AND user_id = $2`,
		keypairID, userID,
	)
	return err
}

// PublicKeysForNames returns the public key strings for the given keypair names
// belonging to the specified user. Names that don't exist are silently skipped.
func (s *Service) PublicKeysForNames(ctx context.Context, userID string, names []string) ([]string, error) {
	if len(names) == 0 {
		return nil, nil
	}
	// Build $1,$2,... placeholders
	placeholders := make([]string, len(names))
	args := make([]interface{}, len(names)+1)
	args[0] = userID
	for i, n := range names {
		placeholders[i] = fmt.Sprintf("$%d", i+2)
		args[i+1] = n
	}
	query := fmt.Sprintf(
		`SELECT public_key FROM keypairs WHERE user_id = $1 AND name IN (%s) ORDER BY name`,
		strings.Join(placeholders, ","),
	)
	rows, err := s.db.Pool().Query(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var keys []string
	for rows.Next() {
		var k string
		if err := rows.Scan(&k); err != nil {
			return nil, err
		}
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys, rows.Err()
}

// ─── Crypto helpers ───────────────────────────────────────────────────────────

// sshFingerprint returns the MD5 fingerprint of a public key in the
// "aa:bb:cc:..." format (same as `ssh-keygen -l -E md5`).
func sshFingerprint(pub ssh.PublicKey) string {
	sum := md5.Sum(pub.Marshal()) //nolint:gosec
	parts := make([]string, len(sum))
	for i, b := range sum {
		parts[i] = fmt.Sprintf("%02x", b)
	}
	return strings.Join(parts, ":")
}

// generateRSAKeyPair creates a 4096-bit RSA keypair.
// Returns (privatePEM, sshPublicKey, error).
func generateRSAKeyPair() (string, ssh.PublicKey, error) {
	privKey, err := generateRSA4096()
	if err != nil {
		return "", nil, err
	}
	sshPub, err := ssh.NewPublicKey(&privKey.PublicKey)
	if err != nil {
		return "", nil, err
	}
	pemBytes := encodeRSAPrivateKeyToPEM(privKey)
	return base64.StdEncoding.EncodeToString(pemBytes), sshPub, nil
}
