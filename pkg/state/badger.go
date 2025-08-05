// Package state provides BadgerDB-based state management
package state

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"strings"
	"sync"
	"time"

	"github.com/dgraph-io/badger/v4"
	"github.com/rizome-dev/arc/pkg/types"
)

const (
	// Key prefixes for different data types
	agentPrefix    = "agent:"
	workflowPrefix = "workflow:"
	taskPrefix     = "task:"
	eventPrefix    = "event:"
	
	// Index prefixes for efficient querying
	agentLabelPrefix    = "idx:agent:label:"
	workflowMetaPrefix  = "idx:workflow:meta:"
	eventTypePrefix     = "idx:event:type:"
	eventSourcePrefix   = "idx:event:source:"
	
	// Default TTL for events (7 days)
	defaultEventTTL = 7 * 24 * time.Hour
)

// BadgerStore implements Store interface using BadgerDB
type BadgerStore struct {
	db       *badger.DB
	path     string
	eventTTL time.Duration
	mu       sync.RWMutex
	closed   bool
}

// BadgerStoreConfig holds BadgerDB-specific configuration
type BadgerStoreConfig struct {
	Path     string
	EventTTL time.Duration
	Options  badger.Options
}

// NewBadgerStore creates a new BadgerDB-based store
func NewBadgerStore(config BadgerStoreConfig) (*BadgerStore, error) {
	if config.Path == "" {
		return nil, fmt.Errorf("path is required for BadgerDB store")
	}
	
	if config.EventTTL == 0 {
		config.EventTTL = defaultEventTTL
	}
	
	// Set default options if not provided
	opts := config.Options
	if opts.Dir == "" {
		opts = badger.DefaultOptions(config.Path)
		opts.Logger = nil // Disable BadgerDB logging by default
	}
	
	db, err := badger.Open(opts)
	if err != nil {
		return nil, fmt.Errorf("failed to open BadgerDB: %w", err)
	}
	
	store := &BadgerStore{
		db:       db,
		path:     config.Path,
		eventTTL: config.EventTTL,
	}
	
	// Start garbage collection goroutine
	go store.runGC()
	
	return store, nil
}

// Initialize initializes the store
func (s *BadgerStore) Initialize(ctx context.Context) error {
	s.mu.RLock()
	defer s.mu.RUnlock()
	
	if s.closed {
		return fmt.Errorf("store is closed")
	}
	
	// Run initial garbage collection
	return s.db.RunValueLogGC(0.5)
}

// Close closes the store
func (s *BadgerStore) Close(ctx context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	
	if s.closed {
		return nil
	}
	
	s.closed = true
	return s.db.Close()
}

// HealthCheck performs a health check
func (s *BadgerStore) HealthCheck(ctx context.Context) error {
	s.mu.RLock()
	defer s.mu.RUnlock()
	
	if s.closed {
		return fmt.Errorf("store is closed")
	}
	
	// Try a simple read operation
	return s.db.View(func(txn *badger.Txn) error {
		_, err := txn.Get([]byte("health"))
		if err == badger.ErrKeyNotFound {
			return nil // Expected for health check
		}
		return err
	})
}

// CreateAgent creates a new agent
func (s *BadgerStore) CreateAgent(ctx context.Context, agent *types.Agent) error {
	s.mu.RLock()
	defer s.mu.RUnlock()
	
	if s.closed {
		return fmt.Errorf("store is closed")
	}
	
	return s.db.Update(func(txn *badger.Txn) error {
		key := []byte(agentPrefix + agent.ID)
		
		// Check if agent already exists
		_, err := txn.Get(key)
		if err == nil {
			return fmt.Errorf("agent %s already exists", agent.ID)
		}
		if err != badger.ErrKeyNotFound {
			return fmt.Errorf("failed to check agent existence: %w", err)
		}
		
		// Serialize agent
		data, err := json.Marshal(agent)
		if err != nil {
			return fmt.Errorf("failed to marshal agent: %w", err)
		}
		
		// Store agent
		if err := txn.Set(key, data); err != nil {
			return fmt.Errorf("failed to store agent: %w", err)
		}
		
		// Create label indexes
		return s.createAgentLabelIndexes(txn, agent)
	})
}

// GetAgent retrieves an agent by ID
func (s *BadgerStore) GetAgent(ctx context.Context, agentID string) (*types.Agent, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	
	if s.closed {
		return nil, fmt.Errorf("store is closed")
	}
	
	var agent *types.Agent
	
	err := s.db.View(func(txn *badger.Txn) error {
		key := []byte(agentPrefix + agentID)
		
		item, err := txn.Get(key)
		if err == badger.ErrKeyNotFound {
			return fmt.Errorf("agent %s not found", agentID)
		}
		if err != nil {
			return fmt.Errorf("failed to get agent: %w", err)
		}
		
		return item.Value(func(val []byte) error {
			agent = &types.Agent{}
			return json.Unmarshal(val, agent)
		})
	})
	
	return agent, err
}

// UpdateAgent updates an existing agent
func (s *BadgerStore) UpdateAgent(ctx context.Context, agent *types.Agent) error {
	s.mu.RLock()
	defer s.mu.RUnlock()
	
	if s.closed {
		return fmt.Errorf("store is closed")
	}
	
	return s.db.Update(func(txn *badger.Txn) error {
		key := []byte(agentPrefix + agent.ID)
		
		// Check if agent exists
		item, err := txn.Get(key)
		if err == badger.ErrKeyNotFound {
			return fmt.Errorf("agent %s not found", agent.ID)
		}
		if err != nil {
			return fmt.Errorf("failed to check agent existence: %w", err)
		}
		
		// Get old agent for index cleanup
		var oldAgent types.Agent
		err = item.Value(func(val []byte) error {
			return json.Unmarshal(val, &oldAgent)
		})
		if err != nil {
			return fmt.Errorf("failed to unmarshal old agent: %w", err)
		}
		
		// Remove old label indexes
		if err := s.removeAgentLabelIndexes(txn, &oldAgent); err != nil {
			return err
		}
		
		// Serialize updated agent
		data, err := json.Marshal(agent)
		if err != nil {
			return fmt.Errorf("failed to marshal agent: %w", err)
		}
		
		// Store updated agent
		if err := txn.Set(key, data); err != nil {
			return fmt.Errorf("failed to store agent: %w", err)
		}
		
		// Create new label indexes
		return s.createAgentLabelIndexes(txn, agent)
	})
}

// DeleteAgent removes an agent
func (s *BadgerStore) DeleteAgent(ctx context.Context, agentID string) error {
	s.mu.RLock()
	defer s.mu.RUnlock()
	
	if s.closed {
		return fmt.Errorf("store is closed")
	}
	
	return s.db.Update(func(txn *badger.Txn) error {
		key := []byte(agentPrefix + agentID)
		
		// Get agent for index cleanup
		item, err := txn.Get(key)
		if err == badger.ErrKeyNotFound {
			return fmt.Errorf("agent %s not found", agentID)
		}
		if err != nil {
			return fmt.Errorf("failed to get agent: %w", err)
		}
		
		var agent types.Agent
		err = item.Value(func(val []byte) error {
			return json.Unmarshal(val, &agent)
		})
		if err != nil {
			return fmt.Errorf("failed to unmarshal agent: %w", err)
		}
		
		// Remove label indexes
		if err := s.removeAgentLabelIndexes(txn, &agent); err != nil {
			return err
		}
		
		// Delete agent
		return txn.Delete(key)
	})
}

// ListAgents returns agents matching the filter
func (s *BadgerStore) ListAgents(ctx context.Context, filter map[string]string) ([]*types.Agent, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	
	if s.closed {
		return nil, fmt.Errorf("store is closed")
	}
	
	var agents []*types.Agent
	
	err := s.db.View(func(txn *badger.Txn) error {
		// If filter is empty, scan all agents
		if len(filter) == 0 {
			return s.scanAllAgents(txn, &agents)
		}
		
		// Use indexes for filtered queries
		return s.scanAgentsByFilter(txn, filter, &agents)
	})
	
	return agents, err
}

// CreateWorkflow creates a new workflow
func (s *BadgerStore) CreateWorkflow(ctx context.Context, workflow *types.Workflow) error {
	s.mu.RLock()
	defer s.mu.RUnlock()
	
	if s.closed {
		return fmt.Errorf("store is closed")
	}
	
	return s.db.Update(func(txn *badger.Txn) error {
		key := []byte(workflowPrefix + workflow.ID)
		
		// Check if workflow already exists
		_, err := txn.Get(key)
		if err == nil {
			return fmt.Errorf("workflow %s already exists", workflow.ID)
		}
		if err != badger.ErrKeyNotFound {
			return fmt.Errorf("failed to check workflow existence: %w", err)
		}
		
		// Serialize workflow
		data, err := json.Marshal(workflow)
		if err != nil {
			return fmt.Errorf("failed to marshal workflow: %w", err)
		}
		
		// Store workflow
		if err := txn.Set(key, data); err != nil {
			return fmt.Errorf("failed to store workflow: %w", err)
		}
		
		// Create metadata indexes
		if err := s.createWorkflowMetaIndexes(txn, workflow); err != nil {
			return err
		}
		
		// Store individual tasks for efficient access
		return s.storeWorkflowTasks(txn, workflow)
	})
}

// GetWorkflow retrieves a workflow by ID
func (s *BadgerStore) GetWorkflow(ctx context.Context, workflowID string) (*types.Workflow, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	
	if s.closed {
		return nil, fmt.Errorf("store is closed")
	}
	
	var workflow *types.Workflow
	
	err := s.db.View(func(txn *badger.Txn) error {
		key := []byte(workflowPrefix + workflowID)
		
		item, err := txn.Get(key)
		if err == badger.ErrKeyNotFound {
			return fmt.Errorf("workflow %s not found", workflowID)
		}
		if err != nil {
			return fmt.Errorf("failed to get workflow: %w", err)
		}
		
		return item.Value(func(val []byte) error {
			workflow = &types.Workflow{}
			return json.Unmarshal(val, workflow)
		})
	})
	
	return workflow, err
}

// UpdateWorkflow updates an existing workflow
func (s *BadgerStore) UpdateWorkflow(ctx context.Context, workflow *types.Workflow) error {
	s.mu.RLock()
	defer s.mu.RUnlock()
	
	if s.closed {
		return fmt.Errorf("store is closed")
	}
	
	return s.db.Update(func(txn *badger.Txn) error {
		key := []byte(workflowPrefix + workflow.ID)
		
		// Check if workflow exists
		item, err := txn.Get(key)
		if err == badger.ErrKeyNotFound {
			return fmt.Errorf("workflow %s not found", workflow.ID)
		}
		if err != nil {
			return fmt.Errorf("failed to check workflow existence: %w", err)
		}
		
		// Get old workflow for index cleanup
		var oldWorkflow types.Workflow
		err = item.Value(func(val []byte) error {
			return json.Unmarshal(val, &oldWorkflow)
		})
		if err != nil {
			return fmt.Errorf("failed to unmarshal old workflow: %w", err)
		}
		
		// Remove old metadata indexes
		if err := s.removeWorkflowMetaIndexes(txn, &oldWorkflow); err != nil {
			return err
		}
		
		// Remove old task entries
		if err := s.removeWorkflowTasks(txn, &oldWorkflow); err != nil {
			return err
		}
		
		// Serialize updated workflow
		data, err := json.Marshal(workflow)
		if err != nil {
			return fmt.Errorf("failed to marshal workflow: %w", err)
		}
		
		// Store updated workflow
		if err := txn.Set(key, data); err != nil {
			return fmt.Errorf("failed to store workflow: %w", err)
		}
		
		// Create new metadata indexes
		if err := s.createWorkflowMetaIndexes(txn, workflow); err != nil {
			return err
		}
		
		// Store updated tasks
		return s.storeWorkflowTasks(txn, workflow)
	})
}

// DeleteWorkflow removes a workflow
func (s *BadgerStore) DeleteWorkflow(ctx context.Context, workflowID string) error {
	s.mu.RLock()
	defer s.mu.RUnlock()
	
	if s.closed {
		return fmt.Errorf("store is closed")
	}
	
	return s.db.Update(func(txn *badger.Txn) error {
		key := []byte(workflowPrefix + workflowID)
		
		// Get workflow for index cleanup
		item, err := txn.Get(key)
		if err == badger.ErrKeyNotFound {
			return fmt.Errorf("workflow %s not found", workflowID)
		}
		if err != nil {
			return fmt.Errorf("failed to get workflow: %w", err)
		}
		
		var workflow types.Workflow
		err = item.Value(func(val []byte) error {
			return json.Unmarshal(val, &workflow)
		})
		if err != nil {
			return fmt.Errorf("failed to unmarshal workflow: %w", err)
		}
		
		// Remove metadata indexes
		if err := s.removeWorkflowMetaIndexes(txn, &workflow); err != nil {
			return err
		}
		
		// Remove task entries
		if err := s.removeWorkflowTasks(txn, &workflow); err != nil {
			return err
		}
		
		// Delete workflow
		return txn.Delete(key)
	})
}

// ListWorkflows returns workflows matching the filter
func (s *BadgerStore) ListWorkflows(ctx context.Context, filter map[string]string) ([]*types.Workflow, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	
	if s.closed {
		return nil, fmt.Errorf("store is closed")
	}
	
	var workflows []*types.Workflow
	
	err := s.db.View(func(txn *badger.Txn) error {
		// If filter is empty, scan all workflows
		if len(filter) == 0 {
			return s.scanAllWorkflows(txn, &workflows)
		}
		
		// Use indexes for filtered queries
		return s.scanWorkflowsByFilter(txn, filter, &workflows)
	})
	
	return workflows, err
}

// GetTask retrieves a task from a workflow
func (s *BadgerStore) GetTask(ctx context.Context, workflowID, taskID string) (*types.Task, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	
	if s.closed {
		return nil, fmt.Errorf("store is closed")
	}
	
	var task *types.Task
	
	err := s.db.View(func(txn *badger.Txn) error {
		key := []byte(taskPrefix + workflowID + ":" + taskID)
		
		item, err := txn.Get(key)
		if err == badger.ErrKeyNotFound {
			return fmt.Errorf("task %s not found in workflow %s", taskID, workflowID)
		}
		if err != nil {
			return fmt.Errorf("failed to get task: %w", err)
		}
		
		return item.Value(func(val []byte) error {
			task = &types.Task{}
			return json.Unmarshal(val, task)
		})
	})
	
	return task, err
}

// UpdateTask updates a task within a workflow
func (s *BadgerStore) UpdateTask(ctx context.Context, workflowID string, task *types.Task) error {
	s.mu.RLock()
	defer s.mu.RUnlock()
	
	if s.closed {
		return fmt.Errorf("store is closed")
	}
	
	return s.db.Update(func(txn *badger.Txn) error {
		// Update task in separate key space for efficiency
		taskKey := []byte(taskPrefix + workflowID + ":" + task.ID)
		
		// Check if task exists
		_, err := txn.Get(taskKey)
		if err == badger.ErrKeyNotFound {
			return fmt.Errorf("task %s not found in workflow %s", task.ID, workflowID)
		}
		if err != nil {
			return fmt.Errorf("failed to check task existence: %w", err)
		}
		
		// Serialize task
		taskData, err := json.Marshal(task)
		if err != nil {
			return fmt.Errorf("failed to marshal task: %w", err)
		}
		
		// Store updated task
		if err := txn.Set(taskKey, taskData); err != nil {
			return fmt.Errorf("failed to store task: %w", err)
		}
		
		// Update the task in the workflow as well
		workflowKey := []byte(workflowPrefix + workflowID)
		item, err := txn.Get(workflowKey)
		if err != nil {
			return fmt.Errorf("failed to get workflow: %w", err)
		}
		
		var workflow types.Workflow
		err = item.Value(func(val []byte) error {
			return json.Unmarshal(val, &workflow)
		})
		if err != nil {
			return fmt.Errorf("failed to unmarshal workflow: %w", err)
		}
		
		// Update task in workflow
		for i := range workflow.Tasks {
			if workflow.Tasks[i].ID == task.ID {
				workflow.Tasks[i] = *task
				break
			}
		}
		
		// Serialize and store updated workflow
		workflowData, err := json.Marshal(&workflow)
		if err != nil {
			return fmt.Errorf("failed to marshal workflow: %w", err)
		}
		
		return txn.Set(workflowKey, workflowData)
	})
}

// RecordEvent records a system event
func (s *BadgerStore) RecordEvent(ctx context.Context, event *types.Event) error {
	s.mu.RLock()
	defer s.mu.RUnlock()
	
	if s.closed {
		return fmt.Errorf("store is closed")
	}
	
	return s.db.Update(func(txn *badger.Txn) error {
		// Use timestamp + ID for ordering
		key := []byte(fmt.Sprintf("%s%d:%s", eventPrefix, event.Timestamp.UnixNano(), event.ID))
		
		// Serialize event
		data, err := json.Marshal(event)
		if err != nil {
			return fmt.Errorf("failed to marshal event: %w", err)
		}
		
		// Store event with TTL
		entry := badger.NewEntry(key, data).WithTTL(s.eventTTL)
		if err := txn.SetEntry(entry); err != nil {
			return fmt.Errorf("failed to store event: %w", err)
		}
		
		// Create type and source indexes
		return s.createEventIndexes(txn, event)
	})
}

// GetEvents retrieves events matching the filter
func (s *BadgerStore) GetEvents(ctx context.Context, filter map[string]string, limit int) ([]*types.Event, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	
	if s.closed {
		return nil, fmt.Errorf("store is closed")
	}
	
	var events []*types.Event
	
	err := s.db.View(func(txn *badger.Txn) error {
		return s.scanEventsByFilter(txn, filter, limit, &events)
	})
	
	return events, err
}

// Transaction executes a function within a transaction
func (s *BadgerStore) Transaction(ctx context.Context, fn func(tx StateManager) error) error {
	s.mu.RLock()
	defer s.mu.RUnlock()
	
	if s.closed {
		return fmt.Errorf("store is closed")
	}
	
	return s.db.Update(func(txn *badger.Txn) error {
		// Create a transaction wrapper
		txStore := &badgerTransaction{
			store: s,
			txn:   txn,
		}
		
		return fn(txStore)
	})
}

// Helper methods for index management

func (s *BadgerStore) createAgentLabelIndexes(txn *badger.Txn, agent *types.Agent) error {
	for key, value := range agent.Labels {
		indexKey := []byte(agentLabelPrefix + key + ":" + value + ":" + agent.ID)
		if err := txn.Set(indexKey, []byte(agent.ID)); err != nil {
			return fmt.Errorf("failed to create agent label index: %w", err)
		}
	}
	return nil
}

func (s *BadgerStore) removeAgentLabelIndexes(txn *badger.Txn, agent *types.Agent) error {
	for key, value := range agent.Labels {
		indexKey := []byte(agentLabelPrefix + key + ":" + value + ":" + agent.ID)
		if err := txn.Delete(indexKey); err != nil {
			return fmt.Errorf("failed to remove agent label index: %w", err)
		}
	}
	return nil
}

func (s *BadgerStore) createWorkflowMetaIndexes(txn *badger.Txn, workflow *types.Workflow) error {
	for key, value := range workflow.Metadata {
		indexKey := []byte(workflowMetaPrefix + key + ":" + value + ":" + workflow.ID)
		if err := txn.Set(indexKey, []byte(workflow.ID)); err != nil {
			return fmt.Errorf("failed to create workflow metadata index: %w", err)
		}
	}
	return nil
}

func (s *BadgerStore) removeWorkflowMetaIndexes(txn *badger.Txn, workflow *types.Workflow) error {
	for key, value := range workflow.Metadata {
		indexKey := []byte(workflowMetaPrefix + key + ":" + value + ":" + workflow.ID)
		if err := txn.Delete(indexKey); err != nil {
			return fmt.Errorf("failed to remove workflow metadata index: %w", err)
		}
	}
	return nil
}

func (s *BadgerStore) storeWorkflowTasks(txn *badger.Txn, workflow *types.Workflow) error {
	for _, task := range workflow.Tasks {
		taskKey := []byte(taskPrefix + workflow.ID + ":" + task.ID)
		taskData, err := json.Marshal(&task)
		if err != nil {
			return fmt.Errorf("failed to marshal task: %w", err)
		}
		
		if err := txn.Set(taskKey, taskData); err != nil {
			return fmt.Errorf("failed to store task: %w", err)
		}
	}
	return nil
}

func (s *BadgerStore) removeWorkflowTasks(txn *badger.Txn, workflow *types.Workflow) error {
	for _, task := range workflow.Tasks {
		taskKey := []byte(taskPrefix + workflow.ID + ":" + task.ID)
		if err := txn.Delete(taskKey); err != nil {
			return fmt.Errorf("failed to remove task: %w", err)
		}
	}
	return nil
}

func (s *BadgerStore) createEventIndexes(txn *badger.Txn, event *types.Event) error {
	eventKey := fmt.Sprintf("%d:%s", event.Timestamp.UnixNano(), event.ID)
	
	// Type index
	typeIndexKey := []byte(eventTypePrefix + string(event.Type) + ":" + eventKey)
	if err := txn.Set(typeIndexKey, []byte(event.ID)); err != nil {
		return fmt.Errorf("failed to create event type index: %w", err)
	}
	
	// Source index
	sourceIndexKey := []byte(eventSourcePrefix + event.Source + ":" + eventKey)
	if err := txn.Set(sourceIndexKey, []byte(event.ID)); err != nil {
		return fmt.Errorf("failed to create event source index: %w", err)
	}
	
	return nil
}

// Helper methods for scanning

func (s *BadgerStore) scanAllAgents(txn *badger.Txn, agents *[]*types.Agent) error {
	opts := badger.DefaultIteratorOptions
	opts.Prefix = []byte(agentPrefix)
	
	it := txn.NewIterator(opts)
	defer it.Close()
	
	for it.Rewind(); it.Valid(); it.Next() {
		item := it.Item()
		err := item.Value(func(val []byte) error {
			var agent types.Agent
			if err := json.Unmarshal(val, &agent); err != nil {
				return err
			}
			*agents = append(*agents, &agent)
			return nil
		})
		if err != nil {
			return err
		}
	}
	
	return nil
}

func (s *BadgerStore) scanAgentsByFilter(txn *badger.Txn, filter map[string]string, agents *[]*types.Agent) error {
	// Use label indexes for filtering
	agentIDs := make(map[string]bool)
	first := true
	
	for key, value := range filter {
		prefix := []byte(agentLabelPrefix + key + ":" + value + ":")
		opts := badger.DefaultIteratorOptions
		opts.Prefix = prefix
		
		it := txn.NewIterator(opts)
		currentIDs := make(map[string]bool)
		
		for it.Rewind(); it.Valid(); it.Next() {
			item := it.Item()
			err := item.Value(func(val []byte) error {
				currentIDs[string(val)] = true
				return nil
			})
			if err != nil {
				it.Close()
				return err
			}
		}
		it.Close()
		
		if first {
			agentIDs = currentIDs
			first = false
		} else {
			// Intersection
			for id := range agentIDs {
				if !currentIDs[id] {
					delete(agentIDs, id)
				}
			}
		}
		
		if len(agentIDs) == 0 {
			break
		}
	}
	
	// Fetch agents by IDs
	for agentID := range agentIDs {
		key := []byte(agentPrefix + agentID)
		item, err := txn.Get(key)
		if err != nil {
			continue // Skip if not found
		}
		
		err = item.Value(func(val []byte) error {
			var agent types.Agent
			if err := json.Unmarshal(val, &agent); err != nil {
				return err
			}
			*agents = append(*agents, &agent)
			return nil
		})
		if err != nil {
			return err
		}
	}
	
	return nil
}

func (s *BadgerStore) scanAllWorkflows(txn *badger.Txn, workflows *[]*types.Workflow) error {
	opts := badger.DefaultIteratorOptions
	opts.Prefix = []byte(workflowPrefix)
	
	it := txn.NewIterator(opts)
	defer it.Close()
	
	for it.Rewind(); it.Valid(); it.Next() {
		item := it.Item()
		err := item.Value(func(val []byte) error {
			var workflow types.Workflow
			if err := json.Unmarshal(val, &workflow); err != nil {
				return err
			}
			*workflows = append(*workflows, &workflow)
			return nil
		})
		if err != nil {
			return err
		}
	}
	
	return nil
}

func (s *BadgerStore) scanWorkflowsByFilter(txn *badger.Txn, filter map[string]string, workflows *[]*types.Workflow) error {
	// Use metadata indexes for filtering
	workflowIDs := make(map[string]bool)
	first := true
	
	for key, value := range filter {
		prefix := []byte(workflowMetaPrefix + key + ":" + value + ":")
		opts := badger.DefaultIteratorOptions
		opts.Prefix = prefix
		
		it := txn.NewIterator(opts)
		currentIDs := make(map[string]bool)
		
		for it.Rewind(); it.Valid(); it.Next() {
			item := it.Item()
			err := item.Value(func(val []byte) error {
				currentIDs[string(val)] = true
				return nil
			})
			if err != nil {
				it.Close()
				return err
			}
		}
		it.Close()
		
		if first {
			workflowIDs = currentIDs
			first = false
		} else {
			// Intersection
			for id := range workflowIDs {
				if !currentIDs[id] {
					delete(workflowIDs, id)
				}
			}
		}
		
		if len(workflowIDs) == 0 {
			break
		}
	}
	
	// Fetch workflows by IDs
	for workflowID := range workflowIDs {
		key := []byte(workflowPrefix + workflowID)
		item, err := txn.Get(key)
		if err != nil {
			continue // Skip if not found
		}
		
		err = item.Value(func(val []byte) error {
			var workflow types.Workflow
			if err := json.Unmarshal(val, &workflow); err != nil {
				return err
			}
			*workflows = append(*workflows, &workflow)
			return nil
		})
		if err != nil {
			return err
		}
	}
	
	return nil
}

func (s *BadgerStore) scanEventsByFilter(txn *badger.Txn, filter map[string]string, limit int, events *[]*types.Event) error {
	var prefix []byte
	
	// Determine which index to use
	if eventType, ok := filter["type"]; ok {
		prefix = []byte(eventTypePrefix + eventType + ":")
	} else if source, ok := filter["source"]; ok {
		prefix = []byte(eventSourcePrefix + source + ":")
	} else {
		// No filter or unsupported filter, scan all events
		prefix = []byte(eventPrefix)
	}
	
	opts := badger.DefaultIteratorOptions
	opts.Prefix = prefix
	opts.Reverse = true // Get newest events first
	
	it := txn.NewIterator(opts)
	defer it.Close()
	
	count := 0
	for it.Rewind(); it.Valid() && (limit <= 0 || count < limit); it.Next() {
		item := it.Item()
		
		if strings.HasPrefix(string(item.Key()), eventPrefix) {
			// Direct event scan
			err := item.Value(func(val []byte) error {
				var event types.Event
				if err := json.Unmarshal(val, &event); err != nil {
					return err
				}
				
				// Apply additional filters
				if s.matchesEventFilter(&event, filter) {
					*events = append(*events, &event)
					count++
				}
				return nil
			})
			if err != nil {
				return err
			}
		} else {
			// Index scan - get event ID and fetch event
			err := item.Value(func(val []byte) error {
				// Extract timestamp and ID from key
				keyStr := string(item.Key())
				parts := strings.Split(keyStr, ":")
				if len(parts) < 3 {
					return nil // Skip malformed key
				}
				
				eventKey := []byte(eventPrefix + parts[len(parts)-2] + ":" + parts[len(parts)-1])
				eventItem, err := txn.Get(eventKey)
				if err != nil {
					return nil // Skip if event not found
				}
				
				return eventItem.Value(func(eventVal []byte) error {
					var event types.Event
					if err := json.Unmarshal(eventVal, &event); err != nil {
						return err
					}
					
					// Apply additional filters
					if s.matchesEventFilter(&event, filter) {
						*events = append(*events, &event)
						count++
					}
					return nil
				})
			})
			if err != nil {
				return err
			}
		}
	}
	
	return nil
}

func (s *BadgerStore) matchesEventFilter(event *types.Event, filter map[string]string) bool {
	for key, value := range filter {
		switch key {
		case "type":
			if string(event.Type) != value {
				return false
			}
		case "source":
			if event.Source != value {
				return false
			}
		default:
			// Check in event data
			if dataValue, ok := event.Data[key]; ok {
				if fmt.Sprintf("%v", dataValue) != value {
					return false
				}
			} else {
				return false
			}
		}
	}
	return true
}

// Background garbage collection
func (s *BadgerStore) runGC() {
	ticker := time.NewTicker(5 * time.Minute)
	defer ticker.Stop()
	
	for {
		select {
		case <-ticker.C:
			s.mu.RLock()
			if s.closed {
				s.mu.RUnlock()
				return
			}
			s.mu.RUnlock()
			
			err := s.db.RunValueLogGC(0.7)
			if err != nil && err != badger.ErrNoRewrite {
				log.Printf("BadgerDB GC error: %v", err)
			}
		}
	}
}

// badgerTransaction wraps a BadgerDB transaction to implement StateManager
type badgerTransaction struct {
	store *BadgerStore
	txn   *badger.Txn
}

// Transaction methods (delegate to store with the existing transaction)
func (tx *badgerTransaction) CreateAgent(ctx context.Context, agent *types.Agent) error {
	key := []byte(agentPrefix + agent.ID)
	
	// Check if agent already exists
	_, err := tx.txn.Get(key)
	if err == nil {
		return fmt.Errorf("agent %s already exists", agent.ID)
	}
	if err != badger.ErrKeyNotFound {
		return fmt.Errorf("failed to check agent existence: %w", err)
	}
	
	// Serialize agent
	data, err := json.Marshal(agent)
	if err != nil {
		return fmt.Errorf("failed to marshal agent: %w", err)
	}
	
	// Store agent
	if err := tx.txn.Set(key, data); err != nil {
		return fmt.Errorf("failed to store agent: %w", err)
	}
	
	// Create label indexes
	return tx.store.createAgentLabelIndexes(tx.txn, agent)
}

func (tx *badgerTransaction) GetAgent(ctx context.Context, agentID string) (*types.Agent, error) {
	key := []byte(agentPrefix + agentID)
	
	item, err := tx.txn.Get(key)
	if err == badger.ErrKeyNotFound {
		return nil, fmt.Errorf("agent %s not found", agentID)
	}
	if err != nil {
		return nil, fmt.Errorf("failed to get agent: %w", err)
	}
	
	var agent *types.Agent
	err = item.Value(func(val []byte) error {
		agent = &types.Agent{}
		return json.Unmarshal(val, agent)
	})
	
	return agent, err
}

func (tx *badgerTransaction) UpdateAgent(ctx context.Context, agent *types.Agent) error {
	key := []byte(agentPrefix + agent.ID)
	
	// Check if agent exists and get old version
	item, err := tx.txn.Get(key)
	if err == badger.ErrKeyNotFound {
		return fmt.Errorf("agent %s not found", agent.ID)
	}
	if err != nil {
		return fmt.Errorf("failed to check agent existence: %w", err)
	}
	
	// Get old agent for index cleanup
	var oldAgent types.Agent
	err = item.Value(func(val []byte) error {
		return json.Unmarshal(val, &oldAgent)
	})
	if err != nil {
		return fmt.Errorf("failed to unmarshal old agent: %w", err)
	}
	
	// Remove old label indexes
	if err := tx.store.removeAgentLabelIndexes(tx.txn, &oldAgent); err != nil {
		return err
	}
	
	// Serialize updated agent
	data, err := json.Marshal(agent)
	if err != nil {
		return fmt.Errorf("failed to marshal agent: %w", err)
	}
	
	// Store updated agent
	if err := tx.txn.Set(key, data); err != nil {
		return fmt.Errorf("failed to store agent: %w", err)
	}
	
	// Create new label indexes
	return tx.store.createAgentLabelIndexes(tx.txn, agent)
}

func (tx *badgerTransaction) DeleteAgent(ctx context.Context, agentID string) error {
	key := []byte(agentPrefix + agentID)
	
	// Get agent for index cleanup
	item, err := tx.txn.Get(key)
	if err == badger.ErrKeyNotFound {
		return fmt.Errorf("agent %s not found", agentID)
	}
	if err != nil {
		return fmt.Errorf("failed to get agent: %w", err)
	}
	
	var agent types.Agent
	err = item.Value(func(val []byte) error {
		return json.Unmarshal(val, &agent)
	})
	if err != nil {
		return fmt.Errorf("failed to unmarshal agent: %w", err)
	}
	
	// Remove label indexes
	if err := tx.store.removeAgentLabelIndexes(tx.txn, &agent); err != nil {
		return err
	}
	
	// Delete agent
	return tx.txn.Delete(key)
}

func (tx *badgerTransaction) ListAgents(ctx context.Context, filter map[string]string) ([]*types.Agent, error) {
	var agents []*types.Agent
	
	// If filter is empty, scan all agents
	if len(filter) == 0 {
		return agents, tx.store.scanAllAgents(tx.txn, &agents)
	}
	
	// Use indexes for filtered queries
	return agents, tx.store.scanAgentsByFilter(tx.txn, filter, &agents)
}

func (tx *badgerTransaction) CreateWorkflow(ctx context.Context, workflow *types.Workflow) error {
	key := []byte(workflowPrefix + workflow.ID)
	
	// Check if workflow already exists
	_, err := tx.txn.Get(key)
	if err == nil {
		return fmt.Errorf("workflow %s already exists", workflow.ID)
	}
	if err != badger.ErrKeyNotFound {
		return fmt.Errorf("failed to check workflow existence: %w", err)
	}
	
	// Serialize workflow
	data, err := json.Marshal(workflow)
	if err != nil {
		return fmt.Errorf("failed to marshal workflow: %w", err)
	}
	
	// Store workflow
	if err := tx.txn.Set(key, data); err != nil {
		return fmt.Errorf("failed to store workflow: %w", err)
	}
	
	// Create metadata indexes
	if err := tx.store.createWorkflowMetaIndexes(tx.txn, workflow); err != nil {
		return err
	}
	
	// Store individual tasks
	return tx.store.storeWorkflowTasks(tx.txn, workflow)
}

func (tx *badgerTransaction) GetWorkflow(ctx context.Context, workflowID string) (*types.Workflow, error) {
	key := []byte(workflowPrefix + workflowID)
	
	item, err := tx.txn.Get(key)
	if err == badger.ErrKeyNotFound {
		return nil, fmt.Errorf("workflow %s not found", workflowID)
	}
	if err != nil {
		return nil, fmt.Errorf("failed to get workflow: %w", err)
	}
	
	var workflow *types.Workflow
	err = item.Value(func(val []byte) error {
		workflow = &types.Workflow{}
		return json.Unmarshal(val, workflow)
	})
	
	return workflow, err
}

func (tx *badgerTransaction) UpdateWorkflow(ctx context.Context, workflow *types.Workflow) error {
	key := []byte(workflowPrefix + workflow.ID)
	
	// Check if workflow exists and get old version
	item, err := tx.txn.Get(key)
	if err == badger.ErrKeyNotFound {
		return fmt.Errorf("workflow %s not found", workflow.ID)
	}
	if err != nil {
		return fmt.Errorf("failed to check workflow existence: %w", err)
	}
	
	// Get old workflow for index cleanup
	var oldWorkflow types.Workflow
	err = item.Value(func(val []byte) error {
		return json.Unmarshal(val, &oldWorkflow)
	})
	if err != nil {
		return fmt.Errorf("failed to unmarshal old workflow: %w", err)
	}
	
	// Remove old metadata indexes
	if err := tx.store.removeWorkflowMetaIndexes(tx.txn, &oldWorkflow); err != nil {
		return err
	}
	
	// Remove old task entries
	if err := tx.store.removeWorkflowTasks(tx.txn, &oldWorkflow); err != nil {
		return err
	}
	
	// Serialize updated workflow
	data, err := json.Marshal(workflow)
	if err != nil {
		return fmt.Errorf("failed to marshal workflow: %w", err)
	}
	
	// Store updated workflow
	if err := tx.txn.Set(key, data); err != nil {
		return fmt.Errorf("failed to store workflow: %w", err)
	}
	
	// Create new metadata indexes
	if err := tx.store.createWorkflowMetaIndexes(tx.txn, workflow); err != nil {
		return err
	}
	
	// Store updated tasks
	return tx.store.storeWorkflowTasks(tx.txn, workflow)
}

func (tx *badgerTransaction) DeleteWorkflow(ctx context.Context, workflowID string) error {
	key := []byte(workflowPrefix + workflowID)
	
	// Get workflow for index cleanup
	item, err := tx.txn.Get(key)
	if err == badger.ErrKeyNotFound {
		return fmt.Errorf("workflow %s not found", workflowID)
	}
	if err != nil {
		return fmt.Errorf("failed to get workflow: %w", err)
	}
	
	var workflow types.Workflow
	err = item.Value(func(val []byte) error {
		return json.Unmarshal(val, &workflow)
	})
	if err != nil {
		return fmt.Errorf("failed to unmarshal workflow: %w", err)
	}
	
	// Remove metadata indexes
	if err := tx.store.removeWorkflowMetaIndexes(tx.txn, &workflow); err != nil {
		return err
	}
	
	// Remove task entries
	if err := tx.store.removeWorkflowTasks(tx.txn, &workflow); err != nil {
		return err
	}
	
	// Delete workflow
	return tx.txn.Delete(key)
}

func (tx *badgerTransaction) ListWorkflows(ctx context.Context, filter map[string]string) ([]*types.Workflow, error) {
	var workflows []*types.Workflow
	
	// If filter is empty, scan all workflows
	if len(filter) == 0 {
		return workflows, tx.store.scanAllWorkflows(tx.txn, &workflows)
	}
	
	// Use indexes for filtered queries
	return workflows, tx.store.scanWorkflowsByFilter(tx.txn, filter, &workflows)
}

func (tx *badgerTransaction) GetTask(ctx context.Context, workflowID, taskID string) (*types.Task, error) {
	key := []byte(taskPrefix + workflowID + ":" + taskID)
	
	item, err := tx.txn.Get(key)
	if err == badger.ErrKeyNotFound {
		return nil, fmt.Errorf("task %s not found in workflow %s", taskID, workflowID)
	}
	if err != nil {
		return nil, fmt.Errorf("failed to get task: %w", err)
	}
	
	var task *types.Task
	err = item.Value(func(val []byte) error {
		task = &types.Task{}
		return json.Unmarshal(val, task)
	})
	
	return task, err
}

func (tx *badgerTransaction) UpdateTask(ctx context.Context, workflowID string, task *types.Task) error {
	// Update task in separate key space for efficiency
	taskKey := []byte(taskPrefix + workflowID + ":" + task.ID)
	
	// Check if task exists
	_, err := tx.txn.Get(taskKey)
	if err == badger.ErrKeyNotFound {
		return fmt.Errorf("task %s not found in workflow %s", task.ID, workflowID)
	}
	if err != nil {
		return fmt.Errorf("failed to check task existence: %w", err)
	}
	
	// Serialize task
	taskData, err := json.Marshal(task)
	if err != nil {
		return fmt.Errorf("failed to marshal task: %w", err)
	}
	
	// Store updated task
	if err := tx.txn.Set(taskKey, taskData); err != nil {
		return fmt.Errorf("failed to store task: %w", err)
	}
	
	// Update the task in the workflow as well
	workflowKey := []byte(workflowPrefix + workflowID)
	item, err := tx.txn.Get(workflowKey)
	if err != nil {
		return fmt.Errorf("failed to get workflow: %w", err)
	}
	
	var workflow types.Workflow
	err = item.Value(func(val []byte) error {
		return json.Unmarshal(val, &workflow)
	})
	if err != nil {
		return fmt.Errorf("failed to unmarshal workflow: %w", err)
	}
	
	// Update task in workflow
	for i := range workflow.Tasks {
		if workflow.Tasks[i].ID == task.ID {
			workflow.Tasks[i] = *task
			break
		}
	}
	
	// Serialize and store updated workflow
	workflowData, err := json.Marshal(&workflow)
	if err != nil {
		return fmt.Errorf("failed to marshal workflow: %w", err)
	}
	
	return tx.txn.Set(workflowKey, workflowData)
}

func (tx *badgerTransaction) RecordEvent(ctx context.Context, event *types.Event) error {
	// Use timestamp + ID for ordering
	key := []byte(fmt.Sprintf("%s%d:%s", eventPrefix, event.Timestamp.UnixNano(), event.ID))
	
	// Serialize event
	data, err := json.Marshal(event)
	if err != nil {
		return fmt.Errorf("failed to marshal event: %w", err)
	}
	
	// Store event with TTL
	entry := badger.NewEntry(key, data).WithTTL(tx.store.eventTTL)
	if err := tx.txn.SetEntry(entry); err != nil {
		return fmt.Errorf("failed to store event: %w", err)
	}
	
	// Create type and source indexes
	return tx.store.createEventIndexes(tx.txn, event)
}

func (tx *badgerTransaction) GetEvents(ctx context.Context, filter map[string]string, limit int) ([]*types.Event, error) {
	var events []*types.Event
	return events, tx.store.scanEventsByFilter(tx.txn, filter, limit, &events)
}

func (tx *badgerTransaction) Transaction(ctx context.Context, fn func(tx StateManager) error) error {
	// Nested transactions not supported, just execute the function
	return fn(tx)
}

// BadgerStoreFactory creates BadgerDB store instances
type BadgerStoreFactory struct{}

// Create creates a new BadgerDB store instance
func (f *BadgerStoreFactory) Create(config Config) (Store, error) {
	badgerConfig := BadgerStoreConfig{
		Path: config.Path,
	}
	
	// Parse options
	if config.Options != nil {
		if eventTTL, ok := config.Options["event_ttl"]; ok {
			if ttlStr, ok := eventTTL.(string); ok {
				if duration, err := time.ParseDuration(ttlStr); err == nil {
					badgerConfig.EventTTL = duration
				}
			}
		}
		
		// Set BadgerDB options
		if config.Path != "" {
			badgerConfig.Options = badger.DefaultOptions(config.Path)
			badgerConfig.Options.Logger = nil // Disable logging by default
			
			if logLevel, ok := config.Options["log_level"]; ok {
				if level, ok := logLevel.(string); ok && level != "none" {
					// Note: In BadgerDB v4, logging is handled differently
					// For now, we keep logging disabled by default
				}
			}
		}
	}
	
	return NewBadgerStore(badgerConfig)
}