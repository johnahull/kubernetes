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

	node, err := readNUMANode(mc, pciBusID)
	if err != nil {
		return DeviceAttribute{}, err
	}

	return DeviceAttribute{
		Name:  StandardDeviceAttributeNUMANode,
		Value: resourceapi.DeviceAttribute{IntValue: ptr.To(int64(node))},
	}, nil
}

// GetLocalNUMANodesByPCIBusID returns the list of NUMA nodes equidistant to a
// PCI device, using the ACPI SLIT distance matrix with a socket boundary
// safety check.
//
// On hardware with a shared I/O die (e.g., AMD EPYC chiplets), a PCI device
// may be equidistant to multiple memory controllers. The kernel reports a
// single numa_node, but the SLIT distances reveal the true topology. This
// function reads the device's reported numa_node, then reads the SLIT
// distance row to find all same-socket nodes at the minimum non-self distance.
//
// The socket filter prevents cross-socket matches on systems where all
// non-self SLIT distances are equal (e.g., 2-socket NPS1 with distances
// [10, 32] — without the filter, both nodes would be included).
//
// If SLIT distances are unavailable, falls back to a single-element list
// containing only the reported numa_node.
//
// Requires the DRAListTypeAttributes feature gate for the IntValues field.
func GetLocalNUMANodesByPCIBusID(pciBusID string, mods ...MachineModifier) (DeviceAttribute, error) {
	var mc machine
	initDefaultMachine(&mc)
	for _, mod := range mods {
		mod(&mc)
	}

	if err := verifyPCIBDFFormat(pciBusID); err != nil {
		return DeviceAttribute{}, err
	}

	reportedNode, err := readNUMANode(mc, pciBusID)
	if err != nil {
		return DeviceAttribute{}, err
	}

	if reportedNode < 0 {
		return DeviceAttribute{
			Name:  StandardDeviceAttributeLocalNUMANodes,
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
		Name:  StandardDeviceAttributeLocalNUMANodes,
		Value: resourceapi.DeviceAttribute{IntValues: intValues},
	}, nil
}

// readNUMANode reads the numa_node sysfs file for a PCI device.
func readNUMANode(mc machine, pciBusID string) (int, error) {
	numaNodePath := filepath.Join("bus", "pci", "devices", pciBusID, "numa_node")
	data, err := fs.ReadFile(mc.sysfs, numaNodePath)
	if err != nil {
		return -1, fmt.Errorf("failed to read NUMA node for PCI Bus ID %s: %w", pciBusID, err)
	}

	node, err := strconv.Atoi(strings.TrimSpace(string(data)))
	if err != nil {
		return -1, fmt.Errorf("failed to parse NUMA node for PCI Bus ID %s: %w", pciBusID, err)
	}

	return node, nil
}

// getEquidistantNUMANodes reads the SLIT distance row for the given NUMA node
// and returns all same-socket nodes at the minimum non-self distance, plus
// the node itself.
//
// The socket filter reads each candidate node's cpulist, picks the first CPU,
// and compares its physical_package_id to the reported node's socket. Nodes
// on a different socket are excluded even if their SLIT distance matches.
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

	if node >= len(fields) {
		return nil, fmt.Errorf("NUMA node %d out of range for distance table with %d entries", node, len(fields))
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

	// Single-node system
	if minDist == -1 {
		return []int{node}, nil
	}

	// Cache socket lookups to avoid re-reading sysfs for each candidate
	socketCache := make(map[int]int)
	lookupSocket := func(n int) (int, bool) {
		if s, ok := socketCache[n]; ok {
			return s, true
		}
		s, err := getSocketForNUMANode(mc, n)
		if err != nil {
			return -1, false
		}
		socketCache[n] = s
		return s, true
	}

	reportedSocket, haveSocket := lookupSocket(node)

	// Collect self + same-socket nodes at minimum distance
	var nodes []int
	for i, d := range distances {
		if i == node {
			nodes = append(nodes, i)
			continue
		}
		if d != minDist {
			continue
		}
		if haveSocket {
			candidateSocket, ok := lookupSocket(i)
			if ok && candidateSocket != reportedSocket {
				continue
			}
		}
		nodes = append(nodes, i)
	}

	sort.Ints(nodes)
	return nodes, nil
}

// getSocketForNUMANode returns the physical_package_id (socket) for a NUMA
// node by reading its cpulist, picking the first CPU, and reading that CPU's
// physical_package_id from sysfs.
func getSocketForNUMANode(mc machine, node int) (int, error) {
	cpulistPath := filepath.Join("devices", "system", "node", fmt.Sprintf("node%d", node), "cpulist")
	data, err := fs.ReadFile(mc.sysfs, cpulistPath)
	if err != nil {
		return -1, fmt.Errorf("failed to read cpulist for NUMA node %d: %w", node, err)
	}

	cpus := parseCPUList(strings.TrimSpace(string(data)))
	if len(cpus) == 0 {
		return -1, fmt.Errorf("no CPUs found for NUMA node %d", node)
	}

	pkgPath := filepath.Join("devices", "system", "cpu", fmt.Sprintf("cpu%d", cpus[0]), "topology", "physical_package_id")
	pkgData, err := fs.ReadFile(mc.sysfs, pkgPath)
	if err != nil {
		return -1, fmt.Errorf("failed to read physical_package_id for CPU %d: %w", cpus[0], err)
	}

	socketID, err := strconv.Atoi(strings.TrimSpace(string(pkgData)))
	if err != nil {
		return -1, fmt.Errorf("failed to parse physical_package_id for CPU %d: %w", cpus[0], err)
	}

	return socketID, nil
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
