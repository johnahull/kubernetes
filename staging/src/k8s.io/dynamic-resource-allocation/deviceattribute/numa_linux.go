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
	"sort"
	"strconv"
	"strings"

	resourceapi "k8s.io/api/resource/v1"
)

// GetNUMANodesByPCIBusID returns the list of NUMA nodes that are equidistant
// to a PCI device, using the ACPI SLIT distance matrix.
//
// On hardware with a shared I/O die (e.g., AMD EPYC chiplets), a PCI device
// may be equidistant to multiple memory controllers. The kernel reports a
// single numa_node, but the SLIT distances reveal the true topology. This
// function reads the device's reported numa_node, then reads the SLIT
// distance row for that node to find all nodes at the same minimum distance.
//
// For example, on a 2-socket system with 4 NUMA nodes per socket and a
// shared I/O die, a GPU reporting numa_node=0 with distances [10, 12, 12, 12, 32, 32, 32, 32]
// returns [0, 1, 2, 3] — all nodes on the same socket at distance 12.
//
// If SLIT distances are unavailable, falls back to a single-element list
// containing only the reported numa_node.
//
// Requires the DRAListTypeAttributes feature gate for the IntValues field.
func GetNUMANodesByPCIBusID(pciBusID string, mods ...MachineModifier) (DeviceAttribute, error) {
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

	reportedNode, err := strconv.Atoi(strings.TrimSpace(string(data)))
	if err != nil {
		return DeviceAttribute{}, fmt.Errorf("failed to parse NUMA node for PCI Bus ID %s: %w", pciBusID, err)
	}

	if reportedNode < 0 {
		return DeviceAttribute{
			Name:  StandardDeviceAttributeNUMANode,
			Value: resourceapi.DeviceAttribute{IntValues: []int64{int64(reportedNode)}},
		}, nil
	}

	nodes, err := getEquidistantNUMANodes(mc, reportedNode)
	if err != nil {
		nodes = []int{reportedNode}
	}

	intValues := make([]int64, len(nodes))
	for i, n := range nodes {
		intValues[i] = int64(n)
	}

	return DeviceAttribute{
		Name:  StandardDeviceAttributeNUMANode,
		Value: resourceapi.DeviceAttribute{IntValues: intValues},
	}, nil
}

// getEquidistantNUMANodes reads the SLIT distance row for the given NUMA node
// and returns all nodes at the minimum non-self distance, plus the node itself.
//
// The SLIT distance file at /sys/devices/system/node/nodeN/distance contains
// space-separated integers: one distance value per NUMA node. Distance 10 is
// self (LOCAL_DISTANCE). The minimum non-self distance identifies nodes on the
// same I/O die or closest interconnect domain.
func getEquidistantNUMANodes(mc machine, node int) ([]int, error) {
	distPath := filepath.Join("devices", "system", "node", fmt.Sprintf("node%d", node), "distance")
	data, err := fs.ReadFile(mc.sysfs, distPath)
	if err != nil {
		return nil, fmt.Errorf("failed to read SLIT distances for NUMA node %d: %w", node, err)
	}

	fields := strings.Fields(strings.TrimSpace(string(data)))
	if len(fields) == 0 {
		return nil, fmt.Errorf("empty distance file for NUMA node %d", node)
	}

	distances := make([]int, len(fields))
	for i, f := range fields {
		d, err := strconv.Atoi(f)
		if err != nil {
			return nil, fmt.Errorf("failed to parse distance value %q for NUMA node %d: %w", f, node, err)
		}
		distances[i] = d
	}

	// Find minimum non-self distance (self is always 10 = LOCAL_DISTANCE)
	minDist := -1
	for i, d := range distances {
		if i == node {
			continue
		}
		if minDist == -1 || d < minDist {
			minDist = d
		}
	}

	// Single-node system: no non-self distances
	if minDist == -1 {
		return []int{node}, nil
	}

	// Collect self + all nodes at minimum distance
	var nodes []int
	for i, d := range distances {
		if i == node || d == minDist {
			nodes = append(nodes, i)
		}
	}

	sort.Ints(nodes)
	return nodes, nil
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
