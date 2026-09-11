package cli

import (
	"bytes"
	"strings"
	"testing"
)

func TestPrintSpeedTableRenders(t *testing.T) {
	rows := []speedResult{
		{label: "Ookla ping", ok: true, detail: "194 ms (jitter 12 ms)"},
		{label: "Ookla download", ok: true, detail: "47.10 Mbps"},
		{label: "Ookla upload", ok: true, detail: "45.16 Mbps"},
		{label: "Cloudflare ping", ok: true, detail: "52 ms (jitter 6 ms)"},
		{label: "Cloudflare download", ok: true, detail: "49.55 Mbps"},
		{label: "Cloudflare upload", ok: false, err: "connect: connection refused"},
	}
	var b bytes.Buffer
	printSpeedTable(&b, rows)
	out := b.String()
	for _, want := range []string{"Ookla", "Cloudflare", "FAIL", "jitter 6 ms", "CHEST SPEED TEST"} {
		if !strings.Contains(out, want) {
			t.Fatalf("table missing %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "EXTRA") || strings.Contains(out, "MISSING") {
		t.Fatalf("format verb leak:\n%s", out)
	}
	t.Logf("\n%s", out)
}