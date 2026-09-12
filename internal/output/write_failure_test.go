package output

import (
	"bytes"
	"errors"
	"io"
	"testing"
)

type failedWriter struct{ short bool }

func (w failedWriter) Write(p []byte) (int, error) {
	if w.short {
		return len(p) / 2, nil
	}
	return 0, errors.New("broken output")
}

func TestEmitDoesNotReportSuccessWhenOutputFails(t *testing.T) {
	for _, jsonMode := range []bool{true, false} {
		for _, short := range []bool{true, false} {
			var stderr bytes.Buffer
			code := Emit(Success("send", map[string]any{"message_id": 7}, "request", nil), EmitOptions{
				JSON: jsonMode, Stdout: failedWriter{short: short}, Stderr: &stderr,
			})
			if code != Generic {
				t.Fatalf("json=%v short=%v code=%v", jsonMode, short, code)
			}
			if stderr.Len() == 0 {
				t.Fatal("missing output failure diagnostic")
			}
		}
	}
}

func TestEmitFailureWithBrokenStderrIsStillFailure(t *testing.T) {
	if code := Emit(Fail("send", BadArgs, "invalid", "r", nil), EmitOptions{Stdout: io.Discard, Stderr: failedWriter{}}); code == OK {
		t.Fatal("broken stderr reported success")
	}
}
