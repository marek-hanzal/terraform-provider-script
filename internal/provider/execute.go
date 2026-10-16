package provider

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
	"unicode/utf8"
)

// stdout is a public data contract, never a diagnostic stream. Bound memory even
// for a broken builder; keep draining the pipe so an oversized output cannot hang.
const maxOutputBytes = 16 * 1024 * 1024

type outputBuffer struct {
	data     []byte
	overflow bool
}

func (b *outputBuffer) Write(p []byte) (int, error) {
	n := len(p)
	remaining := maxOutputBytes - len(b.data)
	if n > remaining {
		b.overflow = true
		p = p[:remaining]
	}
	b.data = append(b.data, p...)
	return n, nil
}

type invocation struct {
	Node, Script, SHA256, Directory string
	Environment                     map[string]string
	Arguments                       []string
	Stdin                           string
	Timeout                         time.Duration
	JSONOutput                      bool
}

func invoke(parent context.Context, in invocation) (string, error) {
	ctx, cancel := context.WithTimeout(parent, in.Timeout)
	defer cancel()
	if ctx.Err() != nil {
		return "", interruptedError()
	}
	artifact, err := os.ReadFile(in.Script)
	if err != nil {
		return "", errors.New("Cannot read the prepared JavaScript artifact.")
	}
	actual := sha256.Sum256(artifact)
	if hex.EncodeToString(actual[:]) != in.SHA256 {
		return "", errors.New("JavaScript artifact changed after planning; rebuild and create a fresh plan.")
	}
	directory := in.Directory
	if directory == "" {
		directory = filepath.Dir(in.Script)
	}
	command := exec.Command(in.Node, append([]string{in.Script}, in.Arguments...)...)
	command.Dir = directory
	// A non-nil empty slice prevents os/exec from inheriting the provider environment.
	command.Env = make([]string, 0, len(in.Environment))
	for key, value := range in.Environment {
		command.Env = append(command.Env, key+"="+value)
	}
	sort.Strings(command.Env)
	command.Stdin = strings.NewReader(in.Stdin)
	command.WaitDelay = 500 * time.Millisecond
	var output outputBuffer
	if in.JSONOutput {
		command.Stdout = &output
	}
	// stderr and resource stdout go to the null device, never logs/diagnostics.
	stop, err := startSupervised(command)
	if err != nil {
		return "", failedError()
	}
	stop = sync.OnceFunc(stop)
	defer stop()
	done := make(chan error, 1)
	go func() { done <- command.Wait() }()
	select {
	case err = <-done:
	case <-ctx.Done():
		stop()
		<-done // reap Node and finish bounded pipe copies before returning
		return "", interruptedError()
	}
	if ctx.Err() != nil {
		return "", interruptedError()
	}
	if err != nil {
		return "", failedError()
	}
	if in.JSONOutput {
		if output.overflow {
			return "", errors.New("Public stdout exceeded the 16 MiB limit. Emit only the JSON data contract, without logs or secrets.")
		}
		if !utf8.Valid(output.data) || !json.Valid(output.data) {
			return "", errors.New("Public stdout must contain exactly one valid UTF-8 JSON value. Send diagnostic logs to stderr; output was omitted to protect secrets.")
		}
	}
	return string(output.data), nil
}

func interruptedError() error {
	return errors.New("Node was interrupted or exceeded its timeout; supervised subprocesses were terminated. External effects may have completed. No automatic retry was attempted.")
}

func failedError() error {
	return errors.New("Node failed to start or exited unsuccessfully. Output was omitted to protect secrets; inspect the program locally. No automatic retry was attempted.")
}
