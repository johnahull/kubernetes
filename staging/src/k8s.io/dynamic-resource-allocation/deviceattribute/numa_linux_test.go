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
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	resourceapi "k8s.io/api/resource/v1"
)

func TestGetNUMANodeForCPU(t *testing.T) {
	tests := map[string]struct {
		testMachineSetup func(t *testing.T, testRootPath string)
		cpuID            int
		expectedNode     int
		expectsError     bool
		expectedErrMsg   string
	}{
		"CPU 0 on NUMA node 0": {
			testMachineSetup: func(t *testing.T, testRootPath string) {
				setupNUMANode(t, testRootPath, 0, "0-3")
			},
			cpuID:        0,
			expectedNode: 0,
		},
		"CPU 5 on NUMA node 1": {
			testMachineSetup: func(t *testing.T, testRootPath string) {
				setupNUMANode(t, testRootPath, 0, "0-3")
				setupNUMANode(t, testRootPath, 1, "4-7")
			},
			cpuID:        5,
			expectedNode: 1,
		},
		"CPU in range with gaps": {
			testMachineSetup: func(t *testing.T, testRootPath string) {
				setupNUMANode(t, testRootPath, 0, "0-3,8-11")
				setupNUMANode(t, testRootPath, 1, "4-7,12-15")
			},
			cpuID:        9,
			expectedNode: 0,
		},
		"CPU on high NUMA node": {
			testMachineSetup: func(t *testing.T, testRootPath string) {
				setupNUMANode(t, testRootPath, 0, "0-31")
				setupNUMANode(t, testRootPath, 1, "32-63")
				setupNUMANode(t, testRootPath, 2, "64-95")
				setupNUMANode(t, testRootPath, 3, "96-127")
			},
			cpuID:        100,
			expectedNode: 3,
		},
		"CPU not found": {
			testMachineSetup: func(t *testing.T, testRootPath string) {
				setupNUMANode(t, testRootPath, 0, "0-3")
				setupNUMANode(t, testRootPath, 1, "4-7")
			},
			cpuID:          99,
			expectsError:   true,
			expectedErrMsg: "CPU 99 not found in any NUMA node",
		},
		"no NUMA nodes": {
			testMachineSetup: func(t *testing.T, testRootPath string) {
				mkDirAll(t, filepath.Join(testRootPath, "devices", "system", "node"))
			},
			cpuID:          0,
			expectsError:   true,
			expectedErrMsg: "CPU 0 not found in any NUMA node",
		},
	}

	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			testMachinePath := t.TempDir()
			if test.testMachineSetup != nil {
				test.testMachineSetup(t, testMachinePath)
			}
			got, err := GetNUMANodeForCPU(test.cpuID, WithFSFromRoot(testMachinePath))
			if test.expectsError {
				if err == nil {
					t.Errorf("Expected error but got none")
					return
				}
				if !strings.Contains(err.Error(), test.expectedErrMsg) {
					t.Errorf("Expected error message to contain %q, got %q", test.expectedErrMsg, err.Error())
				}
				return
			}
			if err != nil {
				t.Fatalf("Unexpected error: %v", err)
			}
			if got != test.expectedNode {
				t.Errorf("Expected NUMA node %d, got %d", test.expectedNode, got)
			}
		})
	}
}

func TestGetNUMANodesByPCIBusID(t *testing.T) {
	pciBusID := "0000:02:00.0"

	tests := map[string]struct {
		testMachineSetup func(t *testing.T, testRootPath string)
		address          string
		expectedValues   []int64
		expectsError     bool
		expectedErrMsg   string
	}{
		"shared I/O die - 4 equidistant nodes": {
			testMachineSetup: func(t *testing.T, testRootPath string) {
				setupPCINUMANode(t, testRootPath, pciBusID, "0")
				setupNUMADistance(t, testRootPath, 0, "10 12 12 12 32 32 32 32")
			},
			address:        pciBusID,
			expectedValues: []int64{0, 1, 2, 3},
		},
		"clear affinity - single node": {
			testMachineSetup: func(t *testing.T, testRootPath string) {
				setupPCINUMANode(t, testRootPath, pciBusID, "0")
				setupNUMADistance(t, testRootPath, 0, "10 32 32 32")
			},
			address:        pciBusID,
			expectedValues: []int64{0, 1, 2, 3},
		},
		"2 socket - same socket only": {
			testMachineSetup: func(t *testing.T, testRootPath string) {
				setupPCINUMANode(t, testRootPath, pciBusID, "0")
				setupNUMADistance(t, testRootPath, 0, "10 12 32 32")
			},
			address:        pciBusID,
			expectedValues: []int64{0, 1},
		},
		"single NUMA node system": {
			testMachineSetup: func(t *testing.T, testRootPath string) {
				setupPCINUMANode(t, testRootPath, pciBusID, "0")
				setupNUMADistance(t, testRootPath, 0, "10")
			},
			address:        pciBusID,
			expectedValues: []int64{0},
		},
		"no SLIT - fallback to single node": {
			testMachineSetup: func(t *testing.T, testRootPath string) {
				setupPCINUMANode(t, testRootPath, pciBusID, "1")
			},
			address:        pciBusID,
			expectedValues: []int64{1},
		},
		"numa_node -1 - passthrough": {
			testMachineSetup: func(t *testing.T, testRootPath string) {
				setupPCINUMANode(t, testRootPath, pciBusID, "-1")
			},
			address:        pciBusID,
			expectedValues: []int64{-1},
		},
		"device on NUMA 1 with shared I/O die": {
			testMachineSetup: func(t *testing.T, testRootPath string) {
				setupPCINUMANode(t, testRootPath, pciBusID, "1")
				setupNUMADistance(t, testRootPath, 1, "12 10 12 12 32 32 32 32")
			},
			address:        pciBusID,
			expectedValues: []int64{0, 1, 2, 3},
		},
		"invalid empty PCI Bus ID": {
			address:        "",
			expectsError:   true,
			expectedErrMsg: "PCI Bus ID cannot be empty",
		},
		"missing numa_node file": {
			testMachineSetup: func(t *testing.T, testRootPath string) {
				devDir := filepath.Join(testRootPath, "bus", "pci", "devices", pciBusID)
				mkDirAll(t, devDir)
			},
			address:        pciBusID,
			expectsError:   true,
			expectedErrMsg: "failed to read NUMA node",
		},
	}

	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			testMachinePath := t.TempDir()
			if test.testMachineSetup != nil {
				test.testMachineSetup(t, testMachinePath)
			}
			got, err := GetNUMANodesByPCIBusID(test.address, WithFSFromRoot(testMachinePath))
			if test.expectsError {
				if err == nil {
					t.Errorf("Expected error but got none")
					return
				}
				if !strings.Contains(err.Error(), test.expectedErrMsg) {
					t.Errorf("Expected error message to contain %q, got %q", test.expectedErrMsg, err.Error())
				}
				return
			}
			if err != nil {
				t.Fatalf("Unexpected error: %v", err)
			}
			expected := DeviceAttribute{
				Name:  StandardDeviceAttributeNUMANode,
				Value: resourceapi.DeviceAttribute{IntValues: test.expectedValues},
			}
			if !reflect.DeepEqual(got, expected) {
				t.Errorf("Expected attribute %v, got %v", expected, got)
			}
		})
	}
}

func setupPCINUMANode(t *testing.T, testRootPath string, pciBusID string, numaNode string) {
	t.Helper()
	numaNodeFile := filepath.Join(testRootPath, "bus", "pci", "devices", pciBusID, "numa_node")
	mkDirAll(t, filepath.Dir(numaNodeFile))
	writeFile(t, numaNodeFile, numaNode+"\n")
}

func setupNUMADistance(t *testing.T, testRootPath string, nodeNum int, distances string) {
	t.Helper()
	distFile := filepath.Join(testRootPath, "devices", "system", "node", fmt.Sprintf("node%d", nodeNum), "distance")
	mkDirAll(t, filepath.Dir(distFile))
	writeFile(t, distFile, distances+"\n")
}

func setupNUMANode(t *testing.T, testRootPath string, nodeNum int, cpulist string) {
	t.Helper()
	cpulistFile := filepath.Join(testRootPath, "devices", "system", "node", fmt.Sprintf("node%d", nodeNum), "cpulist")
	mkDirAll(t, filepath.Dir(cpulistFile))
	writeFile(t, cpulistFile, cpulist+"\n")
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatalf("Failed to write file %s: %v", path, err)
	}
}
