// Package secrets reads sensitive values from Google Secret Manager.
//
// The Cloud Run runtime service account is granted secretAccessor on exactly
// one secret (curiosity-discord) and nothing else, so a compromised container
// cannot read unrelated secrets. Values are cached per instance to keep cold
// starts fast and stay inside the Secret Manager free tier.
package secrets

import (
	"context"
	"fmt"
	"sync"

	secretmanager "cloud.google.com/go/secretmanager/apiv1"
	"cloud.google.com/go/secretmanager/apiv1/secretmanagerpb"
)

// Manager fetches and caches secret payloads.
type Manager struct {
	projectID string
	client    *secretmanager.Client

	mu    sync.Mutex
	cache map[string]string
}

// New constructs a Manager using Application Default Credentials (the Cloud
// Run service identity); no API keys are involved.
func New(ctx context.Context, projectID string) (*Manager, error) {
	cl, err := secretmanager.NewClient(ctx)
	if err != nil {
		return nil, fmt.Errorf("secretmanager client: %w", err)
	}
	return &Manager{projectID: projectID, client: cl, cache: map[string]string{}}, nil
}

// Get returns the latest version payload of the named secret, cached.
func (m *Manager) Get(ctx context.Context, name string) (string, error) {
	m.mu.Lock()
	if v, ok := m.cache[name]; ok {
		m.mu.Unlock()
		return v, nil
	}
	m.mu.Unlock()

	res, err := m.client.AccessSecretVersion(ctx, &secretmanagerpb.AccessSecretVersionRequest{
		Name: fmt.Sprintf("projects/%s/secrets/%s/versions/latest", m.projectID, name),
	})
	if err != nil {
		return "", fmt.Errorf("access secret %s: %w", name, err)
	}
	v := string(res.Payload.Data)

	m.mu.Lock()
	m.cache[name] = v
	m.mu.Unlock()
	return v, nil
}
