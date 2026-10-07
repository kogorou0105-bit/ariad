package modelconfig

import (
	"context"
	"sync"
)

type MemoryRepository struct {
	mu      sync.RWMutex
	configs map[string]Config
}

func NewMemoryRepository() *MemoryRepository {
	return &MemoryRepository{configs: make(map[string]Config)}
}

func (r *MemoryRepository) Get(ctx context.Context, workspaceID string) (Config, bool, error) {
	if err := ctx.Err(); err != nil {
		return Config{}, false, err
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	config, found := r.configs[workspaceID]
	return config, found, nil
}

func (r *MemoryRepository) Save(ctx context.Context, config Config) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.configs[config.WorkspaceID] = config
	return nil
}

func (r *MemoryRepository) Delete(ctx context.Context, workspaceID string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.configs, workspaceID)
	return nil
}
