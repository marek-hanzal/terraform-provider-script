package provider

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/int64default"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

type runResource struct{ node string }

type runModel struct {
	ID               types.String `tfsdk:"id"`
	Script           types.String `tfsdk:"script"`
	SHA256           types.String `tfsdk:"sha256"`
	WorkingDirectory types.String `tfsdk:"working_directory"`
	Environment      types.Map    `tfsdk:"environment"`
	TimeoutSeconds   types.Int64  `tfsdk:"timeout_seconds"`
}

func (r *runResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_run"
}

func (r *runResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		Description: "Records successful execution of a prepared JavaScript file. Create/Update execute once; Read and Delete never execute. The program owns its own idempotency and external data lifecycle.",
		Attributes: map[string]schema.Attribute{
			"id":                schema.StringAttribute{Computed: true, Description: "SHA-256 of the last successfully executed artifact."},
			"script":            schema.StringAttribute{Required: true, Description: "Absolute path to a prepared, standalone JavaScript file. Keep it unchanged between plan and apply."},
			"sha256":            schema.StringAttribute{Required: true, Description: "Expected lowercase hexadecimal SHA-256 of the artifact; checked immediately before execution."},
			"working_directory": schema.StringAttribute{Optional: true, Description: "Absolute working directory; defaults to the script's directory."},
			"environment":       schema.MapAttribute{Optional: true, Sensitive: true, ElementType: types.StringType, Description: "Complete explicit child environment. No Terraform/cloud environment is inherited. Values remain sensitive configuration in Terraform state."},
			"timeout_seconds":   schema.Int64Attribute{Optional: true, Computed: true, Default: int64default.StaticInt64(300), Description: "Execution deadline in seconds, from 1 to 3600. Interruption kills and reaps Node; it does not roll back external effects."},
		},
	}
}

func (r *runResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
	if req.ProviderData == nil {
		return
	}
	node, ok := req.ProviderData.(string)
	if !ok {
		resp.Diagnostics.AddError("Invalid provider configuration", "Expected resolved Node executable.")
		return
	}
	r.node = node
}

func (r *runResource) ValidateConfig(ctx context.Context, req resource.ValidateConfigRequest, resp *resource.ValidateConfigResponse) {
	var model runModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &model)...)
	if resp.Diagnostics.HasError() {
		return
	}
	if !model.Script.IsUnknown() && !model.Script.IsNull() && !filepath.IsAbs(model.Script.ValueString()) {
		resp.Diagnostics.AddError("Invalid script path", "script must be an absolute path.")
	}
	if !model.WorkingDirectory.IsUnknown() && !model.WorkingDirectory.IsNull() && !filepath.IsAbs(model.WorkingDirectory.ValueString()) {
		resp.Diagnostics.AddError("Invalid working directory", "working_directory must be an absolute path.")
	}
	if !model.SHA256.IsUnknown() && !model.SHA256.IsNull() {
		hash := model.SHA256.ValueString()
		decoded, err := hex.DecodeString(hash)
		if err != nil || len(decoded) != sha256.Size || hash != strings.ToLower(hash) {
			resp.Diagnostics.AddError("Invalid artifact hash", "sha256 must be 64 lowercase hexadecimal characters.")
		}
	}
	if !model.TimeoutSeconds.IsUnknown() && !model.TimeoutSeconds.IsNull() && (model.TimeoutSeconds.ValueInt64() < 1 || model.TimeoutSeconds.ValueInt64() > 3600) {
		resp.Diagnostics.AddError("Invalid timeout", "timeout_seconds must be between 1 and 3600.")
	}
	if !model.Environment.IsUnknown() && !model.Environment.IsNull() {
		for key, value := range model.Environment.Elements() {
			if key == "" || strings.ContainsAny(key, "=\x00") || (!value.IsUnknown() && value.IsNull()) {
				resp.Diagnostics.AddError("Invalid environment", "Environment names must be nonempty without '=' or NUL; values must be non-null strings.")
			}
			if v, ok := value.(types.String); ok && !v.IsUnknown() && strings.ContainsRune(v.ValueString(), 0) {
				resp.Diagnostics.AddError("Invalid environment", "Environment values must not contain NUL.")
			}
		}
	}
}

func (r *runResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var model runModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &model)...)
	if resp.Diagnostics.HasError() {
		return
	}
	if err := r.execute(ctx, model); err != nil {
		resp.Diagnostics.AddError("Script execution failed", err.Error())
		return
	}
	model.ID = model.SHA256
	resp.Diagnostics.Append(resp.State.Set(ctx, &model)...)
}

func (r *runResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var model runModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &model)...)
	if resp.Diagnostics.HasError() {
		return
	}
	if err := r.execute(ctx, model); err != nil {
		resp.Diagnostics.AddError("Script execution failed", err.Error())
		return
	}
	model.ID = model.SHA256
	resp.Diagnostics.Append(resp.State.Set(ctx, &model)...)
}

// State describes a successful invocation, not live external data. Refresh never reruns it.
func (r *runResource) Read(_ context.Context, _ resource.ReadRequest, _ *resource.ReadResponse) {}
func (r *runResource) Delete(_ context.Context, _ resource.DeleteRequest, _ *resource.DeleteResponse) {
}

func (r *runResource) execute(ctx context.Context, model runModel) error {
	var environment map[string]string
	if diagnostics := model.Environment.ElementsAs(ctx, &environment, false); diagnostics.HasError() {
		return errors.New("Child environment must contain known non-null strings.")
	}
	env := make([]string, 0, len(environment))
	for key, value := range environment {
		env = append(env, key+"="+value)
	}
	sort.Strings(env)
	return execute(ctx, r.node, model.Script.ValueString(), model.SHA256.ValueString(), model.WorkingDirectory.ValueString(), env, time.Duration(model.TimeoutSeconds.ValueInt64())*time.Second)
}

func execute(ctx context.Context, node, script, expected, directory string, env []string, timeout time.Duration) error {
	artifact, err := os.ReadFile(script)
	if err != nil {
		return errors.New("Cannot read the prepared JavaScript artifact.")
	}
	actual := sha256.Sum256(artifact)
	if hex.EncodeToString(actual[:]) != expected {
		return errors.New("JavaScript artifact changed after planning; rebuild and create a fresh plan.")
	}
	if directory == "" {
		directory = filepath.Dir(script)
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	command := exec.CommandContext(ctx, node, script)
	command.Dir = directory
	command.Env = env
	// Discard both streams: arbitrary JS or driver errors can contain database credentials.
	command.Stdout, command.Stderr = nil, nil
	err = command.Run()
	if ctx.Err() != nil {
		return errors.New("Node was interrupted or exceeded its timeout; external effects may have completed. No automatic retry was attempted.")
	}
	if err != nil {
		return errors.New("Node failed to start or exited unsuccessfully. Output was discarded to protect secrets; inspect the program locally. No automatic retry was attempted.")
	}
	return nil
}
