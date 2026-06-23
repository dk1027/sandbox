# Design Document: eBPF Kubernetes Chaos Monkey

## 1. Overview
**Objective:** Build a resilient, Kubernetes-native Chaos Engineering tool that injects dynamic network and storage faults into targeted Pods using eBPF.

**Design Philosophy:**
* **Kubernetes-Native:** Leverages the Operator pattern and Custom Resource Definitions (CRDs) for declarative state.
* **High Performance & Safe:** Utilizes eBPF (`tc` and tracepoints) for kernel-level fault injection, avoiding sidecar overhead.
* **Minimal Blast Radius:** Targets specific container abstractions (veth interfaces and cgroups) rather than host-level interfaces.
* **Decentralized Reconciliation:** Pushes high-frequency pod churn tracking to the edge (nodes) to protect the API server and etcd from DDOS-like traffic.
* **Least Privilege:** Runs with strict Linux capabilities rather than blanket `privileged: true`.

---

## 2. High-Level Architecture
The system is divided into a centralized Control Plane for high-level orchestration and a distributed Data Plane for execution and local reconciliation.

* **Chaos Controller (Deployment):** A cluster-wide operator that translates user intent into node-specific tasks.
* **Chaos Daemon (DaemonSet):** A capability-restricted agent running on every node that handles local pod watching, interface resolution, and eBPF lifecycle management.

---

## 3. Component Design

### 3.1 Custom Resource Definitions (CRDs)
A two-tier CRD architecture decouples user intent from node-level execution.

**Tier 1: ChaosExperiment (User-Facing)**
Defines the user's intent.
* `spec.targetSelectors`: Label and namespace selectors for the pods.
* `spec.action`: The type of fault (e.g., `NetworkDelay`, `PacketLoss`).
* `spec.parameters`: Action-specific configurations.
* `spec.duration`: Time-to-live (TTL) for the experiment.

**Tier 2: NodeChaosTask (Internal)**
Managed by the Chaos Controller. Scoped to a specific node.
* `spec.nodeName`: The target Kubernetes node.
* `spec.targetSelectors`: Passed down from the ChaosExperiment. (Note: Instead of listing Pod IPs, the node is given the selectors to watch locally).
* `spec.chaosConfig`: The translated configuration payload (the struct data) for the eBPF map.
* `spec.endTime`: A hard UTC timestamp for when the chaos must terminate.

### 3.2 Chaos Controller (Control Plane)
The Controller is built using the `kubernetes-sigs/controller-runtime` framework. Its primary mandate is converting user intent (`ChaosExperiment`) into node-specific execution payloads (`NodeChaosTask`), while aggressively avoiding unnecessary API calls.

**1. The Reconciliation Loop & State Machine**
The controller does not watch Pods. It only watches `ChaosExperiment` CRDs. 
* **Rate Limiting:** Uses a standard `workqueue.RateLimitingInterface` with exponential backoff to handle transient API failures during node discovery.
* **Idempotency & Hashing:** To prevent "hot looping," the Controller computes a SHA-256 hash of the translated `spec.chaosConfig` and stores it in an annotation on the `NodeChaosTask`. Before issuing an Update API call, it compares the desired hash against the existing hash.

**2. Node Discovery Algorithm**
When a `ChaosExperiment` is reconciled, the Controller finds the involved nodes:
1.  **List Pods via Cache:** Performs a `client.List` for Pods matching the `spec.targetSelectors`. *This hits the local informer cache, not the live API server, to avoid degrading cluster performance.*
2.  **Extract Nodes:** Iterates through the returned Pod list and collects unique `spec.nodeName` values. Ignores Pods where `spec.nodeName` is empty (pending scheduling).
3.  **Fan-Out Task Creation:** Generates one `NodeChaosTask` for each unique node. 

**3. Garbage Collection via OwnerReferences**
* When creating a `NodeChaosTask`, the Controller injects an `OwnerReference` pointing back to the parent `ChaosExperiment` with `blockOwnerDeletion: true`.
* When the user deletes the `ChaosExperiment`, the Kubernetes Garbage Collector automatically deletes all child `NodeChaosTask` objects.

**4. Absolute Time (TTL) Propagation**
* The Controller reads `spec.duration` from the user, calculates `endTime := time.Now().UTC().Add(duration)`, and writes this absolute UTC `endTime` into the `NodeChaosTask` to prevent clock-drift issues if a node temporarily disconnects.

### 3.3 Chaos Daemon (Data Plane - The Edge)
The Executor runs as a DaemonSet with specific capabilities: `CAP_BPF`, `CAP_NET_ADMIN`, `CAP_PERFMON`, and `CAP_SYS_ADMIN`. It is built using `client-go` and `cilium/ebpf`.

**1. The Dual-Informer Architecture**
The Daemon requires two locally scoped informers:
* **Task Informer:** Watches `NodeChaosTask` resources, initialized with a `FieldSelector` of `spec.nodeName=CURRENT_NODE_NAME` (injected via Downward API).
* **Pod Informer:** Watches core `Pod` resources, also initialized with a `FieldSelector` of `spec.nodeName=CURRENT_NODE_NAME`. This allows instant reaction to local pod churn without taxing the central controller.

**2. Atomic eBPF Map Updates**
All chaos parameters for a single target are applied atomically using a C struct.

---c
struct chaos_config {
    __u32 drop_percentage;
    __u32 latency_ms;
    __u32 corrupt_percentage;
    __u64 dead_man_timestamp; // For safety detach
};

// Map key is the container's cgroup ID or an internal target ID
struct {
    __uint(type, BPF_MAP_TYPE_HASH);
    __uint(max_entries, 1024);
    __type(key, __u64); 
    __type(value, struct chaos_config);
} chaos_map SEC(".maps");
---

---go
// Generated via cilium/ebpf bpf2go
type bpfChaosConfig struct {
    DropPercentage   uint32
    LatencyMs        uint32
    CorruptPercentage uint32
    DeadManTimestamp uint64
}

func applyChaos(mapHandle *ebpf.Map, targetID uint64, config bpfChaosConfig) error {
    // bpf_map_update_elem is an atomic operation in the Linux kernel
    return mapHandle.Update(targetID, config, ebpf.UpdateAny)
}
---

**3. The Local Reconciliation Loop**
When a task arrives or a local pod changes state:
1.  **Evaluate:** Check if the Pod matches the active `NodeChaosTask` selectors.
2.  **Resolve:** Call the Network Resolver to find the host `veth` interface index.
3.  **Attach (Idempotent):** Use `netlink` to ensure a `clsact` qdisc exists on the `veth`. Attach the eBPF program to the `tc` filter chain (noop if already attached).
4.  **Configure:** Update the eBPF Map with the specific `chaos_config` struct.

**4. The Dead Man's Switch & TTL Enforcement**
* **TTL Enforcement:** Upon receiving a task, the Daemon starts a `time.AfterFunc` routine based on `spec.endTime`. When it fires, it clears the eBPF map entries and detaches the `tc` filters.
* **The Heartbeat:** A ticker runs every 1 second, iterating through active eBPF maps to update the `dead_man_timestamp` field. If the Daemon crashes, the kernel-space eBPF program will detect the stale timestamp (> 5 seconds) and safely return `TC_ACT_OK` to bypass all chaos logic.

### 3.4 CNI Abstraction Layer
Handles mapping a Pod to a host-level attachment point.

---go
type NetworkResolver interface {
    Resolve(podNamespace, podName, containerID string) (AttachmentPoint, error)
    Name() string
}

type AttachmentPoint interface {
    Attach(bpfProgram *ebpf.Program) error
    Detach() error
}
---

**Implementation: VethResolver (v1 Default)**
* Supports CNIs utilizing `veth` pairs (Calico, Flannel, AWS VPC CNI).
* Uses `setns` to enter the container's network namespace, reads the `iflink` index, and matches it to the host-side `veth` interface.
* *Note on Directionality:* Attaching to the host-side `veth` flips network direction. The user-space Daemon translates "Egress Faults" into "Ingress TC filters" on the host veth.

---

## 4. eBPF Implementation Details
* **Framework:** Built using `libbpf` and BTF (BPF Type Format) for CO-RE (Compile Once - Run Everywhere).
* **Network Hooks:** Utilizes the `tc` (Traffic Control) subsystem on the `clsact` qdisc. 
* **Storage Hooks:** Utilizes kernel tracepoints (e.g., `block:block_rq_issue`). 
    * *Warning:* This is a system-wide tracepoint. The eBPF program immediately returns `0` if the calling process's cgroup ID does not match the target. CPU overhead on high-IOPS nodes is acknowledged and documented.
    * *Compatibility:* The resolver determines whether the host is running `cgroup v1` or `v2` and passes the correct nested cgroup ID to the eBPF map.

---

## 5. Lifecycle & Edge Cases

### Scenario: DaemonSet Crash & Recovery
`tc` eBPF programs **do not** auto-detach when the user-space process dies. 
* **Solution:** On startup, the Chaos Daemon executes a reconciliation loop. It scans all local `veth` interfaces for attached eBPF programs, compares them against the current active `NodeChaosTasks`, and garbage collects any orphaned chaos programs. 

### Scenario: Network Partition
A node is actively injecting chaos but loses connection to the K8s API Server. The user attempts to stop the experiment.
* **Solution:** Because the `ChaosDaemon` relies on an internal timer based on `spec.endTime` (and additionally, a "dead man's switch" timestamp inside the eBPF map itself), the chaos terminates safely and locally, regardless of control plane connectivity.

### Scenario: Pod Crash (Fail-Close)
If a targeted Pod crashes, the host kernel destroys the associated `veth` interface. When the interface is destroyed, the Linux kernel automatically cleans up the attached `tc` eBPF programs. No orphaned network chaos.

---

## 6. Safety & Observability
* **Metrics:** The DaemonSet exposes a `/metrics` endpoint. The eBPF program maintains drop/delay counters in an eBPF Map, which the user-space Go program periodically scrapes.
* **Dead Man's Switch Engine:** If the user-space process locks up or dies, the eBPF program detects the missing heartbeat within 5 seconds and instantly fails open, ensuring traffic flows uninterrupted until manual or automated recovery occurs.