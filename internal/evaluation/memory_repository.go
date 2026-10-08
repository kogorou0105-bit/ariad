package evaluation

import (
	"context"
	"sort"
	"sync"
)

type MemoryRepository struct {
	mu   sync.RWMutex
	sets map[string]map[string]TestSet
	runs map[string]map[string]Run
}

func NewMemoryRepository() *MemoryRepository {
	return &MemoryRepository{sets: map[string]map[string]TestSet{}, runs: map[string]map[string]Run{}}
}
func (r *MemoryRepository) CreateSet(ctx context.Context, set TestSet) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.sets[set.WorkspaceID] == nil {
		r.sets[set.WorkspaceID] = map[string]TestSet{}
	}
	r.sets[set.WorkspaceID][set.ID] = set
	return nil
}
func (r *MemoryRepository) ListSets(ctx context.Context, w string) ([]TestSet, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := []TestSet{}
	for _, s := range r.sets[w] {
		s.Cases = append([]TestCase(nil), s.Cases...)
		out = append(out, s)
	}
	sortSets(out)
	return out, nil
}
func (r *MemoryRepository) GetSet(ctx context.Context, w, id string) (TestSet, bool, error) {
	if err := ctx.Err(); err != nil {
		return TestSet{}, false, err
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	s, ok := r.sets[w][id]
	s.Cases = append([]TestCase(nil), s.Cases...)
	return s, ok, nil
}
func (r *MemoryRepository) UpdateSet(ctx context.Context, set TestSet) (bool, error) {
	if err := ctx.Err(); err != nil {
		return false, err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	old, ok := r.sets[set.WorkspaceID][set.ID]
	if !ok {
		return false, nil
	}
	set.Cases = old.Cases
	set.CreatedAt = old.CreatedAt
	r.sets[set.WorkspaceID][set.ID] = set
	return true, nil
}
func (r *MemoryRepository) DeleteSet(ctx context.Context, w, id string) (bool, error) {
	if err := ctx.Err(); err != nil {
		return false, err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, ok := r.sets[w][id]; !ok {
		return false, nil
	}
	delete(r.sets[w], id)
	return true, nil
}
func (r *MemoryRepository) CreateCase(ctx context.Context, item TestCase) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	set := r.sets[item.WorkspaceID][item.SetID]
	set.Cases = append(set.Cases, item)
	set.UpdatedAt = item.UpdatedAt
	r.sets[item.WorkspaceID][item.SetID] = set
	return nil
}
func (r *MemoryRepository) UpdateCase(ctx context.Context, item TestCase) (bool, error) {
	if err := ctx.Err(); err != nil {
		return false, err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	set, ok := r.sets[item.WorkspaceID][item.SetID]
	if !ok {
		return false, nil
	}
	for i, old := range set.Cases {
		if old.ID == item.ID {
			item.CreatedAt = old.CreatedAt
			set.Cases[i] = item
			set.UpdatedAt = item.UpdatedAt
			r.sets[item.WorkspaceID][item.SetID] = set
			return true, nil
		}
	}
	return false, nil
}
func (r *MemoryRepository) DeleteCase(ctx context.Context, w, setID, caseID string) (bool, error) {
	if err := ctx.Err(); err != nil {
		return false, err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	set, ok := r.sets[w][setID]
	if !ok {
		return false, nil
	}
	for i, item := range set.Cases {
		if item.ID == caseID {
			set.Cases = append(set.Cases[:i], set.Cases[i+1:]...)
			r.sets[w][setID] = set
			return true, nil
		}
	}
	return false, nil
}
func (r *MemoryRepository) SaveRun(ctx context.Context, run Run) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.runs[run.WorkspaceID] == nil {
		r.runs[run.WorkspaceID] = map[string]Run{}
	}
	r.runs[run.WorkspaceID][run.ID] = run
	return nil
}
func (r *MemoryRepository) ListRuns(ctx context.Context, w, setID string) ([]Run, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := []Run{}
	for _, run := range r.runs[w] {
		if run.SetID == setID {
			run.Results = append([]CaseResult(nil), run.Results...)
			out = append(out, run)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].CreatedAt.After(out[j].CreatedAt) })
	return out, nil
}
func (r *MemoryRepository) GetRun(ctx context.Context, w, id string) (Run, bool, error) {
	if err := ctx.Err(); err != nil {
		return Run{}, false, err
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	run, ok := r.runs[w][id]
	run.Results = append([]CaseResult(nil), run.Results...)
	return run, ok, nil
}
