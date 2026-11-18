package mcpserver

import (
	"encoding/json"
	"fmt"
	"sync"
	"time"
)

const recipeTTL = 15 * time.Minute
const maxRecipes = 8

type workflowRecipe struct {
	plan    []WorkflowStep
	process string
	expires time.Time
	timer   *time.Timer
}

// UI labels can contain private data. Recipes never reach the disk receipt
// store, journals or reports; only a random session-local handle is returned.
type recipeCache struct {
	mu      sync.Mutex
	epoch   uint64
	entries map[string]workflowRecipe
}

func recipePlan(plan []WorkflowStep) ([]WorkflowStep, error) {
	if len(plan) < 1 || len(plan) > 50 {
		return nil, fmt.Errorf("recipe requires 1..50 bounded steps")
	}
	for _, s := range plan {
		if (s.Step.Action != "click" && s.Step.Action != "move" && s.Step.Action != "wait") || s.Step.Text != "" || s.Step.Key != "" || len(s.Step.Keys) > 0 || s.Step.Delta != 0 || s.Step.Horizontal {
			return nil, fmt.Errorf("recipes accept semantic click/move/wait only; typed text and keyboard actions cannot be remembered")
		}
		if _, err := workflowAction(s.Step, "recipe"); err != nil {
			return nil, err
		}
		if err := validateExpectation(&s.Expect); err != nil {
			return nil, err
		}
	}
	data, err := json.Marshal(plan)
	if err != nil || len(data) > 32768 {
		return nil, fmt.Errorf("recipe exceeds 32 KiB")
	}
	var copy []WorkflowStep
	if err := json.Unmarshal(data, &copy); err != nil {
		return nil, err
	}
	return copy, nil
}

func (c *recipeCache) generation() uint64 {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.epoch
}
func (c *recipeCache) prune(now time.Time) {
	for id, e := range c.entries {
		if !now.Before(e.expires) {
			if e.timer != nil {
				e.timer.Stop()
			}
			delete(c.entries, id)
		}
	}
}
func (c *recipeCache) save(plan []WorkflowStep, process string, epoch uint64) string {
	copy, err := recipePlan(plan)
	if err != nil {
		return ""
	}
	id, err := opaqueID()
	if err != nil {
		return ""
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.epoch != epoch {
		return ""
	} // clear/stop revokes in-flight learning.
	c.prune(time.Now())
	if c.entries == nil {
		c.entries = make(map[string]workflowRecipe)
	}
	if len(c.entries) >= maxRecipes {
		oldest := ""
		for key, e := range c.entries {
			if oldest == "" || e.expires.Before(c.entries[oldest].expires) {
				oldest = key
			}
		}
		// Stop the evicted entry's expiry callback as well.
		if e := c.entries[oldest]; e.timer != nil {
			e.timer.Stop()
		}
		delete(c.entries, oldest)
	}
	expires := time.Now().Add(recipeTTL)
	timer := time.AfterFunc(recipeTTL, func() {
		c.mu.Lock()
		defer c.mu.Unlock()
		if e, ok := c.entries[id]; ok && !time.Now().Before(e.expires) {
			delete(c.entries, id)
		}
	})
	c.entries[id] = workflowRecipe{plan: copy, process: process, expires: expires, timer: timer}
	return id
}
func (c *recipeCache) load(id, process string) ([]WorkflowStep, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.prune(time.Now())
	e, ok := c.entries[id]
	if !ok || e.process != process {
		return nil, fmt.Errorf("unknown, expired or wrong-application workflow recipe")
	}
	return recipePlan(e.plan) // Each run owns a fresh copy, never a mutable template.
}
func (c *recipeCache) clear() {
	c.mu.Lock()
	defer c.mu.Unlock()
	for _, e := range c.entries {
		if e.timer != nil {
			e.timer.Stop()
		}
	}
	c.entries = nil
	c.epoch++
}
func (c *recipeCache) count() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.prune(time.Now())
	return len(c.entries)
}
