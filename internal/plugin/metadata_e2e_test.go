package plugin

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/Aswanidev-vs/chest/internal/models"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestMetadataPluginEndToEnd builds the rich-meta example plugin and talks to
// it over the real hashicorp/go-plugin handshake (protocol version 2),
// verifying wire types, capability filtering, and both Inspect paths
// (RAW format sniffing and XMP date extraction) against a spawned process.
// goleak is intentionally not used here: go-plugin owns internal goroutines
// that outlive client.Kill by design.
func TestMetadataPluginEndToEnd(t *testing.T) {
	// Build the example plugin binary.
	exePath := filepath.Join(t.TempDir(), "rich-meta.exe")
	build := exec.Command("go", "build", "-o", exePath, "../../examples/plugins/rich-meta")
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build example plugin: %v\n%s", err, out)
	}

	// Stage a plugin source directory (binary + manifest) and install it.
	srcDir := t.TempDir()
	manifest, err := os.ReadFile("../../examples/plugins/rich-meta/manifest.json")
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(filepath.Join(srcDir, "manifest.json"), manifest, 0644))
	exeBytes, err := os.ReadFile(exePath)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(filepath.Join(srcDir, "rich-meta.exe"), exeBytes, 0755))

	mgr, err := NewManager(filepath.Join(t.TempDir(), "installed"))
	require.NoError(t, err)
	installed, err := mgr.Install(srcDir)
	require.NoError(t, err)
	assert.Equal(t, "rich-meta", installed.Name)

	services, cleanup := mgr.LoadAllMetadata()
	require.Len(t, services, 1, "metadata plugin should be loaded over RPC")
	defer cleanup()

	// GetInfo round-trips the manifest through RPC.
	info, err := services[0].GetInfo()
	require.NoError(t, err)
	assert.Equal(t, "rich-meta", info.Name)
	assert.Contains(t, info.Capabilities, MetadataCapability)

	// CR3 sample: sniffed format, MIME and category.
	cr3 := make([]byte, 0, 32)
	cr3 = append(cr3, 0x00, 0x00, 0x00, 0x18)
	cr3 = append(cr3, []byte("ftyp")...)
	cr3 = append(cr3, []byte("crx ")...)
	cr3 = append(cr3, 0x00, 0x00, 0x00, 0x00)
	cr3 = append(cr3, make([]byte, 64)...)
	cr3Path := filepath.Join(t.TempDir(), "IMG_1234.CR3")
	require.NoError(t, os.WriteFile(cr3Path, cr3, 0644))

	cr3Sample := SampleFromFile(models.File{
		Path: cr3Path, Name: "IMG_1234.CR3", Extension: "cr3", Size: int64(len(cr3)),
	})
	cr3Meta, err := services[0].Inspect(cr3Sample)
	require.NoError(t, err)
	assert.Equal(t, "CR3", cr3Meta.Format)
	assert.Equal(t, "image/x-cr3", cr3Meta.MIMEType)
	assert.Equal(t, "Image", cr3Meta.Category)
	assert.Equal(t, "ISO-BMFF/CRX", cr3Meta.Fields["raw_container"])

	// Non-RAW sample: plugin contributes nothing.
	plainPath := filepath.Join(t.TempDir(), "notes.txt")
	require.NoError(t, os.WriteFile(plainPath, []byte("hello world"), 0644))
	plainSample := SampleFromFile(models.File{
		Path: plainPath, Name: "notes.txt", Extension: "txt", Size: 11,
	})
	plainMeta, err := services[0].Inspect(plainSample)
	require.NoError(t, err)
	assert.False(t, plainMeta.HasContent(), "plain text should not be enriched")

	// XMP sample: photoshop:DateCreated becomes the taken date.
	xmp := `<?xpacket begin="" id="W5M0MpCehiHzreSzNTczkc9d"?>
<x:xmpmeta xmlns:x="adobe:ns:meta/">
 <rdf:RDF xmlns:rdf="http://www.w3.org/1999/02/22-rdf-syntax-ns#">
  <rdf:Description rdf:about="" xmlns:photoshop="http://ns.adobe.com/photoshop/1.0/">
   <photoshop:DateCreated>2021-11-17T09:31:07</photoshop:DateCreated>
  </rdf:Description>
 </rdf:RDF>
</x:xmpmeta>`
	xmpPath := filepath.Join(t.TempDir(), "IMG_0001.xmp")
	require.NoError(t, os.WriteFile(xmpPath, []byte(xmp), 0644))

	xmpSample := SampleFromFile(models.File{
		Path: xmpPath, Name: "IMG_0001.xmp", Extension: "xmp", Size: int64(len(xmp)),
	})
	xmpMeta, err := services[0].Inspect(xmpSample)
	require.NoError(t, err)
	require.NotNil(t, xmpMeta.TakenDate)
	wantDate := time.Date(2021, 11, 17, 9, 31, 7, 0, time.UTC)
	assert.True(t, xmpMeta.TakenDate.Equal(wantDate), "TakenDate = %v, want %v", xmpMeta.TakenDate, wantDate)
	assert.Equal(t, "xmp:photoshop:DateCreated", xmpMeta.DateSource)
	assert.Equal(t, "2021-11-17T09:31:07", xmpMeta.Fields["xmp_date_created"])
}
