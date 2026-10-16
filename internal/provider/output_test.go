package provider

import (
	"bytes"
	"context"
	"crypto/sha256"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

func TestOutputExecution(t *testing.T) {
	node, err := testNode()
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("AWS_SECRET_ACCESS_KEY", "parent-secret")
	t.Setenv("NODE_OPTIONS", "--invalid-parent-option")
	for _, tc := range []struct{ name, source, want, failure string }{
		{"public JSON and literal arguments/stdin", `import {readFileSync} from 'node:fs'; if (['HOME', 'PATH', 'NODE_OPTIONS', 'AWS_SECRET_ACCESS_KEY'].some(k => process.env[k])) process.exit(8); if (process.env.EXPLICIT !== 'only') process.exit(9); console.error('diagnostic-secret'); process.stdout.write(JSON.stringify({args: process.argv.slice(2), stdin: readFileSync(0, 'utf8')}));`, `{"args":["space value","$(touch nope)","--flag"],"stdin":"input\u0000data"}`, ""},
		{"empty output", `console.error('private-secret');`, "", "valid UTF-8 JSON"},
		{"invalid JSON", `process.stdout.write('private-secret'); console.error('private-secret');`, "", "valid UTF-8 JSON"},
		{"multiple JSON values", `process.stdout.write('{}\n{}');`, "", "valid UTF-8 JSON"},
		{"invalid UTF-8", `process.stdout.write(Buffer.from([34,255,34]));`, "", "valid UTF-8 JSON"},
		{"nonzero exit with JSON", `console.log('{}'); console.error('private-secret'); process.exit(7);`, "", "exited unsuccessfully"},
		{"bounded output", `process.stdout.write('"'+'a'.repeat(16*1024*1024)+'"');`, "", "16 MiB"},
		{"JSON null is valid", `process.stdout.write('null');`, "null", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			script := filepath.Join(dir, "builder with spaces.mjs")
			if err := os.WriteFile(script, []byte(tc.source), 0600); err != nil {
				t.Fatal(err)
			}
			output, err := invoke(context.Background(), invocation{
				Node: node, Script: script, SHA256: fmt.Sprintf("%x", sha256.Sum256([]byte(tc.source))), Directory: dir,
				Environment: map[string]string{"EXPLICIT": "only"}, Arguments: []string{"space value", "$(touch nope)", "--flag"}, Stdin: "input\x00data", Timeout: 5 * time.Second, JSONOutput: true,
			})
			if tc.failure == "" {
				if err != nil || output != tc.want {
					t.Fatalf("output %q, error %v", output, err)
				}
			} else if err == nil || !strings.Contains(err.Error(), tc.failure) || strings.Contains(err.Error(), "private-secret") || output != "" {
				t.Fatalf("unsafe or missing diagnostic: %q %v", output, err)
			}
		})
	}
}

// Exercise an actual child and grandchild, including cancellation after startup.
func TestSubprocessCleanup(t *testing.T) {
	node, err := testNode()
	if err != nil {
		t.Fatal(err)
	}
	for _, mode := range []string{"timeout", "cancellation", "successful parent exit"} {
		t.Run(mode, func(t *testing.T) {
			dir := t.TempDir()
			child := `import {spawn} from 'node:child_process'; const c = spawn(process.execPath, ['-e', "const fs=require('fs'); setInterval(()=>fs.appendFileSync('heartbeat','x'),20)"], {stdio:'ignore'}); setInterval(()=>{},1000);`
			if err := os.WriteFile(filepath.Join(dir, "child.mjs"), []byte(child), 0600); err != nil {
				t.Fatal(err)
			}
			source := `import {spawn} from 'node:child_process'; import {existsSync} from 'node:fs'; spawn(process.execPath, ['child.mjs'], {stdio:'ignore'}).unref(); const timer = setInterval(()=>{if(existsSync('heartbeat')) {`
			if mode == "successful parent exit" {
				source += `console.log('{}'); process.exit(0);`
			} else {
				source += `clearInterval(timer); setInterval(()=>{},1000);`
			}
			source += `}},10);`
			script := filepath.Join(dir, "builder.mjs")
			if err := os.WriteFile(script, []byte(source), 0600); err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			if mode == "cancellation" {
				go func() {
					for ctx.Err() == nil {
						if _, err := os.Stat(filepath.Join(dir, "heartbeat")); err == nil {
							cancel()
							return
						}
						time.Sleep(10 * time.Millisecond)
					}
				}()
			}
			timeout := 2 * time.Second
			start := time.Now()
			_, err := invoke(ctx, invocation{Node: node, Script: script, SHA256: fmt.Sprintf("%x", sha256.Sum256([]byte(source))), Directory: dir, Timeout: timeout, JSONOutput: true})
			if mode == "successful parent exit" {
				if err != nil {
					t.Fatal(err)
				}
			} else if err == nil || !strings.Contains(err.Error(), "interrupted") {
				t.Fatalf("unexpected result: %v", err)
			}
			if time.Since(start) > 4*time.Second {
				t.Fatal("cleanup did not finish promptly")
			}
			assertHeartbeatStopped(t, filepath.Join(dir, "heartbeat"))
		})
	}
}

func assertHeartbeatStopped(t *testing.T, path string) {
	t.Helper()
	// Allow asynchronous kernel termination to settle, then prove no descendant runs.
	time.Sleep(100 * time.Millisecond)
	before, err := os.ReadFile(path)
	if err != nil || len(before) == 0 {
		t.Fatalf("descendant never started: %v", err)
	}
	time.Sleep(150 * time.Millisecond)
	after, err := os.ReadFile(path)
	if err != nil || !bytes.Equal(before, after) {
		t.Fatalf("descendant still running: %v", err)
	}
}

func TestTerraformOutputLifecycle(t *testing.T) {
	f := newTerraformFixture(t)
	f.write("main.tf", fmt.Sprintf(`terraform {
  required_providers { script = { source = "marek-hanzal/script", version = "= %s" } }
}
provider "script" { node_path = %q }
data "script_output" "build" {
  script = abspath("${path.module}/builder.mjs")
  sha256 = filesha256("${path.module}/builder.mjs")
  working_directory = abspath(path.module)
  environment = { EXPLICIT = "yes" }
  arguments = ["literal space", "--literal"]
  stdin = "stdin payload"
}
locals { build = jsondecode(data.script_output.build.stdout) }
resource "script_run" "deploy" {
  for_each = local.build
  script = abspath("${path.module}/deploy.mjs")
  sha256 = filesha256("${path.module}/deploy.mjs")
  environment = { DIGEST = each.value.sha256, FILE = each.value.path }
  timeout_seconds = 5
}
`, testVersion, filepath.ToSlash(f.node)))
	builder := `import {mkdirSync,readFileSync,writeFileSync,appendFileSync} from 'node:fs';
import {createHash} from 'node:crypto';
if (process.env.EXPLICIT !== 'yes' || ['HOME','PATH','NODE_OPTIONS','AWS_SECRET_ACCESS_KEY'].some(k=>process.env[k])) process.exit(9);
if (JSON.stringify(process.argv.slice(2)) !== '["literal space","--literal"]' || readFileSync(0,'utf8') !== 'stdin payload') process.exit(8);
appendFileSync('build-calls','build\n');
mkdirSync('disposable',{recursive:true});
writeFileSync('disposable/bundle',JSON.parse(readFileSync('source.json','utf8')).content);
const sha256=createHash('sha256').update(readFileSync('disposable/bundle')).digest('hex');
process.stdout.write(JSON.stringify({asset:{path:'disposable/bundle',sha256}}));`
	f.write("builder.mjs", builder)
	f.write("deploy.mjs", `import {readFileSync,appendFileSync} from 'node:fs'; import {createHash} from 'node:crypto'; if (createHash('sha256').update(readFileSync(process.env.FILE)).digest('hex') !== process.env.DIGEST) process.exit(6); appendFileSync('deploy-calls',process.env.DIGEST+'\n');`)
	f.write("source.json", `{"content":"one"}`)
	f.run(0, "init", "-backend=false", "-no-color")
	f.run(2, "plan", "-detailed-exitcode", "-out=first.plan", "-no-color")
	f.counts(1, 0)
	f.run(0, "apply", "-no-color", "first.plan")
	f.counts(1, 1)
	t.Log("first plan builds; saved-plan apply deploys once without rebuilding")
	if out := f.run(0, "plan", "-detailed-exitcode", "-no-color"); !strings.Contains(out, "No changes") {
		t.Fatal(out)
	}
	f.counts(2, 1)
	f.write("builder.mjs", builder+"\n// different entrypoint bytes, same compiled output\n")
	f.write("source.json", "{\n  \"content\": \"one\"\n}\n")
	f.run(0, "plan", "-detailed-exitcode", "-out=same.plan", "-no-color")
	f.counts(3, 1)
	f.run(0, "apply", "-no-color", "same.plan")
	f.counts(3, 1)
	t.Log("entrypoint and source changes with identical JSON cause no deploy")
	if err := os.RemoveAll(filepath.Join(f.directory, "disposable")); err != nil {
		t.Fatal(err)
	}
	f.run(0, "plan", "-detailed-exitcode", "-no-color")
	f.counts(4, 1)
	if _, err := os.Stat(filepath.Join(f.directory, "disposable", "bundle")); err != nil {
		t.Fatal(err)
	}
	t.Log("missing disposable outputs are restored with no deploy change")
	f.write("source.json", `{"content":"two"}`)
	f.run(2, "plan", "-detailed-exitcode", "-out=changed.plan", "-no-color")
	f.counts(5, 1)
	f.run(0, "apply", "-no-color", "changed.plan")
	f.counts(5, 2)
	t.Log("changed real artifact hash plans and applies one deploy; apply does not rebuild")
	for _, bad := range []struct{ source, diagnostic string }{
		{`console.log('private-child-secret'); console.error('private-child-secret');`, "valid UTF-8 JSON"},
		{`console.log('{}'); console.error('private-child-secret'); process.exit(3);`, "exited unsuccessfully"},
	} {
		f.write("builder.mjs", bad.source)
		out := f.run(1, "plan", "-no-color")
		if !strings.Contains(out, bad.diagnostic) || strings.Contains(out, "private-child-secret") {
			t.Fatal(out)
		}
		f.counts(5, 2)
	}
	f.write("builder.mjs", builder)
	f.write("main.tf", strings.Replace(readTestFile(t, filepath.Join(f.directory, "main.tf")), `sha256 = filesha256("${path.module}/builder.mjs")`, `sha256 = "`+strings.Repeat("0", 64)+`"`, 1))
	if out := f.run(1, "plan", "-no-color"); !strings.Contains(out, "changed after planning") {
		t.Fatal(out)
	}
	f.counts(5, 2)
	t.Log("invalid JSON, nonzero exit, and hash mismatch stop plan without child diagnostics or deploy")
}

func TestTerraformOutputInterruption(t *testing.T) {
	f := newTerraformFixture(t)
	f.write("main.tf", fmt.Sprintf(`terraform {
 required_providers { script = { source = "marek-hanzal/script", version = "= %s" } }
}
provider "script" { node_path = %q }
variable "timeout" { default = 1 }
data "script_output" "build" {
 script = abspath("${path.module}/builder.mjs")
 sha256 = filesha256("${path.module}/builder.mjs")
 working_directory = abspath(path.module)
 environment = {}
 timeout_seconds = var.timeout
}
`, testVersion, filepath.ToSlash(f.node)))
	f.write("builder.mjs", `import {spawn} from 'node:child_process'; spawn(process.execPath,['-e',"const fs=require('fs'); setInterval(()=>fs.appendFileSync('heartbeat','x'),20)"],{stdio:'ignore'}); setInterval(()=>{},1000);`)
	f.run(0, "init", "-backend=false", "-no-color")
	if out := f.run(1, "plan", "-no-color"); !strings.Contains(out, "interrupted") {
		t.Fatal(out)
	}
	assertHeartbeatStopped(t, filepath.Join(f.directory, "heartbeat"))
	t.Log("Terraform plan timeout terminates builder and child")
	if runtime.GOOS == "windows" {
		t.Log("CLI interrupt test skipped on Windows; context cancellation covered directly")
		return
	}
	if err := os.Remove(filepath.Join(f.directory, "heartbeat")); err != nil {
		t.Fatal(err)
	}
	command := f.command("plan", "-var=timeout=30", "-no-color")
	var output bytes.Buffer
	command.Stdout, command.Stderr = &output, &output
	if err := command.Start(); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- command.Wait() }()
	deadline := time.Now().Add(10 * time.Second)
	for {
		if _, err := os.Stat(filepath.Join(f.directory, "heartbeat")); err == nil {
			break
		}
		if time.Now().After(deadline) {
			_ = command.Process.Kill()
			<-done
			t.Fatal("builder did not start")
		}
		time.Sleep(20 * time.Millisecond)
	}
	if err := command.Process.Signal(os.Interrupt); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("canceled plan succeeded")
		}
	case <-time.After(10 * time.Second):
		_ = command.Process.Kill()
		<-done
		t.Fatal("canceled plan did not terminate")
	}
	assertHeartbeatStopped(t, filepath.Join(f.directory, "heartbeat"))
	t.Log("SIGINT to Terraform cancels the provider and terminates builder descendants")
}

func readTestFile(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}
