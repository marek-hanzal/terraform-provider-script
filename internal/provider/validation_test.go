package provider

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

func TestExecutionValidation(t *testing.T) {
	directory := t.TempDir()
	for _, tc := range []struct {
		name                    string
		script, hash, directory types.String
		env                     types.Map
		timeout                 types.Int64
		bad                     bool
	}{
		{"known valid", types.StringValue(filepath.Join(directory, "builder.mjs")), types.StringValue(strings.Repeat("a", 64)), types.StringValue(directory), types.MapNull(types.StringType), types.Int64Value(300), false},
		{"unknown deferred", types.StringUnknown(), types.StringUnknown(), types.StringUnknown(), types.MapUnknown(types.StringType), types.Int64Unknown(), false},
		{"relative script", types.StringValue("relative.mjs"), types.StringNull(), types.StringNull(), types.MapNull(types.StringType), types.Int64Null(), true},
		{"relative directory", types.StringNull(), types.StringNull(), types.StringValue("relative"), types.MapNull(types.StringType), types.Int64Null(), true},
		{"uppercase hash", types.StringNull(), types.StringValue(strings.Repeat("A", 64)), types.StringNull(), types.MapNull(types.StringType), types.Int64Null(), true},
		{"short hash", types.StringNull(), types.StringValue("aa"), types.StringNull(), types.MapNull(types.StringType), types.Int64Null(), true},
		{"zero timeout", types.StringNull(), types.StringNull(), types.StringNull(), types.MapNull(types.StringType), types.Int64Value(0), true},
		{"excess timeout", types.StringNull(), types.StringNull(), types.StringNull(), types.MapNull(types.StringType), types.Int64Value(3601), true},
		{"invalid env name", types.StringNull(), types.StringNull(), types.StringNull(), types.MapValueMust(types.StringType, map[string]attr.Value{"INVALID=KEY": types.StringValue("secret")}), types.Int64Null(), true},
		{"null env value", types.StringNull(), types.StringNull(), types.StringNull(), types.MapValueMust(types.StringType, map[string]attr.Value{"KEY": types.StringNull()}), types.Int64Null(), true},
		{"NUL env value", types.StringNull(), types.StringNull(), types.StringNull(), types.MapValueMust(types.StringType, map[string]attr.Value{"KEY": types.StringValue("secret\x00value")}), types.Int64Null(), true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			diagnostics := validateExecution(tc.script, tc.hash, tc.directory, tc.env, tc.timeout)
			if diagnostics.HasError() != tc.bad {
				t.Fatalf("unexpected diagnostics: %v", diagnostics)
			}
			for _, d := range diagnostics {
				if strings.Contains(d.Detail(), "secret") {
					t.Fatal("diagnostic leaked environment value")
				}
			}
		})
	}
}
