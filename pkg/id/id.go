package id

import "github.com/google/uuid"

// New returns a new UUID v4.
// Once uuid v7 stabilises in the google/uuid library, this can be changed
// to uuid.NewV7() for monotonic, sortable IDs without any API change.
func New() string {
	return uuid.New().String()
}

// Parse validates and returns a UUID string, or an error.
func Parse(s string) (string, error) {
	u, err := uuid.Parse(s)
	if err != nil {
		return "", err
	}
	return u.String(), nil
}
