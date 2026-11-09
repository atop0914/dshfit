package cli

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestServeRefusesArguments(t *testing.T) {
	// Silently ignoring `serve --host x` would leave the caller believing the
	// sidecar took a default host, while every request still has to name one.
	var out, errOut strings.Builder
	code := Run(strings.NewReader(""), []string{"serve", "--host", "0.2.2-rc.1"}, &out, &errOut)

	if code != 2 {
		t.Fatalf("exit = %d, want 2", code)
	}
	if out.Len() != 0 {
		t.Errorf("stdout carries protocol only, got %q", out.String())
	}
	if !strings.Contains(errOut.String(), "takes no arguments") {
		t.Errorf("stderr = %q", errOut.String())
	}
}

func TestServeWritesOneJSONLinePerRequest(t *testing.T) {
	var out, errOut strings.Builder
	code := Run(strings.NewReader(`{"id":1,"method":"version"}`+"\n"), []string{"serve"}, &out, &errOut)

	if code != 0 {
		t.Fatalf("exit = %d, want 0; stderr=%s", code, errOut.String())
	}
	lines := strings.Split(strings.TrimSuffix(out.String(), "\n"), "\n")
	if len(lines) != 1 {
		t.Fatalf("got %d lines, want 1: %q", len(lines), out.String())
	}
	var resp struct {
		ID     json.RawMessage `json:"id"`
		Result json.RawMessage `json:"result"`
	}
	if err := json.Unmarshal([]byte(lines[0]), &resp); err != nil {
		t.Fatalf("line is not JSON: %v\n%s", err, lines[0])
	}
	if string(resp.ID) != "1" {
		t.Errorf("id = %s", resp.ID)
	}
	if len(resp.Result) == 0 {
		t.Error("no result for a version request")
	}
	if errOut.Len() != 0 {
		t.Errorf("stderr should be empty on the happy path, got %q", errOut.String())
	}
}
