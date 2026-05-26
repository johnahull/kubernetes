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
	"fmt"
	"io/fs"
	"path/filepath"
	"strconv"
	"strings"

	resourceapi "k8s.io/api/resource/v1"
	"k8s.io/utils/ptr"
)

// GetNUMANodeByPCIBusID returns the NUMA node for a PCI device identified by
// its Bus-Device-Function address (e.g., "0000:04:1f.0").
//
// It reads /sys/bus/pci/devices/<BDF>/numa_node, which the kernel populates
// from the root bridge's ACPI _PXM proximity domain. The value identifies
// which memory controller services the device. A value of -1 means the
// kernel has no NUMA affinity information for the device.
func GetNUMANodeByPCIBusID(pciBusID string, mods ...MachineModifier) (DeviceAttribute, error) {
	var mc machine
	initDefaultMachine(&mc)
	for _, mod := range mods {
		mod(&mc)
	}

	if err := verifyPCIBDFFormat(pciBusID); err != nil {
		return DeviceAttribute{}, err
	}

	numaNodePath := filepath.Join("bus", "pci", "devices", pciBusID, "numa_node")
	data, err := fs.ReadFile(mc.sysfs, numaNodePath)
	if err != nil {
		return DeviceAttribute{}, fmt.Errorf("failed to read NUMA node for PCI Bus ID %s: %w", pciBusID, err)
	}

	node, err := strconv.Atoi(strings.TrimSpace(string(data)))
	if err != nil {
		return DeviceAttribute{}, fmt.Errorf("failed to parse NUMA node for PCI Bus ID %s: %w", pciBusID, err)
	}

	attr := DeviceAttribute{
		Name:  StandardDeviceAttributeNUMANode,
		Value: resourceapi.DeviceAttribute{IntValue: ptr.To(int64(node))},
	}

	return attr, nil
}

// GetNUMANodeForCPU returns the NUMA node ID for a given CPU core.
//
// It scans /sys/devices/system/node/node*/cpulist to find which NUMA node
// contains the given CPU ID. Returns an error if the CPU is not found in
// any NUMA node's cpulist.
func GetNUMANodeForCPU(cpuID int, mods ...MachineModifier) (int, error) {
	var mc machine
	initDefaultMachine(&mc)
	for _, mod := range mods {
		mod(&mc)
	}

	matches, err := fs.Glob(mc.sysfs, filepath.Join("devices", "system", "node", "node*", "cpulist"))
	if err != nil {
		return -1, fmt.Errorf("failed to glob NUMA node cpulists: %w", err)
	}

	for _, match := range matches {
		data, err := fs.ReadFile(mc.sysfs, match)
		if err != nil {
			continue
		}

		cpus := parseCPUList(strings.TrimSpace(string(data)))
		for _, cpu := range cpus {
			if cpu == cpuID {
				nodeDir := filepath.Base(filepath.Dir(match))
				nodeNum, err := strconv.Atoi(strings.TrimPrefix(nodeDir, "node"))
				if err != nil {
					return -1, fmt.Errorf("failed to parse NUMA node number from %s: %w", nodeDir, err)
				}
				return nodeNum, nil
			}
		}
	}

	return -1, fmt.Errorf("CPU %d not found in any NUMA node", cpuID)
}
