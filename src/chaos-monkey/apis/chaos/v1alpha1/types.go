package v1alpha1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
)

// GroupName is the group name use in this package
const GroupName = "chaos.dk1027.io"
const GroupVersion = "v1alpha1"

// SchemeGroupVersion is group version used to register these objects
var SchemeGroupVersion = schema.GroupVersion{Group: GroupName, Version: GroupVersion}

// ChaosExperimentSpec defines the desired state of ChaosExperiment
type ChaosExperimentSpec struct {
	TargetSelectors TargetSelectors `json:"targetSelectors"`
	Action          string          `json:"action"`
	Parameters      ChaosParameters `json:"parameters"`
	Duration        string          `json:"duration"`
}

type TargetSelectors struct {
	Namespaces   []string             `json:"namespaces,omitempty"`
	PodSelector  metav1.LabelSelector `json:"podSelector"`
}

type ChaosParameters struct {
	LatencyMs         uint32 `json:"latencyMs,omitempty"`
	DropPercentage    uint32 `json:"dropPercentage,omitempty"`
	CorruptPercentage uint32 `json:"corruptPercentage,omitempty"`
}

// ChaosExperimentStatus defines the observed state of ChaosExperiment
type ChaosExperimentStatus struct {
	Phase string `json:"phase,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status

// ChaosExperiment is the Schema for the chaosexperiments API
type ChaosExperiment struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   ChaosExperimentSpec   `json:"spec,omitempty"`
	Status ChaosExperimentStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true

// ChaosExperimentList contains a list of ChaosExperiment
type ChaosExperimentList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []ChaosExperiment `json:"items"`
}

// NodeChaosTaskSpec defines the desired state of NodeChaosTask
type NodeChaosTaskSpec struct {
	NodeName        string          `json:"nodeName"`
	Action          string          `json:"action"`
	TargetSelectors TargetSelectors `json:"targetSelectors"`
	ChaosConfig     ChaosConfig     `json:"chaosConfig"`
	EndTime         metav1.Time     `json:"endTime"`
}

type ChaosConfig struct {
	DropPercentage    uint32 `json:"dropPercentage"`
	LatencyMs         uint32 `json:"latencyMs"`
	CorruptPercentage uint32 `json:"corruptPercentage"`
	DeadManTimestamp  uint64 `json:"deadManTimestamp"`
}

// NodeChaosTaskStatus defines the observed state of NodeChaosTask
type NodeChaosTaskStatus struct {
	Phase string `json:"phase,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status

// NodeChaosTask is the Schema for the nodechaostasks API
type NodeChaosTask struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   NodeChaosTaskSpec   `json:"spec,omitempty"`
	Status NodeChaosTaskStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true

// NodeChaosTaskList contains a list of NodeChaosTask
type NodeChaosTaskList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []NodeChaosTask `json:"items"`
}

var (
	SchemeBuilder = runtime.NewSchemeBuilder(addKnownTypes)
	AddToScheme   = SchemeBuilder.AddToScheme
)

func addKnownTypes(scheme *runtime.Scheme) error {
	scheme.AddKnownTypes(SchemeGroupVersion,
		&ChaosExperiment{},
		&ChaosExperimentList{},
		&NodeChaosTask{},
		&NodeChaosTaskList{},
	)
	metav1.AddToGroupVersion(scheme, SchemeGroupVersion)
	return nil
}

// DeepCopy implementations

func (in *ChaosExperiment) DeepCopyInto(out *ChaosExperiment) {
	*out = *in
	out.TypeMeta = in.TypeMeta
	in.ObjectMeta.DeepCopyInto(&out.ObjectMeta)
	in.Spec.DeepCopyInto(&out.Spec)
	out.Status = in.Status
}

func (in *ChaosExperiment) DeepCopy() *ChaosExperiment {
	if in == nil {
		return nil
	}
	out := new(ChaosExperiment)
	in.DeepCopyInto(out)
	return out
}

func (in *ChaosExperiment) DeepCopyObject() runtime.Object {
	if c := in.DeepCopy(); c != nil {
		return c
	}
	return nil
}

func (in *ChaosExperimentList) DeepCopyInto(out *ChaosExperimentList) {
	*out = *in
	out.TypeMeta = in.TypeMeta
	in.ListMeta.DeepCopyInto(&out.ListMeta)
	if in.Items != nil {
		in, out := &in.Items, &out.Items
		*out = make([]ChaosExperiment, len(*in))
		for i := range *in {
			(*in)[i].DeepCopyInto(&(*out)[i])
		}
	}
}

func (in *ChaosExperimentList) DeepCopy() *ChaosExperimentList {
	if in == nil {
		return nil
	}
	out := new(ChaosExperimentList)
	in.DeepCopyInto(out)
	return out
}

func (in *ChaosExperimentList) DeepCopyObject() runtime.Object {
	if c := in.DeepCopy(); c != nil {
		return c
	}
	return nil
}

func (in *ChaosExperimentSpec) DeepCopyInto(out *ChaosExperimentSpec) {
	*out = *in
	in.TargetSelectors.DeepCopyInto(&out.TargetSelectors)
	out.Parameters = in.Parameters
}

func (in *TargetSelectors) DeepCopyInto(out *TargetSelectors) {
	*out = *in
	if in.Namespaces != nil {
		in, out := &in.Namespaces, &out.Namespaces
		*out = make([]string, len(*in))
		copy(*out, *in)
	}
	in.PodSelector.DeepCopyInto(&out.PodSelector)
}

func (in *NodeChaosTask) DeepCopyInto(out *NodeChaosTask) {
	*out = *in
	out.TypeMeta = in.TypeMeta
	in.ObjectMeta.DeepCopyInto(&out.ObjectMeta)
	in.Spec.DeepCopyInto(&out.Spec)
	out.Status = in.Status
}

func (in *NodeChaosTask) DeepCopy() *NodeChaosTask {
	if in == nil {
		return nil
	}
	out := new(NodeChaosTask)
	in.DeepCopyInto(out)
	return out
}

func (in *NodeChaosTask) DeepCopyObject() runtime.Object {
	if c := in.DeepCopy(); c != nil {
		return c
	}
	return nil
}

func (in *NodeChaosTaskList) DeepCopyInto(out *NodeChaosTaskList) {
	*out = *in
	out.TypeMeta = in.TypeMeta
	in.ListMeta.DeepCopyInto(&out.ListMeta)
	if in.Items != nil {
		in, out := &in.Items, &out.Items
		*out = make([]NodeChaosTask, len(*in))
		for i := range *in {
			(*in)[i].DeepCopyInto(&(*out)[i])
		}
	}
}

func (in *NodeChaosTaskList) DeepCopy() *NodeChaosTaskList {
	if in == nil {
		return nil
	}
	out := new(NodeChaosTaskList)
	in.DeepCopyInto(out)
	return out
}

func (in *NodeChaosTaskList) DeepCopyObject() runtime.Object {
	if c := in.DeepCopy(); c != nil {
		return c
	}
	return nil
}

func (in *NodeChaosTaskSpec) DeepCopyInto(out *NodeChaosTaskSpec) {
	*out = *in
	in.TargetSelectors.DeepCopyInto(&out.TargetSelectors)
	out.ChaosConfig = in.ChaosConfig
	in.EndTime.DeepCopyInto(&out.EndTime)
}
