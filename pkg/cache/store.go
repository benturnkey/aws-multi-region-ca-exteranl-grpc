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

// Snapshot is an immutable logical view used to replace cache atomically.
type Snapshot struct {
	NodeGroupByProviderID map[string]string
	NodeGroupByNodeName   map[string]string
	InstancesByNodeGroup  map[string][]Instance
}

// Store keeps nodegroup/node mappings for read RPCs.
type Store struct {
	mu   sync.RWMutex
	snap Snapshot
}

// NewStore creates an empty cache store.
func NewStore() *Store {
	return &Store{
		snap: Snapshot{
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

	s.snap = Snapshot{
		NodeGroupByProviderID: copyStringMap(next.NodeGroupByProviderID),
		NodeGroupByNodeName:   copyStringMap(next.NodeGroupByNodeName),
		InstancesByNodeGroup:  copyInstancesMap(next.InstancesByNodeGroup),
	}
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
