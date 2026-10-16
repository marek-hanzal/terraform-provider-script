package provider

import (
	"crypto/sha256"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Real local Terraform protects framework plan/state wiring, including environment-only updates.
func TestTerraformLifecycle(t *testing.T) {
	f := newTerraformFixture(t)
	directory := f.directory
	write := f.write
	write("main.tf", `terraform {
 required_providers { script = { source = "marek-hanzal/script", version = "= 0.2.0" } }
}
variable "input" { type = string }
variable "sha256" { type = string }
resource "script_run" "run" {
 script = abspath("${path.module}/program.mjs")
 sha256 = var.sha256
 environment = { INPUT = var.input }
 timeout_seconds = 5
}
resource "script_run" "downstream" {
 depends_on = [script_run.run]
 script = abspath("${path.module}/downstream.mjs")
 sha256 = filesha256("${path.module}/downstream.mjs")
 environment = { INPUT = var.input }
 timeout_seconds = 5
}
`)
	write("downstream.mjs", `import {appendFileSync} from 'node:fs'; appendFileSync('deployed', process.env.INPUT + '\n');`)
	source := `import {appendFileSync} from 'node:fs'; appendFileSync('calls', process.env.INPUT + '\n');`
	write("program.mjs", source)
	plannedHash := fmt.Sprintf("%x", sha256.Sum256([]byte(source)))
	f.env = append(f.env, "TF_VAR_input=one", "TF_VAR_sha256="+plannedHash)
	run := func(want int, args ...string) string {
		t.Helper()
		f.env[len(f.env)-1] = "TF_VAR_sha256=" + plannedHash
		return f.run(want, args...)
	}
	calls := func(want string) {
		t.Helper()
		data, err := os.ReadFile(filepath.Join(directory, "calls"))
		if want == "" && os.IsNotExist(err) {
			return
		}
		if err != nil || string(data) != want {
			t.Fatalf("calls: %q wanted %q: %v", data, want, err)
		}
	}
	run(0, "init", "-backend=false", "-no-color")
	run(2, "plan", "-detailed-exitcode", "-no-color")
	calls("")
	run(0, "apply", "-auto-approve", "-no-color")
	calls("one\n")
	run(0, "plan", "-detailed-exitcode", "-no-color")
	run(0, "apply", "-refresh-only", "-auto-approve", "-no-color")
	calls("one\n")
	run(0, "apply", "-var=input=two", "-auto-approve", "-no-color")
	calls("one\ntwo\n")
	write("program.mjs", source+"\n//new artifact\n")
	plannedHash = fmt.Sprintf("%x", sha256.Sum256([]byte(source+"\n//new artifact\n")))
	run(0, "apply", "-var=input=two", "-auto-approve", "-no-color")
	calls("one\ntwo\ntwo\n")
	run(0, "plan", "-var=input=three", "-out=saved.plan", "-no-color")
	write("program.mjs", source+"\n//changed after plan\n")
	if output := run(1, "apply", "-no-color", "saved.plan"); !strings.Contains(output, "changed after planning") {
		t.Fatal(output)
	}
	calls("one\ntwo\ntwo\n")
	write("program.mjs", `console.error("private-db-password"); process.exit(4);`)
	plannedHash = fmt.Sprintf("%x", sha256.Sum256([]byte(`console.error("private-db-password"); process.exit(4);`)))
	if output := run(1, "apply", "-var=input=two", "-auto-approve", "-no-color"); strings.Contains(output, "private-db-password") {
		t.Fatal("child credentials leaked")
	}
	state, err := os.ReadFile(filepath.Join(directory, "terraform.tfstate"))
	if err != nil {
		t.Fatal(err)
	}
	failedHash := fmt.Sprintf("%x", sha256.Sum256([]byte(`console.error("private-db-password"); process.exit(4);`)))
	if strings.Contains(string(state), failedHash) {
		t.Fatal("failed invocation recorded as successful")
	}
	deployed, err := os.ReadFile(filepath.Join(directory, "deployed"))
	if err != nil || string(deployed) != "one\ntwo\n" {
		t.Fatalf("failed prerequisite allowed downstream execution: %q %v", deployed, err)
	}
	run(0, "destroy", "-auto-approve", "-no-color")
	calls("one\ntwo\ntwo\n")
}
