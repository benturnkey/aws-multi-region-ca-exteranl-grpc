# Cluster Autoscaler External gRPC AWS Flow

This diagram shows how Kubernetes Cluster Autoscaler (CA) interacts with this service and how this service fans out to AWS regional APIs.

```mermaid
sequenceDiagram
    autonumber
    participant Pod as Unschedulable Pod
    participant Scheduler as kube-scheduler
    participant KubeAPI as kube-apiserver
    participant CA as Cluster Autoscaler
    participant Ext as externalgrpc service (this repo)
    participant Cache as In-memory snapshot cache
    participant ASG1 as AWS ASG API (us-east-1)
    participant ASG2 as AWS ASG API (us-west-2)

    Pod->>Scheduler: Scheduling attempt
    Scheduler->>KubeAPI: Pod marked Unschedulable
    CA->>KubeAPI: Watch Pods/Nodes and detect unschedulable workload

    CA->>Ext: Refresh()
    Ext->>ASG1: DescribeAutoScalingGroups (tags + explicit names)
    Ext->>ASG2: DescribeAutoScalingGroups (tags + explicit names)
    ASG1-->>Ext: ASGs, desired/min/max, instances
    ASG2-->>Ext: ASGs, desired/min/max, instances
    Ext->>Cache: Replace(snapshot)

    CA->>Ext: NodeGroups(), NodeGroupNodes(), NodeGroupForNode(), TargetSize(), GetOptions()
    Ext->>Cache: Read nodegroup + instance state
    Ext-->>CA: region/asg-name groups and current state

    Note over CA: Scale-up decision: choose nodegroup and delta > 0
    CA->>Ext: NodeGroupIncreaseSize(id, +delta)
    Ext->>Cache: Read cached target size for nodegroup
    Ext->>ASG1: SetDesiredCapacity(asg, cachedTarget + delta)
    ASG1-->>Ext: ACK
    Ext-->>CA: Success
    ASG1->>KubeAPI: New instances register as nodes
    KubeAPI-->>CA: Capacity increased, pending pods become schedulable

    Note over CA: Scale-down decision: unneeded/empty node found from Kube API state
    CA->>KubeAPI: Cordon + drain/evict node (CA core behavior)
    CA->>Ext: NodeGroupDeleteNodes(id, [node providerID])
    Ext->>ASG1: TerminateInstanceInAutoScalingGroup(instance, decrementDesired=true)
    ASG1-->>Ext: ACK
    Ext-->>CA: Success

    opt Alternative shrink path
        CA->>Ext: NodeGroupDecreaseTargetSize(id, negative delta)
        Ext->>Cache: Read cached target size
        Ext->>ASG1: SetDesiredCapacity(asg, cachedTarget + delta)
        ASG1-->>Ext: ACK
    end

    CA->>Ext: Refresh() on next loop
    Ext->>ASG1: DescribeAutoScalingGroups
    Ext->>ASG2: DescribeAutoScalingGroups
    Ext->>Cache: Replace(snapshot with new desired/current)
```

## Process Description

1. Trigger and observation:
   - A pod cannot be scheduled, and `kube-scheduler` records this via `kube-apiserver`.
   - Cluster Autoscaler watches API state (pods and nodes) and starts a scale evaluation loop.

2. External provider state sync:
   - CA calls `Refresh()` on this service.
   - This service queries each configured AWS region (`DescribeAutoScalingGroups`) and rebuilds an in-memory snapshot cache keyed by `region/asg-name`.

3. Scale-up path:
   - CA reads nodegroup inventory and state through gRPC calls (`NodeGroups`, `NodeGroupNodes`, `NodeGroupTargetSize`, etc.).
   - CA chooses a nodegroup and sends `NodeGroupIncreaseSize` with positive delta.
   - This service routes by `region/asg-name` and calls regional AWS ASG `SetDesiredCapacity`.
   - AWS launches instances, nodes register to the cluster, and pending pods become schedulable.

4. Scale-down decision and propagation:
   - CA decides scale-down from Kubernetes state (for example, low-utilization or empty nodes after scale-down timers and safety checks).
   - CA typically drains/evicts via Kubernetes APIs, then calls `NodeGroupDeleteNodes`.
   - This service calls AWS `TerminateInstanceInAutoScalingGroup` with `ShouldDecrementDesiredCapacity=true`, so instance count and desired capacity both drop.
   - CA may also call `NodeGroupDecreaseTargetSize` (negative delta), which this service maps to AWS `SetDesiredCapacity`.

5. Eventual consistency:
   - This service is cache-backed for reads and size math.
   - Updated desired/current values are reflected after the next `Refresh()` rebuild.
