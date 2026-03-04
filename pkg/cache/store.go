package cache

import "sync"

// InstanceState models cloud instance lifecycle for NodeGroupNodes responses.
type InstanceState int

const (
	InstanceStateUnspecified InstanceState = iota
	InstanceStateRunning
	InstanceStateCreating
	InstanceStateDeleting
)

// Instance is a cached cloud instance record.
type Instance struct {
	ID    string
	State InstanceState
}

// NodeGroup is a cached representation of an Auto Scaling Group's structural metadata.
type NodeGroup struct {
	ID         string
	MinSize    int
	MaxSize    int
	TargetSize int
}

// Snapshot is an immutable logical view used to replace cache atomically.
type Snapshot struct {
	NodeGroups            map[string]NodeGroup
	NodeGroupByProviderID map[string]string
	NodeGroupByNodeName   map[string]string
	InstancesByNodeGroup  map[string][]Instance
}

// Store keeps nodegroup/node mappings for read RPCs.
type Store struct {
	mu          sync.RWMutex
	snap        Snapshot
	initialized bool
}

// NewStore creates an empty cache store.
func NewStore() *Store {
	return &Store{
		snap: Snapshot{
			NodeGroups:            map[string]NodeGroup{},
			NodeGroupByProviderID: map[string]string{},
			NodeGroupByNodeName:   map[string]string{},
			InstancesByNodeGroup:  map[string][]Instance{},
		},
	}
}

// Replace atomically swaps the cache snapshot.
func (s *Store) Replace(next Snapshot) {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.initialized = true
	s.snap = Snapshot{
		NodeGroups:            copyNodeGroupsMap(next.NodeGroups),
		NodeGroupByProviderID: copyStringMap(next.NodeGroupByProviderID),
		NodeGroupByNodeName:   copyStringMap(next.NodeGroupByNodeName),
		InstancesByNodeGroup:  copyInstancesMap(next.InstancesByNodeGroup),
	}
}

// IsInitialized returns true if the cache has been populated at least once.
func (s *Store) IsInitialized() bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.initialized
}

// NodeGroupIDForNode resolves nodegroup id using providerID first, then node name.
func (s *Store) NodeGroupIDForNode(providerID, nodeName string) (string, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if providerID != "" {
		if id, ok := s.snap.NodeGroupByProviderID[providerID]; ok {
			return id, true
		}
		if instanceID := extractInstanceID(providerID); instanceID != "" {
			if id, ok := s.snap.NodeGroupByProviderID[instanceID]; ok {
				return id, true
			}
		}
	}
	if nodeName != "" {
		if id, ok := s.snap.NodeGroupByNodeName[nodeName]; ok {
			return id, true
		}
	}
	return "", false
}

func extractInstanceID(providerID string) string {
	for i := len(providerID) - 1; i >= 0; i-- {
		if providerID[i] == '/' {
			if i == len(providerID)-1 {
				return ""
			}
			return providerID[i+1:]
		}
	}
	return providerID
}

// InstancesForNodeGroup returns a copy of cached instances for a nodegroup.
func (s *Store) InstancesForNodeGroup(nodeGroupID string) []Instance {
	s.mu.RLock()
	defer s.mu.RUnlock()

	in := s.snap.InstancesByNodeGroup[nodeGroupID]
	out := make([]Instance, len(in))
	copy(out, in)
	return out
}

// NodeGroup returns a copy of the cached nodegroup metadata.
func (s *Store) NodeGroup(nodeGroupID string) (NodeGroup, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	ng, ok := s.snap.NodeGroups[nodeGroupID]
	return ng, ok
}

func copyStringMap(in map[string]string) map[string]string {
	out := make(map[string]string, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}

func copyInstancesMap(in map[string][]Instance) map[string][]Instance {
	out := make(map[string][]Instance, len(in))
	for k, v := range in {
		vv := make([]Instance, len(v))
		copy(vv, v)
		out[k] = vv
	}
	return out
}

func copyNodeGroupsMap(in map[string]NodeGroup) map[string]NodeGroup {
	out := make(map[string]NodeGroup, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}
