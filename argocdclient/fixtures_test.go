package argocdclient

import (
	"encoding/json"
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
