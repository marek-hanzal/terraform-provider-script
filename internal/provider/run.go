package provider

import (
	"context"
	"errors"
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
			"timeout_seconds":   schema.Int64Attribute{Optional: true, Computed: true, Default: int64default.StaticInt64(300), Description: "Execution deadline in seconds, from 1 to 3600. Interruption terminates Node and its supervised subprocesses; it does not roll back external effects."},
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
	resp.Diagnostics.Append(validateExecution(model.Script, model.SHA256, model.WorkingDirectory, model.Environment, model.TimeoutSeconds)...)
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
	_, err := invoke(ctx, invocation{
		Node: r.node, Script: model.Script.ValueString(), SHA256: model.SHA256.ValueString(),
		Directory: model.WorkingDirectory.ValueString(), Environment: environment,
		Timeout: time.Duration(model.TimeoutSeconds.ValueInt64()) * time.Second,
	})
	return err
}
