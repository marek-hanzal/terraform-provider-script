package provider

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

const testVersion = "0.2.0"

type terraformFixture struct {
	t                          *testing.T
	directory, terraform, node string
	env                        []string
}

func newTerraformFixture(t *testing.T) *terraformFixture {
	t.Helper()
	f := &terraformFixture{t: t, directory: t.TempDir()}
	var err error
	f.terraform, err = exec.LookPath("terraform")
	if err != nil {
		t.Fatal(err)
	}
	f.node, err = testNode()
	if err != nil {
		t.Fatal(err)
	}
	f.node, err = filepath.Abs(f.node)
	if err != nil {
		t.Fatal(err)
	}
	mirror := os.Getenv("SCRIPT_TEST_MIRROR")
	if mirror == "" {
		mirror = filepath.Join(f.directory, "mirror")
		binary := filepath.Join(mirror, "registry.terraform.io", "marek-hanzal", "script", testVersion, runtime.GOOS+"_"+runtime.GOARCH, "terraform-provider-script_v"+testVersion)
		if runtime.GOOS == "windows" {
			binary += ".exe"
		}
		if err := os.MkdirAll(filepath.Dir(binary), 0700); err != nil {
			t.Fatal(err)
		}
		build := exec.Command("go", "build", "-mod=readonly", "-trimpath", "-buildvcs=false", "-ldflags=-X main.version="+testVersion, "-o", binary, "../..")
		if output, err := build.CombinedOutput(); err != nil {
			t.Fatalf("build: %v\n%s", err, output)
		}
	} else {
		if !filepath.IsAbs(mirror) {
			t.Fatal("SCRIPT_TEST_MIRROR must be absolute")
		}
		t.Log("testing prepared provider mirror:", mirror)
	}
	f.write("terraform.rc", fmt.Sprintf(`provider_installation { filesystem_mirror { path = %q } }`, filepath.ToSlash(mirror)))
	// Terraform itself has no cloud credentials or user CLI config. Fake sentinels
	// prove the provider does not forward its own environment to the builder.
	f.env = []string{
		"PATH=" + filepath.Dir(f.node), "HOME=" + f.directory, "TMPDIR=" + os.TempDir(),
		"TF_CLI_CONFIG_FILE=" + filepath.Join(f.directory, "terraform.rc"), "TF_INPUT=0", "CHECKPOINT_DISABLE=1",
		"AWS_SECRET_ACCESS_KEY=parent-sentinel", "NODE_OPTIONS=--invalid-parent-sentinel",
	}
	if runtime.GOOS == "windows" {
		f.env = append(f.env, "SystemRoot="+os.Getenv("SystemRoot"), "TEMP="+f.directory, "USERPROFILE="+f.directory)
	}
	return f
}

func (f *terraformFixture) write(name, value string) {
	f.t.Helper()
	if err := os.WriteFile(filepath.Join(f.directory, name), []byte(value), 0600); err != nil {
		f.t.Fatal(err)
	}
}

func (f *terraformFixture) command(args ...string) *exec.Cmd {
	command := exec.Command(f.terraform, args...)
	command.Dir, command.Env = f.directory, f.env
	return command
}

func (f *terraformFixture) run(want int, args ...string) string {
	f.t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	command := exec.CommandContext(ctx, f.terraform, args...)
	command.Dir, command.Env = f.directory, f.env
	command.WaitDelay = 2 * time.Second
	output, err := command.CombinedOutput()
	status := 0
	if err != nil {
		exit, ok := err.(*exec.ExitError)
		if !ok {
			f.t.Fatal(err)
		}
		status = exit.ExitCode()
	}
	if status != want {
		f.t.Fatalf("terraform %v: status %d wanted %d\n%s", args, status, want, output)
	}
	return string(output)
}

func (f *terraformFixture) lines(name string) int {
	f.t.Helper()
	data, err := os.ReadFile(filepath.Join(f.directory, name))
	if os.IsNotExist(err) {
		return 0
	}
	if err != nil {
		f.t.Fatal(err)
	}
	return strings.Count(string(data), "\n")
}

func (f *terraformFixture) counts(builds, deploys int) {
	f.t.Helper()
	if got := f.lines("build-calls"); got != builds {
		f.t.Fatalf("builder calls %d, wanted %d", got, builds)
	}
	if got := f.lines("deploy-calls"); got != deploys {
		f.t.Fatalf("deploy calls %d, wanted %d", got, deploys)
	}
}

func testNode() (string, error) {
	// mise exec resolves its direct command, but Go's later LookPath can still
	// find a global Node first on this host. Resolve the repository pin explicitly.
	output, err := exec.Command("mise", "which", "node").Output()
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(output)), nil
}
