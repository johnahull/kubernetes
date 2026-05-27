/*
Copyright 2025 The Kubernetes Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package deviceattribute

import (
	resourceapi "k8s.io/api/resource/v1"
)

const (
	// StandardDeviceAttributePrefix is the prefix used for standard device attributes.
	StandardDeviceAttributePrefix = "resource.kubernetes.io/"

	// StandardDeviceAttributePCIeRoot is a standard device attribute name
	// which describe PCIe Root Complex of the PCIe device.
	// The value is a string value in the format `pci<domain>:<bus>`,
	// referring to a PCIe (Peripheral Component Interconnect Express) Root Complex.
	// This attribute can be used to identify devices that share the same PCIe Root Complex.
	StandardDeviceAttributePCIeRoot resourceapi.QualifiedName = StandardDeviceAttributePrefix + "pcieRoot"

	// StandardDeviceAttributePCIBusID is a standard device attribute name
	// which describes the PCI Bus address of the PCI device.
	// The value is a string value in the extended BDF notation (Domain:Bus:Device.Function),
	// referring to a PCI (Peripheral Component Interconnect) device.
	// This attribute can be used to identify PCI devices.
	StandardDeviceAttributePCIBusID resourceapi.QualifiedName = StandardDeviceAttributePrefix + "pciBusID"

	// StandardDeviceAttributeNUMANode is a standard device attribute name
	// which identifies the NUMA topology of a device. The value is an
	// integer list (requires DRAListTypeAttributes feature gate) where
	// the first element is the device's physical NUMA node (from the
	// kernel's numa_node sysfs entry) and any additional elements are
	// NUMA nodes equidistant to the device, derived from the ACPI SLIT
	// distance matrix. A socket boundary filter excludes cross-socket
	// matches.
	//
	// For CPU and memory devices (which ARE a NUMA node), this is a
	// single-element list [N]. For I/O devices on shared I/O die
	// hardware (e.g., AMD EPYC chiplets under NPS4), this may include
	// multiple NUMA nodes: [physical, equidistant1, equidistant2, ...].
	//
	// With DRAListTypeAttributes, matchAttribute uses non-empty set
	// intersection (KEP-5491), so a CPU device [4] matches an I/O
	// device [4, 5, 6, 7] because {4} ∩ {4,5,6,7} ≠ ∅.
	StandardDeviceAttributeNUMANode resourceapi.QualifiedName = StandardDeviceAttributePrefix + "numaNode"
)

// DeviceAttribute represents a device attribute name and its value
type DeviceAttribute struct {
	// Name is the qualified name of the device attribute.
	Name resourceapi.QualifiedName
	// Value is the value of the device attribute.
	Value resourceapi.DeviceAttribute
}
