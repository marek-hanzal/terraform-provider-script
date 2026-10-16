package provider

import (
	"context"
	"crypto/sha256"
	"fmt"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestExecute(t *testing.T) {
	node, err := testNode()
	if err != nil {
		t.Fatal(err)
	}
	node, err = filepath.Abs(node)
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("AWS_SECRET_ACCESS_KEY", "parent-cloud-secret")
	t.Setenv("NODE_OPTIONS", "--definitely-invalid")
	for _, scenario := range []struct {
		name, source, failure string
		timeout               time.Duration
		canceled, changed     bool
	}{
		{name: "explicit environment and working directory only", source: `import {writeFileSync} from 'node:fs'; if (process.env.AWS_SECRET_ACCESS_KEY || process.env.NODE_OPTIONS || process.env.HOME || process.env.PATH || process.env.INPUT !== 'safe input') process.exit(9); writeFileSync('ran', 'ok');`, timeout: 5 * time.Second},
		{name: "redacts child failure", source: `console.log('db-password'); console.error('db-password'); process.exit(4);`, failure: "exited unsuccessfully", timeout: 5 * time.Second},
		{name: "bounds and reaps timeout", source: `setInterval(() => {}, 1000);`, failure: "interrupted", timeout: 200 * time.Millisecond},
		{name: "respects cancellation", source: `setInterval(() => {}, 1000);`, failure: "interrupted", timeout: time.Minute, canceled: true},
		{name: "rejects changed artifact before execution", source: `import {writeFileSync} from 'node:fs'; writeFileSync('ran', 'bad');`, failure: "changed after planning", timeout: 5 * time.Second, changed: true},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			directory := t.TempDir()
			script := filepath.Join(directory, "program with spaces.mjs")
			if err := os.WriteFile(script, []byte(scenario.source), 0600); err != nil {
				t.Fatal(err)
			}
			hash := fmt.Sprintf("%x", sha256.Sum256([]byte(scenario.source)))
			if scenario.changed {
				hash = strings.Repeat("0", 64)
			}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			if scenario.canceled {
				cancel()
			}
			started := time.Now()
			_, err := invoke(ctx, invocation{Node: node, Script: script, SHA256: hash, Directory: directory, Environment: map[string]string{"INPUT": "safe input"}, Timeout: scenario.timeout})
			if scenario.failure == "" {
				if err != nil {
					t.Fatal(err)
				}
				if data, err := os.ReadFile(filepath.Join(directory, "ran")); err != nil || string(data) != "ok" {
					t.Fatalf("missing successful execution: %q %v", data, err)
				}
			} else {
				if err == nil || !strings.Contains(err.Error(), scenario.failure) || strings.Contains(err.Error(), "db-password") {
					t.Fatalf("unexpected diagnostic: %v", err)
				}
				if _, err := os.Stat(filepath.Join(directory, "ran")); !os.IsNotExist(err) {
					t.Fatal("unexpected execution")
				}
			}
			if (scenario.canceled || scenario.timeout < time.Second) && time.Since(started) > 3*time.Second {
				t.Fatal("child was not reaped promptly")
			}
		})
	}
}

// Optional null environment must become an explicit empty child environment.
func TestOptionalEnvironment(t *testing.T) {
	directory := t.TempDir()
	script := filepath.Join(directory, "empty.mjs")
	source := `if (process.env.AWS_SECRET_ACCESS_KEY) process.exit(8);`
	if err := os.WriteFile(script, []byte(source), 0600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("AWS_SECRET_ACCESS_KEY", "parent-secret")
	node, err := testNode()
	if err != nil {
		t.Fatal(err)
	}
	r := runResource{node: node}
	model := runModel{
		Script: types.StringValue(script), SHA256: types.StringValue(fmt.Sprintf("%x", sha256.Sum256([]byte(source)))),
		WorkingDirectory: types.StringNull(), Environment: types.MapNull(types.StringType), TimeoutSeconds: types.Int64Value(5),
	}
	if err := r.execute(context.Background(), model); err != nil {
		t.Fatal(err)
	}
}
