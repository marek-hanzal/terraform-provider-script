package provider

import (
	"context"
	"os/exec"
	"path/filepath"

	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/provider"
	"github.com/hashicorp/terraform-plugin-framework/provider/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

type scriptProvider struct{ version string }

func New(version string) func() provider.Provider {
	return func() provider.Provider { return &scriptProvider{version: version} }
}

func (p *scriptProvider) Metadata(_ context.Context, _ provider.MetadataRequest, resp *provider.MetadataResponse) {
	resp.TypeName = "script"
	resp.Version = p.version
}

func (p *scriptProvider) Schema(_ context.Context, _ provider.SchemaRequest, resp *provider.SchemaResponse) {
	resp.Schema = schema.Schema{Attributes: map[string]schema.Attribute{
		"node_path": schema.StringAttribute{Optional: true, Description: "Node executable path. Defaults to node resolved on the provider's PATH; the child does not inherit PATH or other environment."},
	}}
}

func (p *scriptProvider) Configure(ctx context.Context, req provider.ConfigureRequest, resp *provider.ConfigureResponse) {
	var config struct {
		NodePath types.String `tfsdk:"node_path"`
	}
	resp.Diagnostics.Append(req.Config.Get(ctx, &config)...)
	if resp.Diagnostics.HasError() {
		return
	}
	if config.NodePath.IsUnknown() {
		resp.Diagnostics.AddError("Unknown Node executable", "node_path must be known before apply.")
		return
	}
	node := config.NodePath.ValueString()
	if config.NodePath.IsNull() {
		node = "node"
	}
	path, err := exec.LookPath(node)
	if err != nil {
		resp.Diagnostics.AddError("Node executable unavailable", "Install Node or set node_path to an existing executable.")
		return
	}
	absolute, err := filepath.Abs(path)
	if err != nil {
		resp.Diagnostics.AddError("Invalid Node executable", "Cannot resolve Node executable path.")
		return
	}
	resp.ResourceData = absolute
}

func (p *scriptProvider) Resources(_ context.Context) []func() resource.Resource {
	return []func() resource.Resource{func() resource.Resource { return &runResource{} }}
}

func (p *scriptProvider) DataSources(_ context.Context) []func() datasource.DataSource { return nil }
