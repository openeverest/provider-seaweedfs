package definition

import (
	"os"
	"strings"
	"testing"
)

// The master volume size limit is easy to confuse with the size of the
// storage volume, so the UI must explain it.
func TestVolumeSizeLimitHasHelperText(t *testing.T) {
	files := []string{
		"topologies/standalone/topology.yaml",
		"../charts/provider-seaweedfs/generated/provider-spec.yaml",
	}
	for _, f := range files {
		data, err := os.ReadFile(f)
		if err != nil {
			t.Fatalf("reading %s: %v", f, err)
		}
		_, after, found := strings.Cut(string(data), "volumeSizeLimitMB:")
		if !found {
			t.Fatalf("%s: volumeSizeLimitMB not found", f)
		}
		// Only look at this field's block, up to the next "path:" line.
		block, _, _ := strings.Cut(after, "path:")
		if !strings.Contains(block, "helperText:") {
			t.Errorf("%s: volumeSizeLimitMB has no helperText", f)
		}
	}
}
