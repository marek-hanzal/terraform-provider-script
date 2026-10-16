package provider

import (
	"context"
	"strings"
	"time"

	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/datasource/schema"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

type outputDataSource struct{ node string }

type outputModel struct {
	Script           types.String `tfsdk:"script"`
	SHA256           types.String `tfsdk:"sha256"`
	WorkingDirectory types.String `tfsdk:"working_directory"`
	Environment      types.Map    `tfsdk:"environment"`
	TimeoutSeconds   types.Int64  `tfsdk:"timeout_seconds"`
	Arguments        types.List   `tfsdk:"arguments"`
	Stdin            types.String `tfsdk:"stdin"`
	Stdout           types.String `tfsdk:"stdout"`
}

var (
	_ datasource.DataSourceWithConfigure      = &outputDataSource{}
	_ datasource.DataSourceWithValidateConfig = &outputDataSource{}
)

func (d *outputDataSource) Metadata(_ context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_output"
}

func (d *outputDataSource) Schema(_ context.Context, _ datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	resp.Schema = schema.Schema{
		Description: "Runs a local builder during plan when inputs are known. The program may only produce declared disposable local outputs; cloud writes are forbidden. stdout is explicitly public JSON, without secrets.",
		Attributes: map[string]schema.Attribute{
			"script":            schema.StringAttribute{Required: true, Description: "Absolute path to a prepared JavaScript entrypoint."},
			"sha256":            schema.StringAttribute{Required: true, Description: "Expected lowercase hexadecimal SHA-256, verified immediately before execution."},
			"working_directory": schema.StringAttribute{Required: true, Description: "Absolute working directory for the local builder."},
			"environment":       schema.MapAttribute{Required: true, Sensitive: true, ElementType: types.StringType, Description: "Complete explicit child environment, including any required toolchain paths. No parent environment is inherited. Use {} for empty. Stored in state and saved plans."},
			"timeout_seconds":   schema.Int64Attribute{Optional: true, Computed: true, Description: "Execution deadline in seconds, 1–3600. Defaults to 300. Timeout/cancellation terminates supervised subprocesses."},
			"arguments":         schema.ListAttribute{Optional: true, ElementType: types.StringType, Description: "Arguments passed literally after the entrypoint path, without a shell. Must be non-null strings without NUL."},
			"stdin":             schema.StringAttribute{Optional: true, Sensitive: true, Description: "Optional UTF-8 input sent to the program, then closed. Defaults to empty. Stored as sensitive configuration."},
			"stdout":            schema.StringAttribute{Computed: true, Description: "Explicitly public, non-sensitive UTF-8 JSON data contract (up to 16 MiB). Must never contain secrets. Use jsondecode; public keys can be used in for_each. Diagnostic logs belong on stderr and are discarded."},
		},
	}
}

func (d *outputDataSource) Configure(_ context.Context, req datasource.ConfigureRequest, resp *datasource.ConfigureResponse) {
	if req.ProviderData == nil {
		return
	}
	node, ok := req.ProviderData.(string)
	if !ok {
		resp.Diagnostics.AddError("Invalid provider configuration", "Expected resolved Node executable.")
		return
	}
	d.node = node
}

func (d *outputDataSource) ValidateConfig(ctx context.Context, req datasource.ValidateConfigRequest, resp *datasource.ValidateConfigResponse) {
	var model outputModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &model)...)
	if resp.Diagnostics.HasError() {
		return
	}
	resp.Diagnostics.Append(validateExecution(model.Script, model.SHA256, model.WorkingDirectory, model.Environment, model.TimeoutSeconds)...)
	if !model.Arguments.IsUnknown() && !model.Arguments.IsNull() {
		for _, value := range model.Arguments.Elements() {
			v, ok := value.(types.String)
			if value.IsNull() || (ok && !v.IsUnknown() && strings.ContainsRune(v.ValueString(), 0)) {
				resp.Diagnostics.AddError("Invalid arguments", "arguments must contain non-null strings without NUL.")
			}
		}
	}
}

func (d *outputDataSource) Read(ctx context.Context, req datasource.ReadRequest, resp *datasource.ReadResponse) {
	var model outputModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &model)...)
	if resp.Diagnostics.HasError() {
		return
	}
	var environment map[string]string
	var arguments []string
	resp.Diagnostics.Append(model.Environment.ElementsAs(ctx, &environment, false)...)
	resp.Diagnostics.Append(model.Arguments.ElementsAs(ctx, &arguments, false)...)
	if resp.Diagnostics.HasError() {
		return
	}
	if model.TimeoutSeconds.IsNull() {
		model.TimeoutSeconds = types.Int64Value(300)
	}
	stdout, err := invoke(ctx, invocation{
		Node: d.node, Script: model.Script.ValueString(), SHA256: model.SHA256.ValueString(),
		Directory: model.WorkingDirectory.ValueString(), Environment: environment,
		Arguments: arguments, Stdin: model.Stdin.ValueString(),
		Timeout: time.Duration(model.TimeoutSeconds.ValueInt64()) * time.Second, JSONOutput: true,
	})
	if err != nil {
		resp.Diagnostics.AddError("Script output failed", err.Error())
		return
	}
	model.Stdout = types.StringValue(stdout)
	resp.Diagnostics.Append(resp.State.Set(ctx, &model)...)
}
