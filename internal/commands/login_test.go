package commands

import (
	"bytes"
	"strings"
	"testing"
	"time"
)

func TestRenderQRDoesNotPrintLoginURI(t *testing.T) {
	var out bytes.Buffer
	uri := "tg://login?token=secret-token"
	if err := renderQR(&out, uri, time.Unix(1700000000, 0)); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out.String(), uri) || !strings.Contains(out.String(), "Scan this Telegram QR code") || !strings.Contains(out.String(), "██") {
		t.Fatalf("rendered QR output=%q", out.String())
	}
}
