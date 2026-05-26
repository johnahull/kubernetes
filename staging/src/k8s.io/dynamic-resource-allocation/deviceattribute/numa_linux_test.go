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

	"k8s.io/utils/ptr"

	resourceapi "k8s.io/api/resource/v1"
)

func TestGetNUMANodeByPCIBusID(t *testing.T) {
	pciBusID := "0000:02:00.0"

	tests := map[string]struct {
		testMachineSetup  func(t *testing.T, testRootPath string)
		address           string
		expectedAttribute *DeviceAttribute
		expectsError      bool
		expectedErrMsg    string
	}{
		"valid NUMA node 0": {
			testMachineSetup: func(t *testing.T, testRootPath string) {
				numaNodeFile := filepath.Join(testRootPath, "bus", "pci", "devices", pciBusID, "numa_node")
				mkDirAll(t, filepath.Dir(numaNodeFile))
				writeFile(t, numaNodeFile, "0\n")
			},
			address: pciBusID,
			expectedAttribute: &DeviceAttribute{
				Name:  StandardDeviceAttributeNUMANode,
				Value: resourceapi.DeviceAttribute{IntValue: ptr.To(int64(0))},
			},
		},
		"valid NUMA node 1": {
			testMachineSetup: func(t *testing.T, testRootPath string) {
				numaNodeFile := filepath.Join(testRootPath, "bus", "pci", "devices", pciBusID, "numa_node")
				mkDirAll(t, filepath.Dir(numaNodeFile))
				writeFile(t, numaNodeFile, "1\n")
			},
			address: pciBusID,
			expectedAttribute: &DeviceAttribute{
				Name:  StandardDeviceAttributeNUMANode,
				Value: resourceapi.DeviceAttribute{IntValue: ptr.To(int64(1))},
			},
		},
		"valid NUMA no node (-1)": {
			testMachineSetup: func(t *testing.T, testRootPath string) {
				numaNodeFile := filepath.Join(testRootPath, "bus", "pci", "devices", pciBusID, "numa_node")
				mkDirAll(t, filepath.Dir(numaNodeFile))
				writeFile(t, numaNodeFile, "-1\n")
			},
			address: pciBusID,
			expectedAttribute: &DeviceAttribute{
				Name:  StandardDeviceAttributeNUMANode,
				Value: resourceapi.DeviceAttribute{IntValue: ptr.To(int64(-1))},
			},
		},
		"invalid empty PCI Bus ID": {
			address:        "",
			expectsError:   true,
			expectedErrMsg: "PCI Bus ID cannot be empty",
		},
		"invalid PCI Bus ID format": {
			address:        "invalid-pci-id",
			expectsError:   true,
			expectedErrMsg: "invalid PCI Bus ID format: invalid-pci-id",
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
		"invalid numa_node content": {
			testMachineSetup: func(t *testing.T, testRootPath string) {
				numaNodeFile := filepath.Join(testRootPath, "bus", "pci", "devices", pciBusID, "numa_node")
				mkDirAll(t, filepath.Dir(numaNodeFile))
				writeFile(t, numaNodeFile, "not-a-number\n")
			},
			address:        pciBusID,
			expectsError:   true,
			expectedErrMsg: "failed to parse NUMA node",
		},
	}

	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			testMachinePath := t.TempDir()
			if test.testMachineSetup != nil {
				test.testMachineSetup(t, testMachinePath)
			}
			got, err := GetNUMANodeByPCIBusID(test.address, WithFSFromRoot(testMachinePath))
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
			if !reflect.DeepEqual(got, *test.expectedAttribute) {
				t.Errorf("Expected attribute %v, got %v", test.expectedAttribute, got)
			}
		})
	}
}

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
