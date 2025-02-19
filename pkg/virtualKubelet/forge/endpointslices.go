// Copyright 2019-2023 The Liqo Authors
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//      http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package forge

import (
	corev1 "k8s.io/api/core/v1"
	discoveryv1 "k8s.io/api/discovery/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	corev1listers "k8s.io/client-go/listers/core/v1"
	"k8s.io/klog/v2"
	"k8s.io/utils/pointer"

	vkv1alpha1 "github.com/liqotech/liqo/apis/virtualkubelet/v1alpha1"
	"github.com/liqotech/liqo/pkg/utils/getters"
)

// EndpointSliceManagedBy -> The manager associated with the reflected EndpointSlices.
const EndpointSliceManagedBy = "endpointslice.reflection.liqo.io"

// EndpointTranslator defines the function to translate between local and remote endpoint addresses.
type EndpointTranslator func([]string) []string

// EndpointSliceLabels returns the labels assigned to the reflected EndpointSlices.
func EndpointSliceLabels() labels.Set {
	return map[string]string{discoveryv1.LabelManagedBy: EndpointSliceManagedBy}
}

// IsEndpointSliceManagedByReflection returns whether the EndpointSlice is managed by the reflection logic.
func IsEndpointSliceManagedByReflection(obj metav1.Object) bool {
	return EndpointSliceLabels().AsSelectorPreValidated().Matches(labels.Set(obj.GetLabels()))
}

// EndpointToBeReflected filters out the endpoints targeting pods already running on the remote cluster.
func EndpointToBeReflected(endpoint *discoveryv1.Endpoint, localNodeClient corev1listers.NodeLister) bool {
	if endpoint.NodeName == nil {
		klog.Warning("Endpoint without nodeName")
		return false
	}
	epNode, err := localNodeClient.Get(*endpoint.NodeName)
	if err != nil {
		klog.Errorf("Unable to retrieve node %s: %s", *endpoint.NodeName, err.Error())
		return false
	}
	vkNode, err := localNodeClient.Get(LiqoNodeName)
	if err != nil {
		klog.Errorf("Unable to retrieve node %s: %s", LiqoNodeName, err.Error())
		return false
	}
	vkRemoteClusterID, err := getters.RetrieveRemoteClusterIDFromNode(epNode)
	if err != nil {
		klog.Errorf("Unable to retrieve remote cluster ID from node %s: %s", epNode.GetName(), err.Error())
		return false
	}
	nodeRemoteClusterID, err := getters.RetrieveRemoteClusterIDFromNode(vkNode)
	if err != nil {
		klog.Errorf("Unable to retrieve remote cluster ID from node %s: %s", vkNode.GetName(), err.Error())
		return false
	}
	return !pointer.StringEqual(&nodeRemoteClusterID, &vkRemoteClusterID)
}

// RemoteShadowEndpointSlice forges the remote shadowendpointslice, given the local endpointslice.
func RemoteShadowEndpointSlice(local *discoveryv1.EndpointSlice, remote *vkv1alpha1.ShadowEndpointSlice,
	localNodeClient corev1listers.NodeLister, targetNamespace string, translator EndpointTranslator,
	forgingOpts *ForgingOpts) *vkv1alpha1.ShadowEndpointSlice {
	if remote == nil {
		// The remote is nil if not already created.
		remote = &vkv1alpha1.ShadowEndpointSlice{ObjectMeta: metav1.ObjectMeta{Name: local.GetName(), Namespace: targetNamespace}}
	}

	return &vkv1alpha1.ShadowEndpointSlice{
		ObjectMeta: RemoteEndpointSliceObjectMeta(&local.ObjectMeta, &remote.ObjectMeta, forgingOpts),
		Spec: vkv1alpha1.ShadowEndpointSliceSpec{
			Template: vkv1alpha1.EndpointSliceTemplate{
				AddressType: local.AddressType,
				Endpoints:   RemoteEndpointSliceEndpoints(local.Endpoints, localNodeClient, translator),
				Ports:       RemoteEndpointSlicePorts(local.Ports),
			},
		},
	}
}

// RemoteEndpointSliceObjectMeta forges the objectMeta of the reflected endpointslice, given the local one.
func RemoteEndpointSliceObjectMeta(local, remote *metav1.ObjectMeta, forgingOpts *ForgingOpts) metav1.ObjectMeta {
	objectMeta := RemoteObjectMeta(local, remote)
	objectMeta.SetLabels(labels.Merge(objectMeta.Labels, EndpointSliceLabels()))
	objectMeta.SetLabels(FilterNotReflected(objectMeta.Labels, forgingOpts.LabelsNotReflected))
	objectMeta.SetAnnotations(FilterNotReflected(objectMeta.Annotations, forgingOpts.AnnotationsNotReflected))

	return objectMeta
}

// RemoteEndpointSliceEndpoints forges the endpoints of the reflected endpointslice, given the local ones.
func RemoteEndpointSliceEndpoints(locals []discoveryv1.Endpoint, localNodeClient corev1listers.NodeLister,
	translator EndpointTranslator) []discoveryv1.Endpoint {
	var remotes []discoveryv1.Endpoint

	for i := range locals {
		if !EndpointToBeReflected(&locals[i], localNodeClient) {
			// Skip the endpoints referring to the target node (as natively present).
			continue
		}

		local := locals[i].DeepCopy()
		conditions := discoveryv1.EndpointConditions{Ready: local.Conditions.Ready}

		remote := discoveryv1.Endpoint{
			Addresses:  translator(local.Addresses),
			Conditions: conditions,
			Hostname:   local.Hostname,
			TargetRef:  RemoteEndpointTargetRef(local.TargetRef),
			NodeName:   pointer.String(LocalCluster.ClusterName),
			Zone:       local.Zone,
			Hints:      local.Hints,
		}

		remotes = append(remotes, remote)
	}

	return remotes
}

// RemoteEndpointTargetRef forges the ObjectReference of the reflected endpoint, given the local one.
func RemoteEndpointTargetRef(ref *corev1.ObjectReference) *corev1.ObjectReference {
	if ref == nil {
		return nil
	}
	ref.Kind = RemoteKind(ref.Kind)
	return ref
}

// RemoteEndpointSlicePorts forges the ports of the reflected endpointslice, given the local ones.
func RemoteEndpointSlicePorts(locals []discoveryv1.EndpointPort) []discoveryv1.EndpointPort {
	var remotes []discoveryv1.EndpointPort
	for i := range locals {
		// DeepCopy the local object, to avoid mutating the cache.
		local := locals[i].DeepCopy()
		remotes = append(remotes, *local)
	}
	return remotes
}
