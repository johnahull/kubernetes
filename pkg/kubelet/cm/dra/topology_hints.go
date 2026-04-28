package dra

import (
	"context"

	v1 "k8s.io/api/core/v1"
	resourceapi "k8s.io/api/resource/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/klog/v2"
	"k8s.io/kubernetes/pkg/kubelet/cm/topologymanager"
	"k8s.io/kubernetes/pkg/kubelet/cm/topologymanager/bitmask"
)

func (m *Manager) GetTopologyHints(pod *v1.Pod, container *v1.Container) map[string][]topologymanager.TopologyHint {
	return m.getDRATopologyHints(pod)
}

func (m *Manager) GetPodTopologyHints(pod *v1.Pod) map[string][]topologymanager.TopologyHint {
	return m.getDRATopologyHints(pod)
}

func (m *Manager) Allocate(pod *v1.Pod, container *v1.Container) error {
	return nil
}

func (m *Manager) AllocatePod(pod *v1.Pod) error {
	return nil
}

func (m *Manager) getDRATopologyHints(pod *v1.Pod) map[string][]topologymanager.TopologyHint {
	if m.kubeClient == nil || len(pod.Spec.ResourceClaims) == 0 {
		return nil
	}

	numaNodes := make(map[int]bool)

	for _, podClaim := range pod.Spec.ResourceClaims {
		claimName := resolveClaimName(pod, podClaim)
		if claimName == "" {
			continue
		}

		claim, err := m.kubeClient.ResourceV1().ResourceClaims(pod.Namespace).Get(
			context.TODO(), claimName, metav1.GetOptions{})
		if err != nil {
			klog.V(4).InfoS("DRA topology: failed to get claim", "claim", claimName, "err", err)
			continue
		}

		if claim.Status.Allocation == nil {
			continue
		}

		for _, result := range claim.Status.Allocation.Devices.Results {
			numaNode := m.lookupDeviceNUMANode(result.Driver, result.Pool, result.Device)
			if numaNode >= 0 {
				numaNodes[numaNode] = true
				klog.V(2).InfoS("DRA topology: found device NUMA",
					"claim", claimName, "device", result.Device,
					"driver", result.Driver, "numaNode", numaNode)
			}
		}
	}

	if len(numaNodes) == 0 {
		return nil
	}

	hints := make(map[string][]topologymanager.TopologyHint)
	for numaNode := range numaNodes {
		mask, err := bitmask.NewBitMask(numaNode)
		if err != nil {
			continue
		}
		hints["dra-devices"] = append(hints["dra-devices"], topologymanager.TopologyHint{
			NUMANodeAffinity: mask,
			Preferred:        true,
		})
	}

	klog.V(2).InfoS("DRA topology hints generated", "pod", klog.KObj(pod), "hints", hints)
	return hints
}

func (m *Manager) lookupDeviceNUMANode(driverName, poolName, deviceName string) int {
	if m.kubeClient == nil {
		return -1
	}

	slices, err := m.kubeClient.ResourceV1().ResourceSlices().List(
		context.TODO(), metav1.ListOptions{
			FieldSelector: "spec.nodeName=" + m.nodeName,
		})
	if err != nil {
		klog.V(4).InfoS("DRA topology: failed to list resource slices", "err", err)
		return -1
	}

	for _, slice := range slices.Items {
		if slice.Spec.Driver != driverName {
			continue
		}
		if slice.Spec.Pool.Name != poolName {
			continue
		}
		for _, device := range slice.Spec.Devices {
			if device.Name != deviceName {
				continue
			}
			if attr, ok := device.Attributes[resourceapi.QualifiedName("resource.kubernetes.io/numaNode")]; ok {
				if attr.IntValue != nil {
					return int(*attr.IntValue)
				}
			}
		}
	}

	return -1
}

func resolveClaimName(pod *v1.Pod, podClaim v1.PodResourceClaim) string {
	if podClaim.ResourceClaimName != nil {
		return *podClaim.ResourceClaimName
	}
	for _, status := range pod.Status.ResourceClaimStatuses {
		if status.Name == podClaim.Name && status.ResourceClaimName != nil {
			return *status.ResourceClaimName
		}
	}
	return ""
}
