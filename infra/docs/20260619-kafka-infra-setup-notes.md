# Infrastructure Setup Notes (2026-06-19)

During the deployment of the Kafka/Kubernetes infrastructure, we encountered several configuration hurdles, mostly stemming from recent architectural shifts in Docker and the Strimzi Kafka Operator. Below is a summary of the issues faced and how they were resolved.

## 1. Docker Resource Limit Rejections
* **Problem**: When dynamically applying the 6GB memory constraint to the existing Kind node containers (`docker update --cpus 1 -m 6g $NODE`), the Docker daemon rejected the command. It threw a `Memory limit should be smaller than already set memoryswap limit` error.
* **Fix**: When you decrease the physical memory of a container, Docker requires you to adjust the swap memory in tandem so it doesn't violate its internal allocation ratios. I updated the `Makefile` command to explicitly pass `--memory-swap 12g` alongside `-m 6g`.

## 2. Kubernetes CRD Race Condition
* **Problem**: The `setup-kafka` Makefile target attempted to apply the `Kafka` and `KafkaTopic` custom resources immediately after the Strimzi Operator was installed via Helm. Kubernetes rejected them with a `no matches for kind "Kafka"` error because the API server hadn't finished parsing and establishing the new CRDs yet.
* **Fix**: Added a `kubectl wait --for=condition=established --timeout=60s crd/kafkas.kafka.strimzi.io` command in the Makefile to block execution until the API server was fully ready to accept Strimzi resources.

## 3. Strimzi v1beta2 Deprecation & KRaft NodePools
* **Problem**: Our original Kafka manifest used the `kafka.strimzi.io/v1beta2` API version and defined broker replicas directly inside `spec.kafka`. The latest Strimzi Operator (`1.0.1`) dropped `v1beta2` entirely. Furthermore, Strimzi fundamentally changed its architecture to enforce KRaft via Node Pools instead of inline broker configurations.
* **Fix**: 
  - Bumped the `apiVersion` to `kafka.strimzi.io/v1`.
  - Stripped `replicas` and `storage` out of the main `Kafka` object and moved them into a dedicated `KafkaNodePool` resource with roles for `controller` and `broker`.

## 4. Unsupported Kafka Version
* **Problem**: The Strimzi operator threw a `NotReady` condition stating: `Unsupported Kafka.spec.kafka.version: 3.7.0. Supported versions are: [4.1.0, 4.1.1, 4.1.2, 4.2.0]`.
* **Fix**: Bumped the `version` field in `deploy/manifests/kafka/kafka-cluster.yaml` from `3.7.0` to `4.2.0` (and dropped the `metadataVersion` field to let Strimzi auto-default it), aligning with the operator's supported matrix.

## 5. Operator CrashLoopBackOff (Feature Gates)
* **Problem**: The Strimzi Cluster Operator pod entered a `CrashLoopBackOff` state with the error: `Unknown feature gate UseKRaft found in the configuration`. Since KRaft is now strictly the default, the operator actively rejects the legacy feature gate toggles.
* **Fix**: Removed the `--set featureGates="+UseKRaft,+KafkaNodePools"` parameter from the `helm upgrade` command in the `Makefile`.

## 6. Schema Registry Version Audit
* **Problem**: During an infrastructure audit, we discovered that the hardcoded Confluent Schema Registry image (`7.5.0`) was severely outdated compared to the latest available Docker Hub tags.
* **Fix**: Bumped the image in `deploy/manifests/kafka/schema-registry.yaml` to `confluentinc/cp-schema-registry:8.2.2` and triggered a seamless rolling update via `kubectl apply`.
