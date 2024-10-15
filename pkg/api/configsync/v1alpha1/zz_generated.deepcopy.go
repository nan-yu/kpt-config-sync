//go:build !ignore_autogenerated

// Code generated by controller-gen. DO NOT EDIT.

package v1alpha1

import (
	"k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"kpt.dev/configsync/pkg/pubsub"
)

// DeepCopyInto is an autogenerated deepcopy function, copying the receiver, writing into out. in must be non-nil.
func (in *ConfigSyncError) DeepCopyInto(out *ConfigSyncError) {
	*out = *in
	if in.Resources != nil {
		in, out := &in.Resources, &out.Resources
		*out = make([]ResourceRef, len(*in))
		copy(*out, *in)
	}
}

// DeepCopy is an autogenerated deepcopy function, copying the receiver, creating a new ConfigSyncError.
func (in *ConfigSyncError) DeepCopy() *ConfigSyncError {
	if in == nil {
		return nil
	}
	out := new(ConfigSyncError)
	in.DeepCopyInto(out)
	return out
}

// DeepCopyInto is an autogenerated deepcopy function, copying the receiver, writing into out. in must be non-nil.
func (in *ContainerLogLevelOverride) DeepCopyInto(out *ContainerLogLevelOverride) {
	*out = *in
}

// DeepCopy is an autogenerated deepcopy function, copying the receiver, creating a new ContainerLogLevelOverride.
func (in *ContainerLogLevelOverride) DeepCopy() *ContainerLogLevelOverride {
	if in == nil {
		return nil
	}
	out := new(ContainerLogLevelOverride)
	in.DeepCopyInto(out)
	return out
}

// DeepCopyInto is an autogenerated deepcopy function, copying the receiver, writing into out. in must be non-nil.
func (in *ContainerResourcesSpec) DeepCopyInto(out *ContainerResourcesSpec) {
	*out = *in
	out.CPURequest = in.CPURequest.DeepCopy()
	out.MemoryRequest = in.MemoryRequest.DeepCopy()
	out.CPULimit = in.CPULimit.DeepCopy()
	out.MemoryLimit = in.MemoryLimit.DeepCopy()
}

// DeepCopy is an autogenerated deepcopy function, copying the receiver, creating a new ContainerResourcesSpec.
func (in *ContainerResourcesSpec) DeepCopy() *ContainerResourcesSpec {
	if in == nil {
		return nil
	}
	out := new(ContainerResourcesSpec)
	in.DeepCopyInto(out)
	return out
}

// DeepCopyInto is an autogenerated deepcopy function, copying the receiver, writing into out. in must be non-nil.
func (in *ErrorSummary) DeepCopyInto(out *ErrorSummary) {
	*out = *in
}

// DeepCopy is an autogenerated deepcopy function, copying the receiver, creating a new ErrorSummary.
func (in *ErrorSummary) DeepCopy() *ErrorSummary {
	if in == nil {
		return nil
	}
	out := new(ErrorSummary)
	in.DeepCopyInto(out)
	return out
}

// DeepCopyInto is an autogenerated deepcopy function, copying the receiver, writing into out. in must be non-nil.
func (in *Git) DeepCopyInto(out *Git) {
	*out = *in
	out.Period = in.Period
	if in.SecretRef != nil {
		in, out := &in.SecretRef, &out.SecretRef
		*out = new(SecretReference)
		**out = **in
	}
	if in.CACertSecretRef != nil {
		in, out := &in.CACertSecretRef, &out.CACertSecretRef
		*out = new(SecretReference)
		**out = **in
	}
}

// DeepCopy is an autogenerated deepcopy function, copying the receiver, creating a new Git.
func (in *Git) DeepCopy() *Git {
	if in == nil {
		return nil
	}
	out := new(Git)
	in.DeepCopyInto(out)
	return out
}

// DeepCopyInto is an autogenerated deepcopy function, copying the receiver, writing into out. in must be non-nil.
func (in *GitStatus) DeepCopyInto(out *GitStatus) {
	*out = *in
}

// DeepCopy is an autogenerated deepcopy function, copying the receiver, creating a new GitStatus.
func (in *GitStatus) DeepCopy() *GitStatus {
	if in == nil {
		return nil
	}
	out := new(GitStatus)
	in.DeepCopyInto(out)
	return out
}

// DeepCopyInto is an autogenerated deepcopy function, copying the receiver, writing into out. in must be non-nil.
func (in *HelmBase) DeepCopyInto(out *HelmBase) {
	*out = *in
	if in.Values != nil {
		in, out := &in.Values, &out.Values
		*out = new(v1.JSON)
		(*in).DeepCopyInto(*out)
	}
	if in.ValuesFileRefs != nil {
		in, out := &in.ValuesFileRefs, &out.ValuesFileRefs
		*out = make([]ValuesFileRef, len(*in))
		copy(*out, *in)
	}
	out.Period = in.Period
	if in.SecretRef != nil {
		in, out := &in.SecretRef, &out.SecretRef
		*out = new(SecretReference)
		**out = **in
	}
	if in.CACertSecretRef != nil {
		in, out := &in.CACertSecretRef, &out.CACertSecretRef
		*out = new(SecretReference)
		**out = **in
	}
}

// DeepCopy is an autogenerated deepcopy function, copying the receiver, creating a new HelmBase.
func (in *HelmBase) DeepCopy() *HelmBase {
	if in == nil {
		return nil
	}
	out := new(HelmBase)
	in.DeepCopyInto(out)
	return out
}

// DeepCopyInto is an autogenerated deepcopy function, copying the receiver, writing into out. in must be non-nil.
func (in *HelmRepoSync) DeepCopyInto(out *HelmRepoSync) {
	*out = *in
	in.HelmBase.DeepCopyInto(&out.HelmBase)
}

// DeepCopy is an autogenerated deepcopy function, copying the receiver, creating a new HelmRepoSync.
func (in *HelmRepoSync) DeepCopy() *HelmRepoSync {
	if in == nil {
		return nil
	}
	out := new(HelmRepoSync)
	in.DeepCopyInto(out)
	return out
}

// DeepCopyInto is an autogenerated deepcopy function, copying the receiver, writing into out. in must be non-nil.
func (in *HelmRootSync) DeepCopyInto(out *HelmRootSync) {
	*out = *in
	in.HelmBase.DeepCopyInto(&out.HelmBase)
}

// DeepCopy is an autogenerated deepcopy function, copying the receiver, creating a new HelmRootSync.
func (in *HelmRootSync) DeepCopy() *HelmRootSync {
	if in == nil {
		return nil
	}
	out := new(HelmRootSync)
	in.DeepCopyInto(out)
	return out
}

// DeepCopyInto is an autogenerated deepcopy function, copying the receiver, writing into out. in must be non-nil.
func (in *HelmStatus) DeepCopyInto(out *HelmStatus) {
	*out = *in
}

// DeepCopy is an autogenerated deepcopy function, copying the receiver, creating a new HelmStatus.
func (in *HelmStatus) DeepCopy() *HelmStatus {
	if in == nil {
		return nil
	}
	out := new(HelmStatus)
	in.DeepCopyInto(out)
	return out
}

// DeepCopyInto is an autogenerated deepcopy function, copying the receiver, writing into out. in must be non-nil.
func (in *Oci) DeepCopyInto(out *Oci) {
	*out = *in
	out.Period = in.Period
	if in.CACertSecretRef != nil {
		in, out := &in.CACertSecretRef, &out.CACertSecretRef
		*out = new(SecretReference)
		**out = **in
	}
}

// DeepCopy is an autogenerated deepcopy function, copying the receiver, creating a new Oci.
func (in *Oci) DeepCopy() *Oci {
	if in == nil {
		return nil
	}
	out := new(Oci)
	in.DeepCopyInto(out)
	return out
}

// DeepCopyInto is an autogenerated deepcopy function, copying the receiver, writing into out. in must be non-nil.
func (in *OciStatus) DeepCopyInto(out *OciStatus) {
	*out = *in
}

// DeepCopy is an autogenerated deepcopy function, copying the receiver, creating a new OciStatus.
func (in *OciStatus) DeepCopy() *OciStatus {
	if in == nil {
		return nil
	}
	out := new(OciStatus)
	in.DeepCopyInto(out)
	return out
}

// DeepCopyInto is an autogenerated deepcopy function, copying the receiver, writing into out. in must be non-nil.
func (in *OverrideSpec) DeepCopyInto(out *OverrideSpec) {
	*out = *in
	if in.Resources != nil {
		in, out := &in.Resources, &out.Resources
		*out = make([]ContainerResourcesSpec, len(*in))
		for i := range *in {
			(*in)[i].DeepCopyInto(&(*out)[i])
		}
	}
	if in.GitSyncDepth != nil {
		in, out := &in.GitSyncDepth, &out.GitSyncDepth
		*out = new(int64)
		**out = **in
	}
	if in.ReconcileTimeout != nil {
		in, out := &in.ReconcileTimeout, &out.ReconcileTimeout
		*out = new(metav1.Duration)
		**out = **in
	}
	if in.APIServerTimeout != nil {
		in, out := &in.APIServerTimeout, &out.APIServerTimeout
		*out = new(metav1.Duration)
		**out = **in
	}
	if in.EnableShellInRendering != nil {
		in, out := &in.EnableShellInRendering, &out.EnableShellInRendering
		*out = new(bool)
		**out = **in
	}
	if in.LogLevels != nil {
		in, out := &in.LogLevels, &out.LogLevels
		*out = make([]ContainerLogLevelOverride, len(*in))
		copy(*out, *in)
	}
}

// DeepCopy is an autogenerated deepcopy function, copying the receiver, creating a new OverrideSpec.
func (in *OverrideSpec) DeepCopy() *OverrideSpec {
	if in == nil {
		return nil
	}
	out := new(OverrideSpec)
	in.DeepCopyInto(out)
	return out
}

// DeepCopyInto is an autogenerated deepcopy function, copying the receiver, writing into out. in must be non-nil.
func (in *PubSub) DeepCopyInto(out *PubSub) {
	*out = *in
}

// DeepCopy is an autogenerated deepcopy function, copying the receiver, creating a new PubSub.
func (in *PubSub) DeepCopy() *PubSub {
	if in == nil {
		return nil
	}
	out := new(PubSub)
	in.DeepCopyInto(out)
	return out
}

// DeepCopyInto is an autogenerated deepcopy function, copying the receiver, writing into out. in must be non-nil.
func (in *RenderingStatus) DeepCopyInto(out *RenderingStatus) {
	*out = *in
	if in.Git != nil {
		in, out := &in.Git, &out.Git
		*out = new(GitStatus)
		**out = **in
	}
	if in.Oci != nil {
		in, out := &in.Oci, &out.Oci
		*out = new(OciStatus)
		**out = **in
	}
	if in.Helm != nil {
		in, out := &in.Helm, &out.Helm
		*out = new(HelmStatus)
		**out = **in
	}
	in.LastUpdate.DeepCopyInto(&out.LastUpdate)
	if in.Errors != nil {
		in, out := &in.Errors, &out.Errors
		*out = make([]ConfigSyncError, len(*in))
		for i := range *in {
			(*in)[i].DeepCopyInto(&(*out)[i])
		}
	}
	if in.ErrorSummary != nil {
		in, out := &in.ErrorSummary, &out.ErrorSummary
		*out = new(ErrorSummary)
		**out = **in
	}
}

// DeepCopy is an autogenerated deepcopy function, copying the receiver, creating a new RenderingStatus.
func (in *RenderingStatus) DeepCopy() *RenderingStatus {
	if in == nil {
		return nil
	}
	out := new(RenderingStatus)
	in.DeepCopyInto(out)
	return out
}

// DeepCopyInto is an autogenerated deepcopy function, copying the receiver, writing into out. in must be non-nil.
func (in *RepoSync) DeepCopyInto(out *RepoSync) {
	*out = *in
	out.TypeMeta = in.TypeMeta
	in.ObjectMeta.DeepCopyInto(&out.ObjectMeta)
	in.Spec.DeepCopyInto(&out.Spec)
	in.Status.DeepCopyInto(&out.Status)
}

// DeepCopy is an autogenerated deepcopy function, copying the receiver, creating a new RepoSync.
func (in *RepoSync) DeepCopy() *RepoSync {
	if in == nil {
		return nil
	}
	out := new(RepoSync)
	in.DeepCopyInto(out)
	return out
}

// DeepCopyObject is an autogenerated deepcopy function, copying the receiver, creating a new runtime.Object.
func (in *RepoSync) DeepCopyObject() runtime.Object {
	if c := in.DeepCopy(); c != nil {
		return c
	}
	return nil
}

// DeepCopyInto is an autogenerated deepcopy function, copying the receiver, writing into out. in must be non-nil.
func (in *RepoSyncCondition) DeepCopyInto(out *RepoSyncCondition) {
	*out = *in
	in.LastUpdateTime.DeepCopyInto(&out.LastUpdateTime)
	in.LastTransitionTime.DeepCopyInto(&out.LastTransitionTime)
	if in.Errors != nil {
		in, out := &in.Errors, &out.Errors
		*out = make([]ConfigSyncError, len(*in))
		for i := range *in {
			(*in)[i].DeepCopyInto(&(*out)[i])
		}
	}
	if in.ErrorSourceRefs != nil {
		in, out := &in.ErrorSourceRefs, &out.ErrorSourceRefs
		*out = make([]ErrorSource, len(*in))
		copy(*out, *in)
	}
	if in.ErrorSummary != nil {
		in, out := &in.ErrorSummary, &out.ErrorSummary
		*out = new(ErrorSummary)
		**out = **in
	}
}

// DeepCopy is an autogenerated deepcopy function, copying the receiver, creating a new RepoSyncCondition.
func (in *RepoSyncCondition) DeepCopy() *RepoSyncCondition {
	if in == nil {
		return nil
	}
	out := new(RepoSyncCondition)
	in.DeepCopyInto(out)
	return out
}

// DeepCopyInto is an autogenerated deepcopy function, copying the receiver, writing into out. in must be non-nil.
func (in *RepoSyncList) DeepCopyInto(out *RepoSyncList) {
	*out = *in
	out.TypeMeta = in.TypeMeta
	in.ListMeta.DeepCopyInto(&out.ListMeta)
	if in.Items != nil {
		in, out := &in.Items, &out.Items
		*out = make([]RepoSync, len(*in))
		for i := range *in {
			(*in)[i].DeepCopyInto(&(*out)[i])
		}
	}
}

// DeepCopy is an autogenerated deepcopy function, copying the receiver, creating a new RepoSyncList.
func (in *RepoSyncList) DeepCopy() *RepoSyncList {
	if in == nil {
		return nil
	}
	out := new(RepoSyncList)
	in.DeepCopyInto(out)
	return out
}

// DeepCopyObject is an autogenerated deepcopy function, copying the receiver, creating a new runtime.Object.
func (in *RepoSyncList) DeepCopyObject() runtime.Object {
	if c := in.DeepCopy(); c != nil {
		return c
	}
	return nil
}

// DeepCopyInto is an autogenerated deepcopy function, copying the receiver, writing into out. in must be non-nil.
func (in *RepoSyncOverrideSpec) DeepCopyInto(out *RepoSyncOverrideSpec) {
	*out = *in
	in.OverrideSpec.DeepCopyInto(&out.OverrideSpec)
}

// DeepCopy is an autogenerated deepcopy function, copying the receiver, creating a new RepoSyncOverrideSpec.
func (in *RepoSyncOverrideSpec) DeepCopy() *RepoSyncOverrideSpec {
	if in == nil {
		return nil
	}
	out := new(RepoSyncOverrideSpec)
	in.DeepCopyInto(out)
	return out
}

// DeepCopyInto is an autogenerated deepcopy function, copying the receiver, writing into out. in must be non-nil.
func (in *RepoSyncSpec) DeepCopyInto(out *RepoSyncSpec) {
	*out = *in
	if in.Git != nil {
		in, out := &in.Git, &out.Git
		*out = new(Git)
		(*in).DeepCopyInto(*out)
	}
	if in.Oci != nil {
		in, out := &in.Oci, &out.Oci
		*out = new(Oci)
		(*in).DeepCopyInto(*out)
	}
	if in.Helm != nil {
		in, out := &in.Helm, &out.Helm
		*out = new(HelmRepoSync)
		(*in).DeepCopyInto(*out)
	}
	if in.PubSub != nil {
		in, out := &in.PubSub, &out.PubSub
		*out = new(PubSub)
		**out = **in
	}
	if in.Override != nil {
		in, out := &in.Override, &out.Override
		*out = new(RepoSyncOverrideSpec)
		(*in).DeepCopyInto(*out)
	}
}

// DeepCopy is an autogenerated deepcopy function, copying the receiver, creating a new RepoSyncSpec.
func (in *RepoSyncSpec) DeepCopy() *RepoSyncSpec {
	if in == nil {
		return nil
	}
	out := new(RepoSyncSpec)
	in.DeepCopyInto(out)
	return out
}

// DeepCopyInto is an autogenerated deepcopy function, copying the receiver, writing into out. in must be non-nil.
func (in *RepoSyncStatus) DeepCopyInto(out *RepoSyncStatus) {
	*out = *in
	in.Status.DeepCopyInto(&out.Status)
	if in.Conditions != nil {
		in, out := &in.Conditions, &out.Conditions
		*out = make([]RepoSyncCondition, len(*in))
		for i := range *in {
			(*in)[i].DeepCopyInto(&(*out)[i])
		}
	}
}

// DeepCopy is an autogenerated deepcopy function, copying the receiver, creating a new RepoSyncStatus.
func (in *RepoSyncStatus) DeepCopy() *RepoSyncStatus {
	if in == nil {
		return nil
	}
	out := new(RepoSyncStatus)
	in.DeepCopyInto(out)
	return out
}

// DeepCopyInto is an autogenerated deepcopy function, copying the receiver, writing into out. in must be non-nil.
func (in *ResourceRef) DeepCopyInto(out *ResourceRef) {
	*out = *in
	out.GVK = in.GVK
}

// DeepCopy is an autogenerated deepcopy function, copying the receiver, creating a new ResourceRef.
func (in *ResourceRef) DeepCopy() *ResourceRef {
	if in == nil {
		return nil
	}
	out := new(ResourceRef)
	in.DeepCopyInto(out)
	return out
}

// DeepCopyInto is an autogenerated deepcopy function, copying the receiver, writing into out. in must be non-nil.
func (in *RootSync) DeepCopyInto(out *RootSync) {
	*out = *in
	out.TypeMeta = in.TypeMeta
	in.ObjectMeta.DeepCopyInto(&out.ObjectMeta)
	in.Spec.DeepCopyInto(&out.Spec)
	in.Status.DeepCopyInto(&out.Status)
}

// DeepCopy is an autogenerated deepcopy function, copying the receiver, creating a new RootSync.
func (in *RootSync) DeepCopy() *RootSync {
	if in == nil {
		return nil
	}
	out := new(RootSync)
	in.DeepCopyInto(out)
	return out
}

// DeepCopyObject is an autogenerated deepcopy function, copying the receiver, creating a new runtime.Object.
func (in *RootSync) DeepCopyObject() runtime.Object {
	if c := in.DeepCopy(); c != nil {
		return c
	}
	return nil
}

// DeepCopyInto is an autogenerated deepcopy function, copying the receiver, writing into out. in must be non-nil.
func (in *RootSyncCondition) DeepCopyInto(out *RootSyncCondition) {
	*out = *in
	in.LastUpdateTime.DeepCopyInto(&out.LastUpdateTime)
	in.LastTransitionTime.DeepCopyInto(&out.LastTransitionTime)
	if in.Errors != nil {
		in, out := &in.Errors, &out.Errors
		*out = make([]ConfigSyncError, len(*in))
		for i := range *in {
			(*in)[i].DeepCopyInto(&(*out)[i])
		}
	}
	if in.ErrorSourceRefs != nil {
		in, out := &in.ErrorSourceRefs, &out.ErrorSourceRefs
		*out = make([]ErrorSource, len(*in))
		copy(*out, *in)
	}
	if in.ErrorSummary != nil {
		in, out := &in.ErrorSummary, &out.ErrorSummary
		*out = new(ErrorSummary)
		**out = **in
	}
}

// DeepCopy is an autogenerated deepcopy function, copying the receiver, creating a new RootSyncCondition.
func (in *RootSyncCondition) DeepCopy() *RootSyncCondition {
	if in == nil {
		return nil
	}
	out := new(RootSyncCondition)
	in.DeepCopyInto(out)
	return out
}

// DeepCopyInto is an autogenerated deepcopy function, copying the receiver, writing into out. in must be non-nil.
func (in *RootSyncList) DeepCopyInto(out *RootSyncList) {
	*out = *in
	out.TypeMeta = in.TypeMeta
	in.ListMeta.DeepCopyInto(&out.ListMeta)
	if in.Items != nil {
		in, out := &in.Items, &out.Items
		*out = make([]RootSync, len(*in))
		for i := range *in {
			(*in)[i].DeepCopyInto(&(*out)[i])
		}
	}
}

// DeepCopy is an autogenerated deepcopy function, copying the receiver, creating a new RootSyncList.
func (in *RootSyncList) DeepCopy() *RootSyncList {
	if in == nil {
		return nil
	}
	out := new(RootSyncList)
	in.DeepCopyInto(out)
	return out
}

// DeepCopyObject is an autogenerated deepcopy function, copying the receiver, creating a new runtime.Object.
func (in *RootSyncList) DeepCopyObject() runtime.Object {
	if c := in.DeepCopy(); c != nil {
		return c
	}
	return nil
}

// DeepCopyInto is an autogenerated deepcopy function, copying the receiver, writing into out. in must be non-nil.
func (in *RootSyncOverrideSpec) DeepCopyInto(out *RootSyncOverrideSpec) {
	*out = *in
	in.OverrideSpec.DeepCopyInto(&out.OverrideSpec)
	if in.RoleRefs != nil {
		in, out := &in.RoleRefs, &out.RoleRefs
		*out = make([]RootSyncRoleRef, len(*in))
		copy(*out, *in)
	}
}

// DeepCopy is an autogenerated deepcopy function, copying the receiver, creating a new RootSyncOverrideSpec.
func (in *RootSyncOverrideSpec) DeepCopy() *RootSyncOverrideSpec {
	if in == nil {
		return nil
	}
	out := new(RootSyncOverrideSpec)
	in.DeepCopyInto(out)
	return out
}

// DeepCopyInto is an autogenerated deepcopy function, copying the receiver, writing into out. in must be non-nil.
func (in *RootSyncRoleRef) DeepCopyInto(out *RootSyncRoleRef) {
	*out = *in
}

// DeepCopy is an autogenerated deepcopy function, copying the receiver, creating a new RootSyncRoleRef.
func (in *RootSyncRoleRef) DeepCopy() *RootSyncRoleRef {
	if in == nil {
		return nil
	}
	out := new(RootSyncRoleRef)
	in.DeepCopyInto(out)
	return out
}

// DeepCopyInto is an autogenerated deepcopy function, copying the receiver, writing into out. in must be non-nil.
func (in *RootSyncSpec) DeepCopyInto(out *RootSyncSpec) {
	*out = *in
	if in.Git != nil {
		in, out := &in.Git, &out.Git
		*out = new(Git)
		(*in).DeepCopyInto(*out)
	}
	if in.Oci != nil {
		in, out := &in.Oci, &out.Oci
		*out = new(Oci)
		(*in).DeepCopyInto(*out)
	}
	if in.Helm != nil {
		in, out := &in.Helm, &out.Helm
		*out = new(HelmRootSync)
		(*in).DeepCopyInto(*out)
	}
	if in.PubSub != nil {
		in, out := &in.PubSub, &out.PubSub
		*out = new(PubSub)
		**out = **in
	}
	if in.Override != nil {
		in, out := &in.Override, &out.Override
		*out = new(RootSyncOverrideSpec)
		(*in).DeepCopyInto(*out)
	}
}

// DeepCopy is an autogenerated deepcopy function, copying the receiver, creating a new RootSyncSpec.
func (in *RootSyncSpec) DeepCopy() *RootSyncSpec {
	if in == nil {
		return nil
	}
	out := new(RootSyncSpec)
	in.DeepCopyInto(out)
	return out
}

// DeepCopyInto is an autogenerated deepcopy function, copying the receiver, writing into out. in must be non-nil.
func (in *RootSyncStatus) DeepCopyInto(out *RootSyncStatus) {
	*out = *in
	in.Status.DeepCopyInto(&out.Status)
	if in.Conditions != nil {
		in, out := &in.Conditions, &out.Conditions
		*out = make([]RootSyncCondition, len(*in))
		for i := range *in {
			(*in)[i].DeepCopyInto(&(*out)[i])
		}
	}
}

// DeepCopy is an autogenerated deepcopy function, copying the receiver, creating a new RootSyncStatus.
func (in *RootSyncStatus) DeepCopy() *RootSyncStatus {
	if in == nil {
		return nil
	}
	out := new(RootSyncStatus)
	in.DeepCopyInto(out)
	return out
}

// DeepCopyInto is an autogenerated deepcopy function, copying the receiver, writing into out. in must be non-nil.
func (in *SecretReference) DeepCopyInto(out *SecretReference) {
	*out = *in
}

// DeepCopy is an autogenerated deepcopy function, copying the receiver, creating a new SecretReference.
func (in *SecretReference) DeepCopy() *SecretReference {
	if in == nil {
		return nil
	}
	out := new(SecretReference)
	in.DeepCopyInto(out)
	return out
}

// DeepCopyInto is an autogenerated deepcopy function, copying the receiver, writing into out. in must be non-nil.
func (in *SourceStatus) DeepCopyInto(out *SourceStatus) {
	*out = *in
	if in.Git != nil {
		in, out := &in.Git, &out.Git
		*out = new(GitStatus)
		**out = **in
	}
	if in.Oci != nil {
		in, out := &in.Oci, &out.Oci
		*out = new(OciStatus)
		**out = **in
	}
	if in.Helm != nil {
		in, out := &in.Helm, &out.Helm
		*out = new(HelmStatus)
		**out = **in
	}
	in.LastUpdate.DeepCopyInto(&out.LastUpdate)
	if in.Errors != nil {
		in, out := &in.Errors, &out.Errors
		*out = make([]ConfigSyncError, len(*in))
		for i := range *in {
			(*in)[i].DeepCopyInto(&(*out)[i])
		}
	}
	if in.ErrorSummary != nil {
		in, out := &in.ErrorSummary, &out.ErrorSummary
		*out = new(ErrorSummary)
		**out = **in
	}
}

// DeepCopy is an autogenerated deepcopy function, copying the receiver, creating a new SourceStatus.
func (in *SourceStatus) DeepCopy() *SourceStatus {
	if in == nil {
		return nil
	}
	out := new(SourceStatus)
	in.DeepCopyInto(out)
	return out
}

// DeepCopyInto is an autogenerated deepcopy function, copying the receiver, writing into out. in must be non-nil.
func (in *Status) DeepCopyInto(out *Status) {
	*out = *in
	if in.LastPublishedMessages != nil {
		in, out := &in.LastPublishedMessages, &out.LastPublishedMessages
		*out = make(map[pubsub.Status]pubsub.Message, len(*in))
		for key, val := range *in {
			(*out)[key] = val
		}
	}
	in.Source.DeepCopyInto(&out.Source)
	in.Rendering.DeepCopyInto(&out.Rendering)
	in.Sync.DeepCopyInto(&out.Sync)
}

// DeepCopy is an autogenerated deepcopy function, copying the receiver, creating a new Status.
func (in *Status) DeepCopy() *Status {
	if in == nil {
		return nil
	}
	out := new(Status)
	in.DeepCopyInto(out)
	return out
}

// DeepCopyInto is an autogenerated deepcopy function, copying the receiver, writing into out. in must be non-nil.
func (in *SyncStatus) DeepCopyInto(out *SyncStatus) {
	*out = *in
	if in.Git != nil {
		in, out := &in.Git, &out.Git
		*out = new(GitStatus)
		**out = **in
	}
	if in.Oci != nil {
		in, out := &in.Oci, &out.Oci
		*out = new(OciStatus)
		**out = **in
	}
	if in.Helm != nil {
		in, out := &in.Helm, &out.Helm
		*out = new(HelmStatus)
		**out = **in
	}
	in.LastUpdate.DeepCopyInto(&out.LastUpdate)
	if in.Errors != nil {
		in, out := &in.Errors, &out.Errors
		*out = make([]ConfigSyncError, len(*in))
		for i := range *in {
			(*in)[i].DeepCopyInto(&(*out)[i])
		}
	}
	if in.ErrorSummary != nil {
		in, out := &in.ErrorSummary, &out.ErrorSummary
		*out = new(ErrorSummary)
		**out = **in
	}
}

// DeepCopy is an autogenerated deepcopy function, copying the receiver, creating a new SyncStatus.
func (in *SyncStatus) DeepCopy() *SyncStatus {
	if in == nil {
		return nil
	}
	out := new(SyncStatus)
	in.DeepCopyInto(out)
	return out
}

// DeepCopyInto is an autogenerated deepcopy function, copying the receiver, writing into out. in must be non-nil.
func (in *ValuesFileRef) DeepCopyInto(out *ValuesFileRef) {
	*out = *in
}

// DeepCopy is an autogenerated deepcopy function, copying the receiver, creating a new ValuesFileRef.
func (in *ValuesFileRef) DeepCopy() *ValuesFileRef {
	if in == nil {
		return nil
	}
	out := new(ValuesFileRef)
	in.DeepCopyInto(out)
	return out
}
