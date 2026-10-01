package argocdclient

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestFixtures_CarryNoClusterInventory guards the recorded resource-tree fixtures.
// This repository is public and the fixtures are recorded from a real cluster, so
// they must not carry the cluster inventory ArgoCD adds to resource-tree responses:
// the top-level "hosts" block (node names, machine IDs, kernel/kubelet/containerd
// versions), per-node "images" (registry and mesh sidecar versions) and Pod "Node"
// info entries.
func TestFixtures_CarryNoClusterInventory(t *testing.T) {
	files, err := filepath.Glob(filepath.Join("testdata", "resource-tree-*.json"))
	if err != nil || len(files) == 0 {
		t.Fatalf("no resource-tree fixtures found: %v", err)
	}
	for _, f := range files {
		raw, err := os.ReadFile(f)
		if err != nil {
			t.Fatalf("read %s: %v", f, err)
		}
		var tree struct {
			Hosts json.RawMessage `json:"hosts"`
			Nodes []struct {
				Kind   string   `json:"kind"`
				Name   string   `json:"name"`
				Images []string `json:"images"`
				Info   []struct {
					Name string `json:"name"`
				} `json:"info"`
			} `json:"nodes"`
		}
		if err := json.Unmarshal(raw, &tree); err != nil {
			t.Fatalf("decode %s: %v", f, err)
		}
		if tree.Hosts != nil {
			t.Errorf("%s carries a top-level hosts block (cluster node inventory)", f)
		}
		for _, key := range []string{`"machineID"`, `"systemUUID"`, `"bootID"`, `"kubeletVersion"`} {
			if strings.Contains(string(raw), key) {
				t.Errorf("%s contains node identifier %s", f, key)
			}
		}
		for _, n := range tree.Nodes {
			if len(n.Images) > 0 {
				t.Errorf("%s: %s %s lists container images %v", f, n.Kind, n.Name, n.Images)
			}
			for _, i := range n.Info {
				if i.Name == "Node" {
					t.Errorf("%s: %s %s names the cluster node it runs on", f, n.Kind, n.Name)
				}
			}
		}
	}
}

// TestFixtures_EveryRecordingCarriesNoNodeIdentity checks every recorded fixture, not only
// the resource trees. This repository is public and the fixtures are recorded from a real
// cluster, so none of them may carry node identity, pod or host addresses, ingress details
// or the last-applied-configuration annotation. A live-resource envelope's inner manifest
// is decoded and checked as well.
func TestFixtures_EveryRecordingCarriesNoNodeIdentity(t *testing.T) {
	files, err := filepath.Glob(filepath.Join("testdata", "*.json"))
	if err != nil || len(files) == 0 {
		t.Fatalf("no fixtures found: %v", err)
	}
	for _, f := range files {
		raw, err := os.ReadFile(f)
		if err != nil {
			t.Fatalf("read %s: %v", f, err)
		}
		var doc any
		if err := json.Unmarshal(raw, &doc); err != nil {
			t.Fatalf("decode %s: %v", f, err)
		}
		docs := []any{doc}
		if top, ok := doc.(map[string]any); ok {
			if _, hasHosts := top["hosts"]; hasHosts {
				t.Errorf("%s carries a top-level hosts block", f)
			}
			if manifest, ok := top["manifest"].(string); ok {
				var inner any
				if err := json.Unmarshal([]byte(manifest), &inner); err != nil {
					t.Fatalf("decode manifest of %s: %v", f, err)
				}
				docs = append(docs, inner)
			}
		}
		for _, d := range docs {
			for _, problem := range inventoryProblems("$", d) {
				t.Errorf("%s: %s", f, problem)
			}
		}
		for _, marker := range []string{"machineID", "systemUUID", "bootID", "kubeletVersion"} {
			if strings.Contains(string(raw), marker) {
				t.Errorf("%s contains node identifier %s", f, marker)
			}
		}
	}
}

// inventoryProblems walks a decoded JSON value and describes every cluster-inventory marker in it.
func inventoryProblems(path string, v any) []string {
	var problems []string
	switch value := v.(type) {
	case map[string]any:
		for key, child := range value {
			childPath := path + "." + key
			switch key {
			case "nodeName", "hostIP", "podIP":
				problems = append(problems, childPath+" names the node or address a pod runs on")
			case "kubectl.kubernetes.io/last-applied-configuration":
				problems = append(problems, childPath+" is the last-applied-configuration annotation")
			case "images":
				if list, ok := child.([]any); ok && len(list) > 0 {
					problems = append(problems, childPath+" lists container images")
				}
			case "networkingInfo":
				if info, ok := child.(map[string]any); ok {
					for _, forbidden := range []string{"ingress", "externalURLs"} {
						if _, present := info[forbidden]; present {
							problems = append(problems, childPath+"."+forbidden+" carries ingress details")
						}
					}
				}
			case "info":
				if entries, ok := child.([]any); ok {
					for _, entry := range entries {
						if m, ok := entry.(map[string]any); ok && m["name"] == "Node" {
							problems = append(problems, childPath+" names the cluster node")
						}
					}
				}
			}
			problems = append(problems, inventoryProblems(childPath, child)...)
		}
	case []any:
		for i, child := range value {
			problems = append(problems, inventoryProblems(fmt.Sprintf("%s[%d]", path, i), child)...)
		}
	}
	return problems
}
