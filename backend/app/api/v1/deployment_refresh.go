package v1

import (
	"context"
	"log"
	"sync"
	"time"

	"github.com/z46-dev/organesson/backend/app/api/common"
	"github.com/z46-dev/organesson/backend/domain"
	"github.com/z46-dev/organesson/backend/proxmox"
)

const (
	deploymentRefreshInterval = 30 * time.Second
	deploymentRefreshTimeout  = 4 * time.Minute
	deploymentRefreshParallel = 2
)

type (
	deploymentRefreshTarget struct {
		actorID    int
		resourceID int
		generation uint64
		request    proxmox.VMReadRequest
	}

	deploymentRefreshState struct {
		running      bool
		pending      map[int]deploymentRefreshTarget
		active       map[int]bool
		lastAttempt  map[int]time.Time
		generation   map[int]uint64
		lastChecked  time.Time
		lastActivity time.Time
	}

	deploymentRefreshManager struct {
		mutex       sync.Mutex
		states      map[int]*deploymentRefreshState
		concurrency chan struct{}
	}
)

// newDeploymentRefreshManager coalesces live refreshes and caps cluster-wide refresh work.
func newDeploymentRefreshManager() (manager *deploymentRefreshManager) {
	manager = &deploymentRefreshManager{
		states:      make(map[int]*deploymentRefreshState),
		concurrency: make(chan struct{}, deploymentRefreshParallel),
	}
	return
}

// enqueue starts or joins an asynchronous refresh for the caller's visible VMs.
func (manager *deploymentRefreshManager) enqueue(services common.Services, actorID int, deploymentID int, resources []*domain.ResourceSummary) (checkedAt *time.Time, refreshing bool) {
	if services.Proxmox == nil || !services.Proxmox.Configured() {
		return
	}
	var now time.Time = time.Now()
	manager.mutex.Lock()
	defer manager.mutex.Unlock()
	manager.prune(now)
	var state *deploymentRefreshState = manager.states[deploymentID]
	if state == nil {
		state = &deploymentRefreshState{
			pending:     make(map[int]deploymentRefreshTarget),
			active:      make(map[int]bool),
			lastAttempt: make(map[int]time.Time),
			generation:  make(map[int]uint64),
		}
		manager.states[deploymentID] = state
	}
	for _, resource := range resources {
		if resource == nil || resource.Kind != "virtual_machine" || resource.ExternalID == "" || resource.OperationKey == "" {
			continue
		}
		if state.active[resource.ID] {
			continue
		}
		if _, exists := state.pending[resource.ID]; exists {
			continue
		}
		if lastAttempt, exists := state.lastAttempt[resource.ID]; exists && now.Sub(lastAttempt) < deploymentRefreshInterval {
			continue
		}
		state.pending[resource.ID] = deploymentRefreshTarget{
			actorID:    actorID,
			resourceID: resource.ID,
			generation: state.generation[resource.ID],
			request: proxmox.VMReadRequest{
				Node: resource.ExternalNode, VMID: resource.ExternalID, OperationKey: resource.OperationKey,
			},
		}
		state.lastAttempt[resource.ID] = now
	}
	state.lastActivity = now
	if !state.running && len(state.pending) > 0 {
		state.running = true
		go manager.run(services, deploymentID)
	}
	if !state.lastChecked.IsZero() {
		var value time.Time = state.lastChecked
		checkedAt = &value
	}
	refreshing = state.running
	return
}

// run drains coalesced batches until no newly visible VMs remain pending.
func (manager *deploymentRefreshManager) run(services common.Services, deploymentID int) {
	for {
		manager.mutex.Lock()
		var state *deploymentRefreshState = manager.states[deploymentID]
		if state == nil || len(state.pending) == 0 {
			if state != nil {
				state.running = false
				state.lastActivity = time.Now()
			}
			manager.mutex.Unlock()
			return
		}
		var targets []deploymentRefreshTarget = make([]deploymentRefreshTarget, 0, len(state.pending))
		for resourceID, target := range state.pending {
			targets = append(targets, target)
			state.active[resourceID] = true
			delete(state.pending, resourceID)
		}
		manager.mutex.Unlock()

		manager.refreshBatch(services, deploymentID, targets)
	}
}

// refreshBatch reads Proxmox in bounded parallelism and persists each still-current placement.
func (manager *deploymentRefreshManager) refreshBatch(services common.Services, deploymentID int, targets []deploymentRefreshTarget) {
	var ctx context.Context
	var cancel context.CancelFunc
	ctx, cancel = context.WithTimeout(context.Background(), deploymentRefreshTimeout)
	defer cancel()
	select {
	case manager.concurrency <- struct{}{}:
	case <-ctx.Done():
		manager.completeBatch(deploymentID, targets, time.Now())
		log.Printf("deployment %d live VM refresh did not start before its timeout: %v", deploymentID, ctx.Err())
		return
	}
	defer func() { <-manager.concurrency }()

	var requests []proxmox.VMReadRequest = make([]proxmox.VMReadRequest, len(targets))
	for index, target := range targets {
		requests[index] = target.request
	}
	var results []proxmox.VMReadResult
	var err error
	if results, err = services.Proxmox.ReadVMs(ctx, requests); err != nil {
		manager.completeBatch(deploymentID, targets, time.Now())
		log.Printf("deployment %d live VM batch refresh failed: %v", deploymentID, err)
		return
	}
	for index, target := range targets {
		if index >= len(results) {
			log.Printf("deployment %d VM resource %d was not returned by the live refresh", deploymentID, target.resourceID)
			continue
		}
		if results[index].Err != nil {
			log.Printf("deployment %d VM resource %d live refresh failed: %v", deploymentID, target.resourceID, results[index].Err)
			continue
		}
		if err = manager.recordIfCurrent(services, deploymentID, target, results[index].Placement); err != nil {
			log.Printf("deployment %d VM resource %d placement could not be saved: %v", deploymentID, target.resourceID, err)
		}
	}
	manager.completeBatch(deploymentID, targets, time.Now())
}

// recordIfCurrent prevents an older background read from overwriting a newer power operation.
func (manager *deploymentRefreshManager) recordIfCurrent(services common.Services, deploymentID int, target deploymentRefreshTarget, placement proxmox.VMPlacement) (err error) {
	manager.mutex.Lock()
	defer manager.mutex.Unlock()
	var state *deploymentRefreshState = manager.states[deploymentID]
	if state == nil || state.generation[target.resourceID] != target.generation {
		return
	}
	_, err = services.Domain.RecordLiveVMPlacement(target.actorID, target.resourceID, placement)
	return
}

// markResourceChanged invalidates any in-flight observation after an explicit control action.
func (manager *deploymentRefreshManager) markResourceChanged(deploymentID int, resourceID int) {
	manager.mutex.Lock()
	defer manager.mutex.Unlock()
	var state *deploymentRefreshState = manager.states[deploymentID]
	if state != nil {
		state.generation[resourceID]++
		delete(state.pending, resourceID)
	}
}

// completeBatch releases the coalescing locks and updates the exposed refresh timestamp.
func (manager *deploymentRefreshManager) completeBatch(deploymentID int, targets []deploymentRefreshTarget, checkedAt time.Time) {
	manager.mutex.Lock()
	defer manager.mutex.Unlock()
	var state *deploymentRefreshState = manager.states[deploymentID]
	if state == nil {
		return
	}
	for _, target := range targets {
		delete(state.active, target.resourceID)
	}
	state.lastChecked = checkedAt
	state.lastActivity = checkedAt
}

// prune removes old completed deployment entries when the manager grows beyond its normal working set.
func (manager *deploymentRefreshManager) prune(now time.Time) {
	if len(manager.states) < 256 {
		return
	}
	for deploymentID, state := range manager.states {
		if !state.running && now.Sub(state.lastActivity) > time.Hour {
			delete(manager.states, deploymentID)
		}
	}
}
